package main

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/HarnageaGabriel/varco/internal/cli"
)

// referenceCommands are the varco subcommands that get a reference page.
//
// Source: internal/cli/cli.go Run; the switch there lists the subcommands the
// CLI accepts. The list is checked against the top-level help, so a new
// subcommand cannot go undocumented: the generator fails until the command is
// listed here.
var referenceCommands = []string{"diff", "plan", "run", "version"}

// helpCommand prints the top-level usage. It is the index of the reference,
// so it has no page of its own.
//
// Source: internal/cli/cli.go Run, case "help".
const helpCommand = "help"

// fence opens and closes a fenced code block.
const fence = "```"

// exitCodesLink points at the hand-written page that documents the process
// exit codes. Source: docs/exit-codes.md.
const exitCodesLink = "../exit-codes.md"

var (
	// usageHeader matches the header the flag package prints for a command.
	// Source: flag.FlagSet defaultUsage.
	usageHeader = regexp.MustCompile(`^Usage of (\S+):$`)

	// flagStart matches the first line of a flag entry. The type name that
	// follows the flag name may contain spaces: the flag package uses the
	// word a usage string quotes in backquotes as the type name.
	// Source: flag.FlagSet PrintDefaults and flag.UnquoteUsage.
	flagStart = regexp.MustCompile(`^  -(\S+)(?: (.*))?$`)

	// flagInline matches a flag whose description follows the flag name on
	// the same line. The flag package writes it that way for a flag with a
	// one-character name, which is always a boolean flag.
	// Source: flag.FlagSet PrintDefaults.
	flagInline = regexp.MustCompile(`^  -(\S+)\t(.*)$`)

	// flagBody matches a description line of a flag entry.
	// Source: flag.FlagSet PrintDefaults, which indents a description with
	// four spaces and a tab.
	flagBody = regexp.MustCompile(`^    \t(.*)$`)

	// defaultSuffix matches the default the flag package appends to a
	// description.
	// Source: flag.FlagSet PrintDefaults, which appends " (default %q)" for
	// a string flag and " (default %v)" for every other flag.
	defaultSuffix = regexp.MustCompile(`^(.*) \(default (.*)\)$`)

	// commandEntry matches one command line of the top-level help.
	// Source: internal/cli/cli.go usage.
	commandEntry = regexp.MustCompile(`^  (\S+)\s+(.*)$`)
)

// commandSummary is one command of the top-level help.
type commandSummary struct {
	Name        string
	Description string
}

// flagDoc is one row of a generated flag table.
type flagDoc struct {
	Name        string
	Type        string
	Default     string
	Description string
	// Inferred marks a default the help output left out. The flag package
	// omits a default that equals the zero value of the flag type, so the
	// zero value is the default.
	Inferred bool
}

// helpCapture reads the help output of a command. The generator uses the
// real CLI; a test can supply another source.
type helpCapture func(args []string) (string, error)

// renderReference renders the command reference pages from the help of the
// real CLI. The pages are generated from the same code that serves the
// commands, so the published flags cannot drift from the implemented ones
// (CONVENTIONS.md, Determinism).
func renderReference() (map[string][]byte, error) {
	return renderReferenceWith(captureHelp)
}

// renderReferenceWith renders the command reference pages from the help a
// capture returns.
func renderReferenceWith(capture helpCapture) (map[string][]byte, error) {
	usage, err := capture([]string{helpCommand})
	if err != nil {
		return nil, err
	}
	summaries, err := parseCommandSummaries(usage)
	if err != nil {
		return nil, err
	}
	descriptions := map[string]string{}
	for _, summary := range summaries {
		descriptions[summary.Name] = summary.Description
	}
	for _, name := range referenceCommands {
		if _, ok := descriptions[name]; !ok {
			return nil, fmt.Errorf("the command %q has no line in the top-level help", name)
		}
	}
	for _, summary := range summaries {
		if summary.Name == helpCommand {
			continue
		}
		if !slices.Contains(referenceCommands, summary.Name) {
			return nil, fmt.Errorf("the command %q has no reference page; add it to referenceCommands", summary.Name)
		}
	}
	pages := map[string][]byte{}
	for _, name := range referenceCommands {
		help, err := capture([]string{name, "-h"})
		if err != nil {
			return nil, err
		}
		pages[name+".md"] = []byte(renderReferencePage(name, descriptions[name], help))
	}
	pages["README.md"] = []byte(renderReferenceIndex(usage, summaries))
	return pages, nil
}

// captureHelp runs the real CLI entry point and returns the help output it
// wrote.
//
// Source: cmd/varco/main.go main; the binary calls cli.Run, so the generator
// calls the same function in process instead of running the binary. The
// command help goes to stderr (the flag package writes there) and the
// top-level usage goes to stdout.
func captureHelp(args []string) (string, error) {
	var stdout, stderr bytes.Buffer
	code := cli.Run(args, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		return "", fmt.Errorf("%s: the help exited with code %d: %s", strings.Join(args, " "), code, strings.TrimSpace(stderr.String()))
	}
	if stderr.Len() != 0 {
		return stderr.String(), nil
	}
	return stdout.String(), nil
}

// parseCommandSummaries reads the command list of the top-level help.
//
// Source: internal/cli/cli.go usage; the commands are listed under a
// "Commands:" header, one per line, as the command name and its description.
func parseCommandSummaries(usage string) ([]commandSummary, error) {
	lines := strings.Split(usage, "\n")
	summaries := make([]commandSummary, 0, len(lines))
	inCommands := false
	for _, line := range lines {
		if !inCommands {
			if strings.TrimSpace(line) == "Commands:" {
				inCommands = true
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			break
		}
		match := commandEntry.FindStringSubmatch(line)
		if match == nil {
			return nil, fmt.Errorf("cannot read the command line %q of the top-level help", line)
		}
		summaries = append(summaries, commandSummary{Name: match[1], Description: match[2]})
	}
	if len(summaries) == 0 {
		return nil, fmt.Errorf("the top-level help lists no command")
	}
	return summaries, nil
}

// flagEntry is a flag while its description is being read.
type flagEntry struct {
	name string
	kind string
	body []string
}

// parseFlagDocs reads the flag entries of a command help.
//
// It reports false when the help does not have the shape the flag package
// prints, or when a default does not look like a value of its type. The page
// then falls back to the raw help text, so an unreadable help is published
// verbatim instead of half-read.
func parseFlagDocs(help string) ([]flagDoc, bool) {
	lines := strings.Split(help, "\n")
	if len(lines) == 0 || !usageHeader.MatchString(lines[0]) {
		return nil, false
	}
	entries := make([]flagEntry, 0, len(lines))
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if match := flagStart.FindStringSubmatch(line); match != nil {
			entries = append(entries, flagEntry{name: match[1], kind: strings.TrimSpace(match[2])})
			continue
		}
		if match := flagInline.FindStringSubmatch(line); match != nil {
			entries = append(entries, flagEntry{name: match[1], body: []string{match[2]}})
			continue
		}
		match := flagBody.FindStringSubmatch(line)
		if match == nil || len(entries) == 0 {
			return nil, false
		}
		last := len(entries) - 1
		entries[last].body = append(entries[last].body, match[1])
	}
	docs := make([]flagDoc, 0, len(entries))
	for _, entry := range entries {
		description := strings.Join(entry.body, " ")
		text, value, printed := splitDefault(entry.kind, description)
		if !printed {
			value = zeroDefault(entry.kind)
		}
		docs = append(docs, flagDoc{
			Name:        entry.name,
			Type:        flagType(entry.kind),
			Default:     displayDefault(entry.kind, value),
			Description: text,
			Inferred:    !printed,
		})
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Name < docs[j].Name })
	return docs, true
}

// splitDefault splits the default the flag package appends to a flag
// description from the description itself.
//
// The flag package appends " (default %q)" to a string flag and
// " (default %v)" to every other flag, but only when the default differs
// from the zero value of the type. A description that ends with a
// parenthesized value of its own, such as "read Ingresses from this
// namespace (default all namespaces)", is therefore read as a default only
// when the value fits the type and is not the zero value the help leaves
// out. The flag package's output cannot tell the two apart any better, so a
// description that ends with a value of its type is read as a default.
func splitDefault(kind, description string) (string, string, bool) {
	match := defaultSuffix.FindStringSubmatch(description)
	if match == nil {
		return description, "", false
	}
	value := match[2]
	if !validDefault(kind, value) || value == zeroDefault(kind) {
		return description, "", false
	}
	return match[1], value, true
}

// zeroDefault is the default the flag package leaves out of the help: the
// zero value of the flag type.
//
// Source: flag.FlagSet PrintDefaults, which prints a default only when it
// differs from the zero value of the type.
func zeroDefault(kind string) string {
	switch kind {
	case "":
		return "false"
	case "int", "uint":
		return "0"
	case "float":
		return "0"
	case "duration":
		return "0s"
	default:
		return `""`
	}
}

// validDefault reports whether a default looks like a value of the flag
// type. A default that does not fit means the description ended with
// something that only looks like a default, so the description is kept whole.
func validDefault(kind, value string) bool {
	switch kind {
	case "":
		return value == "true" || value == "false"
	case "string":
		return len(value) >= 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`)
	case "int", "uint":
		_, err := strconv.Atoi(value)
		return err == nil
	case "float":
		_, err := strconv.ParseFloat(value, 64)
		return err == nil
	case "duration":
		_, err := time.ParseDuration(value)
		return err == nil
	default:
		return value != "" && !strings.ContainsAny(value, "()")
	}
}

// flagType is the type a flag declares, as the help names it. A boolean flag
// has no type name in the help output.
//
// Source: flag.UnquoteUsage, which names no type for a boolean flag.
func flagType(kind string) string {
	if kind == "" {
		return "bool"
	}
	return kind
}

// displayDefault is the default as the table shows it. A string default is
// shown without the quotes the help adds.
func displayDefault(kind, value string) string {
	if kind == "string" {
		unquoted := strings.TrimSuffix(strings.TrimPrefix(value, `"`), `"`)
		if unquoted == "" {
			return `""`
		}
		return unquoted
	}
	if value == "" {
		return `""`
	}
	return value
}

// renderReferencePage renders the page of one command.
func renderReferencePage(name, description, help string) string {
	var page strings.Builder
	page.WriteString(generatedBanner + "\n\n")
	fmt.Fprintf(&page, "# varco %s\n\n", name)
	fmt.Fprintf(&page, "%s\n\n", sentence(description))
	page.WriteString("## Usage\n\n")
	page.WriteString(fence + "text\n")
	fmt.Fprintf(&page, "varco %s [options]\n", name)
	page.WriteString(fence + "\n\n")
	page.WriteString("## Flags\n\n")
	docs, ok := parseFlagDocs(help)
	if !ok {
		page.WriteString("The help output below is the command's own; it could not be read as a flag table.\n\n")
		page.WriteString(fence + "text\n")
		page.WriteString(strings.TrimRight(help, "\n") + "\n")
		page.WriteString(fence + "\n\n")
	} else {
		page.WriteString("| Flag | Type | Default | Description |\n")
		page.WriteString("|---|---|---|---|\n")
		inferred := false
		for _, doc := range docs {
			fmt.Fprintf(&page, "| `-%s` | `%s` | `%s` | %s |\n", doc.Name, doc.Type, doc.Default, escapeCell(doc.Description))
			inferred = inferred || doc.Inferred
		}
		if inferred {
			page.WriteString("\nThe help output leaves out a default that equals the zero value of the flag type; the table above shows that zero value.\n")
		}
		page.WriteString("\n")
	}
	page.WriteString("## Exit codes\n\n")
	fmt.Fprintf(&page, "See [exit codes](%s) for the process exit codes varco returns.\n\n", exitCodesLink)
	page.WriteString("## See also\n\n")
	for _, other := range referenceCommands {
		if other == name {
			continue
		}
		fmt.Fprintf(&page, "- [varco %s](%s.md)\n", other, other)
	}
	return page.String()
}

// renderReferenceIndex renders the index of the reference.
func renderReferenceIndex(usage string, summaries []commandSummary) string {
	var page strings.Builder
	page.WriteString(generatedBanner + "\n\n")
	page.WriteString("# Command reference\n\n")
	page.WriteString("Every page here is generated from the help of the real `varco` command, so the flags cannot drift from the code. Run `make docs` after a flag changes.\n\n")
	page.WriteString("## Commands\n\n")
	page.WriteString("| Command | Description | Reference |\n")
	page.WriteString("|---|---|---|\n")
	documented := make([]commandSummary, 0, len(summaries))
	for _, summary := range summaries {
		if slices.Contains(referenceCommands, summary.Name) {
			documented = append(documented, summary)
		}
	}
	sort.Slice(documented, func(i, j int) bool { return documented[i].Name < documented[j].Name })
	for _, summary := range documented {
		fmt.Fprintf(&page, "| `varco %s` | %s | [%s](%s.md) |\n", summary.Name, escapeCell(summary.Description), summary.Name, summary.Name)
	}
	page.WriteString("\n## Top-level help\n\n")
	page.WriteString(fence + "text\n")
	page.WriteString(strings.TrimRight(usage, "\n") + "\n")
	page.WriteString(fence + "\n\n")
	page.WriteString("## Hand-written guides\n\n")
	page.WriteString("- [Usage](../usage.md)\n")
	page.WriteString("- [Running a plan](../run.md)\n")
	page.WriteString("- [Planning](../plan.md)\n")
	fmt.Fprintf(&page, "- [Exit codes](%s)\n", exitCodesLink)
	return page.String()
}

// sentence turns a description of the top-level help into a sentence.
func sentence(description string) string {
	text := strings.TrimSpace(description)
	if text == "" {
		return ""
	}
	text = strings.ToUpper(text[:1]) + text[1:]
	if !strings.HasSuffix(text, ".") {
		text += "."
	}
	return text
}

// escapeCell escapes the characters that break a Markdown table cell.
func escapeCell(text string) string {
	return strings.ReplaceAll(text, "|", `\|`)
}
