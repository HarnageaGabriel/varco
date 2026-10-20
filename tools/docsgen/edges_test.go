package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HarnageaGabriel/varco/internal/diag"
)

// stalePageName is a page the diagnostic registry does not produce, so the
// generator has to remove it from the output directory.
const stalePageName = "VAR-STALE-9999.md"

// helperEnv marks the child process that runs main() so the parent can
// observe the exit code of the command.
const (
	helperEnv    = "VARCO_DOCSGEN_HELPER"
	helperOutEnv = "VARCO_DOCSGEN_HELPER_OUT"
)

// Source: tools/docsgen/main.go write; a page that is no longer generated is
// removed from the output directory, so the published docs never keep a page
// the registry no longer describes.
func TestWriteRemovesStalePages(t *testing.T) {
	directory := t.TempDir()
	stale := filepath.Join(directory, stalePageName)
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := write(directory, render(diag.List())); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale page still exists: %v", err)
	}
}

// Source: tools/docsgen/main.go write; a stale page the generator cannot
// remove fails the run instead of leaving a page the registry no longer
// describes.
func TestWriteReportsStalePageItCannotRemove(t *testing.T) {
	directory := t.TempDir()
	stale := filepath.Join(directory, stalePageName)
	if err := os.MkdirAll(filepath.Join(stale, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	err := write(directory, render(diag.List()))
	if err == nil {
		t.Fatal("write() = nil error, want the stale page error")
	}
	if !strings.Contains(err.Error(), "remove stale page") {
		t.Fatalf("write() error = %v, want the stale page error", err)
	}
}

// Source: tools/docsgen/main.go write; an output directory the generator
// cannot create fails the run.
func TestWriteReportsUnusableOutputDirectory(t *testing.T) {
	directory := t.TempDir()
	blocker := filepath.Join(directory, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := write(filepath.Join(blocker, "docs"), render(diag.List()))
	if err == nil {
		t.Fatal("write() = nil error, want the create output directory error")
	}
	if !strings.Contains(err.Error(), "create output directory") {
		t.Fatalf("write() error = %v, want the create output directory error", err)
	}
}

// Source: tools/docsgen/main.go write; a page the generator cannot write
// fails the run.
func TestWriteReportsUnwritablePage(t *testing.T) {
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, "README.md"), 0o700); err != nil {
		t.Fatal(err)
	}
	err := write(directory, render(diag.List()))
	if err == nil {
		t.Fatal("write() = nil error, want the write error")
	}
	if !strings.Contains(err.Error(), "write README.md") {
		t.Fatalf("write() error = %v, want the write error", err)
	}
}

// Source: tools/docsgen/main.go write; an output directory whose name makes
// the page pattern invalid fails the run.
func TestWriteReportsUnusablePagePattern(t *testing.T) {
	directory := t.TempDir()
	err := write(filepath.Join(directory, "["), render(diag.List()))
	if err == nil {
		t.Fatal("write() = nil error, want the list generated docs error")
	}
	if !strings.Contains(err.Error(), "list generated docs") {
		t.Fatalf("write() error = %v, want the list generated docs error", err)
	}
}

// Source: tools/docsgen/main.go verify; a generated page that is missing is
// drift, so the check fails and names the page.
func TestVerifyReportsMissingPages(t *testing.T) {
	directory := t.TempDir()
	err := verify(directory, render(diag.List()))
	if err == nil {
		t.Fatal("verify() = nil error, want the drift error")
	}
	if !strings.Contains(err.Error(), "out of date") || !strings.Contains(err.Error(), "README.md") {
		t.Fatalf("verify() error = %v, want the drift error", err)
	}
}

// Source: tools/docsgen/main.go verify; a page in the output directory that
// the registry no longer produces is drift too, so the check fails and names
// the stale page.
func TestVerifyReportsStalePages(t *testing.T) {
	directory := t.TempDir()
	if err := write(directory, render(diag.List())); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(directory, stalePageName)
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := verify(directory, render(diag.List()))
	if err == nil {
		t.Fatal("verify() = nil error, want the drift error")
	}
	if !strings.Contains(err.Error(), stalePageName+" (stale)") {
		t.Fatalf("verify() error = %v, want the stale page drift", err)
	}
}

// Source: tools/docsgen/main.go verify; an output directory whose name makes
// the page pattern invalid fails the check.
func TestVerifyReportsUnusablePagePattern(t *testing.T) {
	directory := t.TempDir()
	err := verify(filepath.Join(directory, "["), render(diag.List()))
	if err == nil {
		t.Fatal("verify() = nil error, want the list generated docs error")
	}
	if !strings.Contains(err.Error(), "list generated docs") {
		t.Fatalf("verify() error = %v, want the list generated docs error", err)
	}
}

// Source: tools/docsgen/main.go main; the command writes the generated pages
// to the directory named by -out.
//
// main registers its flags on the global flag set, so it can run only once
// per process. The failure path of main runs in a child process instead.
func TestMainWritesGeneratedPages(t *testing.T) {
	directory := t.TempDir()
	original := os.Args
	os.Args = []string{"docsgen", "-out", directory}
	defer func() { os.Args = original }()
	main()
	pages, err := filepath.Glob(filepath.Join(directory, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) == 0 {
		t.Fatal("docsgen wrote no pages")
	}
	index, err := os.ReadFile(filepath.Join(directory, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), string(diag.CodeInvalidFlag)) {
		t.Fatalf("index = %q, want the diagnostic codes", index)
	}
}

// Source: tools/docsgen/main.go main; a run that cannot write its pages
// reports the failure on stderr and exits with code 1. The child process
// runs main so the exit code can be observed.
func TestMainReportsFailure(t *testing.T) {
	if os.Getenv(helperEnv) == "" {
		t.Skip("helper process; the parent test drives it")
	}
	os.Args = []string{"docsgen", "-out", os.Getenv(helperOutEnv)}
	main()
}

// Source: tools/docsgen/main.go main; a run that cannot write its pages
// reports the failure on stderr and exits with code 1.
func TestMainExitsWithFailure(t *testing.T) {
	directory := t.TempDir()
	blocker := filepath.Join(directory, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=TestMainReportsFailure")
	command.Env = append(os.Environ(), helperEnv+"=1", helperOutEnv+"="+filepath.Join(blocker, "docs"))
	output, err := command.CombinedOutput()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		t.Fatalf("docsgen error = %v, want a failing exit; output = %q", err, output)
	}
	if exitError.ExitCode() != 1 {
		t.Fatalf("exit code = %d, want 1; output = %q", exitError.ExitCode(), output)
	}
	if !strings.Contains(string(output), "docsgen: create output directory") {
		t.Fatalf("output = %q, want the docsgen error", output)
	}
}
