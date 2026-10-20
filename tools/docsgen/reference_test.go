package main

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// update rewrites the golden files of this package.
var update = flag.Bool("update", false, "update golden files")

// referenceDirectory is the directory the generated command reference pages
// are published in.
var referenceDirectory = filepath.Join("..", "..", "docs", "reference")

// helpOf returns the help output of a command of the real CLI.
func helpOf(t *testing.T, args ...string) string {
	t.Helper()
	help, err := captureHelp(args)
	if err != nil {
		t.Fatal(err)
	}
	return help
}

// descriptionOf returns the description the top-level help gives a command.
func descriptionOf(t *testing.T, name string) string {
	t.Helper()
	summaries, err := parseCommandSummaries(helpOf(t, helpCommand))
	if err != nil {
		t.Fatal(err)
	}
	for _, summary := range summaries {
		if summary.Name == name {
			return summary.Description
		}
	}
	t.Fatalf("the top-level help has no command %q", name)
	return ""
}

// Source: tools/docsgen/reference.go renderReferencePage; the page of a
// command is generated from the help of the real CLI, so the golden keeps the
// published page in step with the flags the command really has.
func TestReferencePageGolden(t *testing.T) {
	page := renderReferencePage("diff", descriptionOf(t, "diff"), helpOf(t, "diff", "-h"))
	path := filepath.Join("testdata", "reference-diff.golden")
	if *update {
		if err := os.WriteFile(path, []byte(page), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run `go test ./tools/docsgen -update` to create it)", err)
	}
	if !bytes.Equal(normalize(want), []byte(page)) {
		t.Fatalf("golden mismatch for the diff page; run `go test ./tools/docsgen -update` and review:\n%s", page)
	}
}

// Source: tools/docsgen/reference.go renderReference; the same input renders
// the same pages, so the published reference never changes between runs
// (CONVENTIONS.md, Determinism).
func TestReferenceIsDeterministic(t *testing.T) {
	first, err := renderReference()
	if err != nil {
		t.Fatal(err)
	}
	second, err := renderReference()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) {
		t.Fatalf("the first run rendered %d pages and the second %d", len(first), len(second))
	}
	for name, page := range first {
		if !bytes.Equal(page, second[name]) {
			t.Fatalf("the page %s differs between two runs", name)
		}
		if bytes.Contains(page, []byte("\r")) {
			t.Fatalf("the page %s has a carriage return", name)
		}
	}
}

// Source: tools/docsgen/reference.go renderReference; every command the CLI
// accepts has a page, and the index links to it.
func TestReferenceRendersEveryCommand(t *testing.T) {
	pages, err := renderReference()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range referenceCommands {
		page, ok := pages[name+".md"]
		if !ok {
			t.Fatalf("the reference has no page for the command %q", name)
		}
		if !bytes.Contains(page, []byte("| Flag | Type | Default | Description |")) {
			t.Fatalf("the page of %q has no flag table", name)
		}
		if !bytes.Contains(page, []byte("(../exit-codes.md)")) {
			t.Fatalf("the page of %q does not link to the exit codes", name)
		}
	}
	index, ok := pages["README.md"]
	if !ok {
		t.Fatal("the reference has no index")
	}
	for _, name := range referenceCommands {
		if !bytes.Contains(index, []byte("["+name+"]("+name+".md)")) {
			t.Fatalf("the index does not link to the page of %q", name)
		}
	}
	for _, guide := range []string{"../usage.md", "../run.md", "../plan.md", "../exit-codes.md"} {
		if !bytes.Contains(index, []byte("]("+guide+")")) {
			t.Fatalf("the index does not link to %s", guide)
		}
	}
}

// Source: tools/docsgen/main.go verify; a reference page that no longer
// matches the CLI is drift, so the check fails and names the page.
func TestReferenceCheckDetectsAStalePage(t *testing.T) {
	diagnostics := t.TempDir()
	reference := t.TempDir()
	if err := run(diagnostics, reference, false); err != nil {
		t.Fatal(err)
	}
	if err := run(diagnostics, reference, true); err != nil {
		t.Fatalf("fresh output drifted: %v", err)
	}
	path := filepath.Join(reference, "diff.md")
	if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := run(diagnostics, reference, true)
	if err == nil {
		t.Fatal("the check accepted a stale reference page")
	}
	if !strings.Contains(err.Error(), "diff.md") {
		t.Fatalf("the check error = %v, want the stale page", err)
	}
}

// Source: tools/docsgen/main.go verify; a page in the reference directory the
// CLI no longer produces is drift too, so the check fails and names it.
func TestReferenceCheckDetectsAPageTheCLIDoesNotProduce(t *testing.T) {
	diagnostics := t.TempDir()
	reference := t.TempDir()
	if err := run(diagnostics, reference, false); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(reference, "gone.md")
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := run(diagnostics, reference, true)
	if err == nil {
		t.Fatal("the check accepted a page the CLI does not produce")
	}
	if !strings.Contains(err.Error(), "gone.md (stale)") {
		t.Fatalf("the check error = %v, want the stale page", err)
	}
}

// Source: tools/docsgen/reference.go renderReference; the pages published
// under docs/reference must be the pages the CLI produces, so the reference
// never drifts from the flags.
func TestCheckedInReferenceIsCurrent(t *testing.T) {
	pages, err := renderReference()
	if err != nil {
		t.Fatal(err)
	}
	for name, page := range pages {
		content, err := os.ReadFile(filepath.Join(referenceDirectory, name))
		if err != nil {
			t.Fatalf("%v (run `make docs` to write it)", err)
		}
		if !bytes.Equal(normalize(content), page) {
			t.Fatalf("the published page %s is out of date; run `make docs`", name)
		}
	}
	existing, err := filepath.Glob(filepath.Join(referenceDirectory, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range existing {
		name := filepath.Base(path)
		if _, ok := pages[name]; !ok {
			t.Fatalf("the published page %s is not a page the CLI produces; run `make docs`", name)
		}
	}
}

// Source: tools/docsgen/reference.go parseFlagDocs; a help the flag package
// did not print is not read as a flag table.
func TestParseFlagDocsRejectsAnUnreadableHelp(t *testing.T) {
	if _, ok := parseFlagDocs("not the help of the flag package\n"); ok {
		t.Fatal("parseFlagDocs read a help that is not one")
	}
}

// Source: tools/docsgen/reference.go parseFlagDocs; a line the flag package
// did not print makes the whole help unreadable, so the page falls back to
// the raw help.
func TestParseFlagDocsRejectsAnUnexpectedLine(t *testing.T) {
	help := "Usage of diff:\n  -host string\n    \tHTTP Host header\nsomething else\n"
	if _, ok := parseFlagDocs(help); ok {
		t.Fatal("parseFlagDocs read a help with an unexpected line")
	}
}

// Source: tools/docsgen/reference.go parseFlagDocs; the flag package writes
// the description of a flag with a one-character name on the same line, and
// the table reads it there.
func TestParseFlagDocsReadsAnInlineDescription(t *testing.T) {
	help := "Usage of diff:\n  -v\tprint the version\n  -host string\n    \tHTTP Host header\n"
	docs, ok := parseFlagDocs(help)
	if !ok {
		t.Fatal("parseFlagDocs did not read the help")
	}
	if len(docs) != 2 {
		t.Fatalf("parseFlagDocs read %d flags, want 2", len(docs))
	}
	if docs[0].Name != "host" || docs[1].Name != "v" {
		t.Fatalf("the flags are %q and %q, want them sorted by name", docs[0].Name, docs[1].Name)
	}
	if docs[1].Type != "bool" || docs[1].Description != "print the version" {
		t.Fatalf("the inline flag = %+v, want a boolean flag with its description", docs[1])
	}
}

// Source: tools/docsgen/reference.go renderReferencePage; a help that cannot
// be read as a flag table is published as the command's own output, so the
// page never states a flag the help does not show.
func TestReferencePageFallsBackToTheRawHelp(t *testing.T) {
	page := renderReferencePage("diff", "compare two endpoints", "not the help of the flag package\n")
	if !strings.Contains(page, "could not be read as a flag table") {
		t.Fatalf("the page does not say the help was not read:\n%s", page)
	}
	if !strings.Contains(page, "not the help of the flag package") {
		t.Fatalf("the page does not carry the raw help:\n%s", page)
	}
}

// Source: tools/docsgen/reference.go splitDefault; the default the flag
// package prints is split from the description.
func TestSplitDefaultReadsAPrintedDefault(t *testing.T) {
	text, value, printed := splitDefault("string", `table or json (default "table")`)
	if !printed {
		t.Fatal("splitDefault did not read the printed default")
	}
	if text != "table or json" {
		t.Fatalf("the description = %q, want the text without the default", text)
	}
	if value != `"table"` {
		t.Fatalf("the default = %q, want the quoted default", value)
	}
}

// Source: tools/docsgen/reference.go splitDefault; a description that ends
// with a parenthesized text of its own is not a default, so it stays in the
// description.
func TestSplitDefaultKeepsAParenthesisOfTheDescription(t *testing.T) {
	description := "read Ingresses from this namespace (default all namespaces)"
	text, value, printed := splitDefault("string", description)
	if printed {
		t.Fatal("splitDefault read a description as a default")
	}
	if text != description {
		t.Fatalf("the description = %q, want it unchanged", text)
	}
	if value != "" {
		t.Fatalf("the default = %q, want no default", value)
	}
}

// Source: tools/docsgen/reference.go splitDefault; the flag package leaves
// out a default that equals the zero value of the type, so a default equal
// to the zero value is not one the help printed.
func TestSplitDefaultIgnoresTheZeroValue(t *testing.T) {
	if _, _, printed := splitDefault("int", "how many requests (default 0)"); printed {
		t.Fatal("splitDefault read the zero value as a printed default")
	}
}

// Source: tools/docsgen/reference.go zeroDefault; the default the help leaves
// out is the zero value of the flag type.
func TestZeroDefault(t *testing.T) {
	cases := map[string]string{
		"":         "false",
		"string":   `""`,
		"int":      "0",
		"uint":     "0",
		"float":    "0",
		"duration": "0s",
		"value":    `""`,
	}
	for kind, want := range cases {
		if got := zeroDefault(kind); got != want {
			t.Fatalf("zeroDefault(%q) = %q, want %q", kind, got, want)
		}
	}
}

// Source: tools/docsgen/reference.go captureHelp; a command the CLI refuses
// has no help, so the generator fails instead of publishing an empty page.
func TestCaptureHelpReportsAFailingCommand(t *testing.T) {
	if _, err := captureHelp([]string{"nosuchcommand"}); err == nil {
		t.Fatal("captureHelp accepted a command the CLI does not have")
	}
}

// Source: tools/docsgen/reference.go renderReferenceWith; a subcommand the
// reference does not document fails the generator, so a new command cannot
// go undocumented.
func TestRenderReferenceRejectsACommandWithoutAPage(t *testing.T) {
	capture := func(args []string) (string, error) {
		if args[0] == helpCommand {
			return "Usage: varco <command> [options]\n\nCommands:\n  diff       compare two endpoints\n  plan       derive probe cases from Ingress manifests\n  run        execute a probe plan against two endpoints\n  version    print build version\n  news       a new command\n", nil
		}
		return "Usage of " + args[0] + ":\n", nil
	}
	_, err := renderReferenceWith(capture)
	if err == nil {
		t.Fatal("the generator accepted a command without a page")
	}
	if !strings.Contains(err.Error(), "news") {
		t.Fatalf("the generator error = %v, want the undocumented command", err)
	}
}

// Source: tools/docsgen/reference.go renderReferenceWith; a documented
// command the top-level help does not list fails the generator, so the
// reference cannot describe a command the CLI does not have.
func TestRenderReferenceRejectsAMissingCommand(t *testing.T) {
	capture := func(args []string) (string, error) {
		if args[0] == helpCommand {
			return "Usage: varco <command> [options]\n\nCommands:\n  diff       compare two endpoints\n", nil
		}
		return "Usage of " + args[0] + ":\n", nil
	}
	_, err := renderReferenceWith(capture)
	if err == nil {
		t.Fatal("the generator accepted a missing command")
	}
	if !strings.Contains(err.Error(), "plan") {
		t.Fatalf("the generator error = %v, want the missing command", err)
	}
}

// Source: tools/docsgen/reference.go renderReferenceWith; a help the
// generator cannot read fails the generator instead of publishing an empty
// reference.
func TestRenderReferenceReportsAnUnreadableHelp(t *testing.T) {
	capture := func([]string) (string, error) {
		return "", errUnreadable
	}
	if _, err := renderReferenceWith(capture); err == nil {
		t.Fatal("the generator accepted an unreadable help")
	}
}

// errUnreadable is the error of a help the generator cannot read.
var errUnreadable = errors.New("the help cannot be read")

// Source: tools/docsgen/reference.go parseCommandSummaries; a top-level help
// without a command list fails the generator.
func TestParseCommandSummariesRejectsAHelpWithoutCommands(t *testing.T) {
	if _, err := parseCommandSummaries("Usage: varco <command> [options]\n"); err == nil {
		t.Fatal("parseCommandSummaries accepted a help without a command list")
	}
}

// Source: tools/docsgen/reference.go parseCommandSummaries; a command line
// the generator cannot read fails the generator, so no command is dropped
// from the reference.
func TestParseCommandSummariesRejectsAnUnreadableCommandLine(t *testing.T) {
	usage := "Usage: varco <command> [options]\n\nCommands:\n  diff\n"
	_, err := parseCommandSummaries(usage)
	if err == nil {
		t.Fatal("parseCommandSummaries accepted an unreadable command line")
	}
	if !strings.Contains(err.Error(), "diff") {
		t.Fatalf("the parse error = %v, want the unreadable line", err)
	}
}

// Source: tools/docsgen/reference.go escapeCell; a pipe in a description
// cannot break the flag table.
func TestEscapeCell(t *testing.T) {
	if got := escapeCell("a | b"); got != `a \| b` {
		t.Fatalf("escapeCell = %q, want the escaped pipe", got)
	}
}

// Source: tools/docsgen/reference.go validDefault; a default that does not fit
// the type of its flag is not a default the help printed.
func TestValidDefault(t *testing.T) {
	cases := []struct {
		kind  string
		value string
		want  bool
	}{
		{"", "true", true},
		{"", "false", true},
		{"", "yes", false},
		{"string", `"table"`, true},
		{"string", "table", false},
		{"int", "4", true},
		{"int", "four", false},
		{"uint", "4", true},
		{"float", "5", true},
		{"float", "five", false},
		{"duration", "10s", true},
		{"duration", "ten", false},
		{"value", "x", true},
		{"value", "x (y)", false},
	}
	for _, item := range cases {
		if got := validDefault(item.kind, item.value); got != item.want {
			t.Fatalf("validDefault(%q, %q) = %v, want %v", item.kind, item.value, got, item.want)
		}
	}
}

// Source: tools/docsgen/reference.go displayDefault; a string default is shown
// without the quotes the help adds, and an empty default stays visible.
func TestDisplayDefault(t *testing.T) {
	cases := []struct {
		kind  string
		value string
		want  string
	}{
		{"string", `"table"`, "table"},
		{"string", `""`, `""`},
		{"int", "4", "4"},
		{"value", "", `""`},
	}
	for _, item := range cases {
		if got := displayDefault(item.kind, item.value); got != item.want {
			t.Fatalf("displayDefault(%q, %q) = %q, want %q", item.kind, item.value, got, item.want)
		}
	}
}

// Source: tools/docsgen/reference.go sentence; the description of a command
// becomes a sentence.
func TestSentence(t *testing.T) {
	cases := map[string]string{
		"compare two endpoints": "Compare two endpoints.",
		"print build version.":  "Print build version.",
		"":                      "",
	}
	for description, want := range cases {
		if got := sentence(description); got != want {
			t.Fatalf("sentence(%q) = %q, want %q", description, got, want)
		}
	}
}
