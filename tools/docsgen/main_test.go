package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateAndCheck(t *testing.T) {
	directory := t.TempDir()
	if err := run(directory, false); err != nil {
		t.Fatal(err)
	}
	if err := run(directory, true); err != nil {
		t.Fatalf("fresh output drifted: %v", err)
	}
	path := filepath.Join(directory, "VAR-CFG-0001.md")
	if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(directory, true); err == nil {
		t.Fatal("check accepted stale output")
	}
}
