package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
)

// ADR index coverage follows the documentation and test conventions in CONVENTIONS.md.
func TestADRIndexCoversEveryADR(t *testing.T) {
	adrDir := filepath.Join("..", "..", "docs", "adr")
	indexPath := filepath.Join(adrDir, "README.md")
	index, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}

	entries, err := filepath.Glob(filepath.Join(adrDir, "[0-9][0-9][0-9][0-9]-*.md"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(entries)
	linkPattern := regexp.MustCompile(`\[[^]]+\]\(([^)]+)\)`)
	links := make(map[string]bool)
	for _, match := range linkPattern.FindAllStringSubmatch(string(index), -1) {
		links[match[1]] = true
		if _, err := os.Stat(filepath.Join(adrDir, filepath.FromSlash(match[1]))); err != nil {
			t.Errorf("index link %q does not resolve: %v", match[1], err)
		}
	}
	for _, entry := range entries {
		name := filepath.Base(entry)
		if !links[name] {
			t.Errorf("ADR %s is missing from %s", name, indexPath)
		}
	}
	for link := range links {
		if filepath.IsAbs(filepath.FromSlash(link)) {
			t.Errorf("unexpected absolute ADR link %q", link)
		}
	}
}
