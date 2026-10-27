// Command linkcheck validates local links and anchors in Markdown files.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

var skippedDirectories = map[string]struct{}{
	".git": {}, ".worktrees": {}, ".notes": {}, "dist": {}, "bin": {},
	"node_modules": {}, "testdata": {},
}

type link struct {
	line   int
	target string
}

type finding struct {
	path     string
	line     int
	message  string
	external bool
}

func main() {
	root := flag.String("root", ".", "repository root to walk")
	external := flag.Bool("external", false, "list external links")
	flag.Parse()

	failed, err := run(*root, *external, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "linkcheck:", err)
		os.Exit(2)
	}
	if failed {
		os.Exit(1)
	}
}

// run centralizes sorting and output so command behavior stays deterministic.
func run(root string, includeExternal bool, output io.Writer) (bool, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false, fmt.Errorf("resolve root: %w", err)
	}
	paths, err := markdownFiles(absRoot)
	if err != nil {
		return false, err
	}

	findings := make([]finding, 0)
	anchorCache := make(map[string]map[string]struct{})
	for _, path := range paths {
		//nolint:gosec // Paths come only from the requested repository walk.
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return false, fmt.Errorf("read %s: %w", path, readErr)
		}
		rel, relErr := filepath.Rel(absRoot, path)
		if relErr != nil {
			return false, fmt.Errorf("make path relative: %w", relErr)
		}
		rel = filepath.ToSlash(rel)
		for _, item := range extractLinks(data) {
			if isExternal(item.target) {
				if includeExternal {
					findings = append(findings, finding{rel, item.line, "external: " + item.target, true})
				}
				continue
			}
			message := checkTarget(absRoot, path, item.target, anchorCache)
			if message != "" {
				findings = append(findings, finding{rel, item.line, message, false})
			}
		}
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].path != findings[j].path {
			return findings[i].path < findings[j].path
		}
		if findings[i].line != findings[j].line {
			return findings[i].line < findings[j].line
		}
		return findings[i].message < findings[j].message
	})
	failed := false
	for _, problem := range findings {
		if _, err := fmt.Fprintf(output, "%s:%d: %s\n", problem.path, problem.line, problem.message); err != nil {
			return false, fmt.Errorf("write output: %w", err)
		}
		failed = failed || !problem.external
	}
	return failed, nil
}

// markdownFiles excludes repositories and bulky generated dependency trees from the scan.
func markdownFiles(root string) ([]string, error) {
	paths := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk %s: %w", path, walkErr)
		}
		if entry.IsDir() {
			if path != root {
				if _, skip := skippedDirectories[entry.Name()]; skip {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".md") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk Markdown files: %w", err)
	}
	sort.Strings(paths)
	return paths, nil
}

// extractLinks removes code before recognizing links to avoid documentation examples becoming findings.
func extractLinks(data []byte) []link {
	lines := splitLines(data)
	links := make([]link, 0)
	fence := byte(0)
	inlineTicks := 0
	for index := 0; index < len(lines); index++ {
		lineText := lines[index]
		if marker, ok := fenceMarker(lineText); ok {
			switch fence {
			case 0:
				fence = marker
			case marker:
				fence = 0
			}
			continue
		}
		if fence != 0 {
			continue
		}
		clean := stripInlineCode(lineText, &inlineTicks)
		if target, ok := referenceTarget(clean); ok {
			if target == "" && index+1 < len(lines) {
				index++
				target = strings.TrimSpace(stripInlineCode(lines[index], &inlineTicks))
				if target != "" {
					links = append(links, link{index + 1, normalizeTarget(target)})
				}
			} else if target != "" {
				links = append(links, link{index + 1, normalizeTarget(target)})
			}
			continue
		}
		links = append(links, inlineLinks(clean, index+1)...)
	}
	return links
}

// splitLines normalizes scanner input without imposing bufio.Scanner's token limit.
func splitLines(data []byte) []string {
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	return strings.Split(string(data), "\n")
}

// fenceMarker treats longer backtick and tilde runs as their Markdown fence family.
func fenceMarker(lineText string) (byte, bool) {
	trimmed := strings.TrimLeft(lineText, " \t")
	if len(trimmed) < 3 || (trimmed[0] != '`' && trimmed[0] != '~') {
		return 0, false
	}
	marker := trimmed[0]
	count := 0
	for count < len(trimmed) && trimmed[count] == marker {
		count++
	}
	return marker, count >= 3
}

// stripInlineCode preserves column structure while making code-span syntax invisible to parsers.
func stripInlineCode(lineText string, openTicks *int) string {
	result := []byte(lineText)
	for index := 0; index < len(result); {
		if result[index] != '`' {
			if *openTicks > 0 {
				result[index] = ' '
			}
			index++
			continue
		}
		run := 1
		for index+run < len(result) && result[index+run] == '`' {
			run++
		}
		for offset := 0; offset < run; offset++ {
			result[index+offset] = ' '
		}
		switch *openTicks {
		case 0:
			*openTicks = run
		case run:
			*openTicks = 0
		}
		index += run
	}
	return string(result)
}

// referenceTarget recognizes definitions separately because their destination can be on the next line.
func referenceTarget(lineText string) (string, bool) {
	trimmed := strings.TrimSpace(lineText)
	if !strings.HasPrefix(trimmed, "[") {
		return "", false
	}
	closeIndex := strings.Index(trimmed, "]:")
	if closeIndex < 1 {
		return "", false
	}
	return strings.TrimSpace(trimmed[closeIndex+2:]), true
}

// inlineLinks uses balanced parentheses so common link targets containing parentheses remain intact.
func inlineLinks(lineText string, lineNumber int) []link {
	links := make([]link, 0)
	for offset := 0; offset < len(lineText); {
		start := strings.Index(lineText[offset:], "](")
		if start < 0 {
			break
		}
		start += offset + 2
		if start < len(lineText) && lineText[start] == '<' {
			end := strings.IndexByte(lineText[start+1:], '>')
			if end >= 0 {
				links = append(links, link{lineNumber, lineText[start+1 : start+1+end]})
				offset = start + end + 2
				continue
			}
		}
		depth := 0
		end := start
		for ; end < len(lineText); end++ {
			switch lineText[end] {
			case '(':
				depth++
			case ')':
				if depth == 0 {
					links = append(links, link{lineNumber, normalizeTarget(lineText[start:end])})
					offset = end + 1
					end = len(lineText)
				} else {
					depth--
				}
			}
		}
		if offset <= start {
			break
		}
	}
	return links
}

// normalizeTarget discards optional Markdown titles while retaining angle-wrapped spaces.
func normalizeTarget(target string) string {
	target = strings.TrimSpace(target)
	if strings.HasPrefix(target, "<") {
		if end := strings.IndexByte(target, '>'); end >= 0 {
			return target[1:end]
		}
	}
	if index := strings.IndexAny(target, " \t"); index >= 0 {
		return target[:index]
	}
	return target
}

// isExternal keeps network destinations informational because CI deliberately does not fetch them.
func isExternal(target string) bool {
	lower := strings.ToLower(target)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "mailto:")
}

// checkTarget combines URL cleanup with exact filesystem and anchor validation.
func checkTarget(root, source, target string, anchorCache map[string]map[string]struct{}) string {
	pathAndQuery, rawFragment, _ := strings.Cut(target, "#")
	rawPath, _, _ := strings.Cut(pathAndQuery, "?")
	pathPart, err := url.PathUnescape(rawPath)
	if err != nil {
		return fmt.Sprintf("%s: invalid escaped path: %v", target, err)
	}
	fragment, err := url.PathUnescape(rawFragment)
	if err != nil {
		return fmt.Sprintf("%s: invalid escaped fragment: %v", target, err)
	}

	destination := source
	if pathPart != "" {
		if strings.HasPrefix(pathPart, "/") {
			destination = filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(pathPart, "/")))
		} else {
			destination = filepath.Join(filepath.Dir(source), filepath.FromSlash(pathPart))
		}
		rel, relErr := filepath.Rel(root, destination)
		if relErr != nil {
			return fmt.Sprintf("%s: inspect path: %v", target, relErr)
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Sprintf("%s: path leaves the repository root", pathPart)
		}
		actual, mismatch, exists, caseErr := exactPath(root, destination)
		if caseErr != nil {
			return fmt.Sprintf("%s: inspect path: %v", target, caseErr)
		}
		if mismatch != "" {
			return fmt.Sprintf("%s: case mismatch, file is %s", pathPart, mismatch)
		}
		if !exists {
			return fmt.Sprintf("%s: path does not exist", pathPart)
		}
		destination = actual
	}
	if fragment == "" {
		return ""
	}
	if !strings.HasSuffix(strings.ToLower(destination), ".md") {
		return ""
	}
	anchors, ok := anchorCache[destination]
	if !ok {
		anchors, err = anchorsInFile(destination)
		if err != nil {
			return fmt.Sprintf("%s: read anchors: %v", target, err)
		}
		anchorCache[destination] = anchors
	}
	if _, ok := anchors[fragment]; !ok {
		return fmt.Sprintf("%s: anchor does not exist", target)
	}
	return ""
}

// exactPath compares every component with directory entries to expose case errors on insensitive filesystems.
func exactPath(root, destination string) (string, string, bool, error) {
	rel, err := filepath.Rel(root, filepath.Clean(destination))
	if err != nil {
		return "", "", false, fmt.Errorf("relativize destination: %w", err)
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		if part == ".." {
			if current == root {
				return destination, "", false, nil
			}
			current = filepath.Dir(current)
			continue
		}
		entries, readErr := os.ReadDir(current)
		if readErr != nil {
			if errors.Is(readErr, os.ErrNotExist) {
				return destination, "", false, nil
			}
			return "", "", false, fmt.Errorf("read directory %s: %w", current, readErr)
		}
		matched := ""
		caseMatch := ""
		for _, entry := range entries {
			if entry.Name() == part {
				matched = entry.Name()
				break
			}
			if caseMatch == "" && strings.EqualFold(entry.Name(), part) {
				caseMatch = entry.Name()
			}
		}
		if matched == "" {
			matched = caseMatch
		}
		if matched == "" {
			return destination, "", false, nil
		}
		if matched != part {
			return destination, matched, true, nil
		}
		current = filepath.Join(current, matched)
	}
	return current, "", true, nil
}

// anchorsInFile derives destination anchors once and shares them across all incoming links.
func anchorsInFile(path string) (map[string]struct{}, error) {
	//nolint:gosec // The path was resolved from a Markdown link under the requested tree.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read anchor target: %w", err)
	}
	return anchorSet(data), nil
}

// anchorSet applies GitHub-style duplicate numbering in document order.
func anchorSet(data []byte) map[string]struct{} {
	anchors := make(map[string]struct{})
	counts := make(map[string]int)
	fence := byte(0)
	inlineTicks := 0
	for _, lineText := range splitLines(data) {
		if marker, ok := fenceMarker(lineText); ok {
			switch fence {
			case 0:
				fence = marker
			case marker:
				fence = 0
			}
			continue
		}
		if fence != 0 {
			continue
		}
		clean := stripInlineCode(lineText, &inlineTicks)
		if heading, ok := headingText(clean); ok {
			base := githubSlug(heading)
			slug := base
			if count := counts[base]; count > 0 {
				slug = fmt.Sprintf("%s-%d", base, count)
			}
			counts[base]++
			anchors[slug] = struct{}{}
		}
		for _, anchor := range htmlAnchors(clean) {
			anchors[anchor] = struct{}{}
		}
	}
	return anchors
}

// headingText limits headings to ATX syntax while allowing optional closing hashes.
func headingText(lineText string) (string, bool) {
	trimmed := strings.TrimSpace(lineText)
	count := 0
	for count < len(trimmed) && trimmed[count] == '#' {
		count++
	}
	if count == 0 || count > 6 || count == len(trimmed) || !unicode.IsSpace(rune(trimmed[count])) {
		return "", false
	}
	heading := strings.TrimSpace(trimmed[count:])
	heading = strings.TrimSpace(strings.TrimRight(heading, "#"))
	return heading, true
}

// githubSlug retains letters, numbers, underscores, hyphens, and whitespace-derived hyphens.
func githubSlug(heading string) string {
	var builder strings.Builder
	for _, char := range strings.ToLower(heading) {
		switch {
		case unicode.IsLetter(char), unicode.IsNumber(char), char == '_', char == '-':
			builder.WriteRune(char)
		case unicode.IsSpace(char):
			builder.WriteRune('-')
		}
	}
	return builder.String()
}

// htmlAnchors accepts both name and id attributes without requiring a particular attribute order.
func htmlAnchors(lineText string) []string {
	anchors := make([]string, 0)
	lower := strings.ToLower(lineText)
	for offset := 0; offset < len(lineText); {
		start := strings.Index(lower[offset:], "<a ")
		if start < 0 {
			break
		}
		start += offset
		end := strings.IndexByte(lineText[start:], '>')
		if end < 0 {
			break
		}
		tag := lineText[start : start+end]
		for _, attribute := range []string{"id", "name"} {
			if value := attributeValue(tag, attribute); value != "" {
				anchors = append(anchors, value)
			}
		}
		offset = start + end + 1
	}
	return anchors
}

// attributeValue parses quoted HTML attributes narrowly to avoid treating prose as anchors.
func attributeValue(tag, name string) string {
	lower := strings.ToLower(tag)
	for offset := 0; offset < len(tag); {
		index := strings.Index(lower[offset:], name)
		if index < 0 {
			return ""
		}
		index += offset
		after := index + len(name)
		if index > 0 && !unicode.IsSpace(rune(tag[index-1])) {
			offset = after
			continue
		}
		if after < len(tag) && !unicode.IsSpace(rune(tag[after])) && tag[after] != '=' {
			offset = after
			continue
		}
		for after < len(tag) && unicode.IsSpace(rune(tag[after])) {
			after++
		}
		if after >= len(tag) || tag[after] != '=' {
			offset = after
			continue
		}
		after++
		for after < len(tag) && unicode.IsSpace(rune(tag[after])) {
			after++
		}
		if after >= len(tag) || (tag[after] != '\'' && tag[after] != '"') {
			return ""
		}
		quote := tag[after]
		end := strings.IndexByte(tag[after+1:], quote)
		if end < 0 {
			return ""
		}
		return tag[after+1 : after+1+end]
	}
	return ""
}
