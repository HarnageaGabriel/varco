package cli

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/ingress"
	"github.com/HarnageaGabriel/varco/internal/plan"
	"github.com/HarnageaGabriel/varco/internal/report"
)

// stdinInput is the -f value that reads a manifest stream from standard input.
const stdinInput = "-"

func runPlan(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("plan", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var inputs stringsFlag
	flags.Var(&inputs, "f", "Ingress manifest file, directory, or - for stdin (repeatable)")
	format := flags.String("format", "table", "table or json")
	ingressClass := flags.String("ingress-class", "", "plan only Ingress resources of this class")
	defaultHost := flags.String("default-host", "", "host for rules that declare none")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		return configFailure(stderr, "plan takes no positional arguments; pass manifests with -f")
	}
	if len(inputs) == 0 {
		return configFailure(stderr, "plan requires at least one -f <file|directory|->")
	}
	if *format != "table" && *format != "json" {
		return configFailure(stderr, "--format must be table or json")
	}
	paths, code := expandInputs(inputs, stderr)
	if code != 0 {
		return code
	}
	resources, code := parseInputs(paths, stdin, stderr)
	if code != 0 {
		return code
	}
	result := plan.Build(resources, plan.Options{IngressClass: *ingressClass, DefaultHost: *defaultHost})
	var err error
	if *format == "json" {
		err = report.PlanJSON(stdout, result)
	} else {
		err = report.PlanTable(stdout, result)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "report:", err)
		return 4
	}
	return 0
}

// expandInputs turns -f values into a sorted, de-duplicated list of manifest
// files. Directories are walked recursively and only *.yaml, *.yml and *.json
// regular files are collected, so the plan never depends on directory order.
func expandInputs(inputs []string, stderr io.Writer) ([]string, int) {
	seen := make(map[string]bool, len(inputs))
	paths := make([]string, 0, len(inputs))
	appendPath := func(path string) {
		if seen[path] {
			return
		}
		seen[path] = true
		paths = append(paths, path)
	}
	for _, input := range inputs {
		if input == stdinInput {
			appendPath(input)
			continue
		}
		info, err := os.Stat(input)
		if err != nil {
			return nil, configFailure(stderr, fmt.Sprintf("cannot read %s: %v", input, err))
		}
		if !info.IsDir() {
			appendPath(input)
			continue
		}
		err = filepath.WalkDir(input, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !walkableManifest(entry) {
				return nil
			}
			appendPath(path)
			return nil
		})
		if err != nil {
			return nil, configFailure(stderr, fmt.Sprintf("cannot walk %s: %v", input, err))
		}
	}
	sort.Strings(paths)
	return paths, 0
}

// parseInputs parses every path, in order. A manifest error is reported with
// its VAR-PLAN-0003 code and fails the command; unreadable files are usage
// errors.
func parseInputs(paths []string, stdin io.Reader, stderr io.Writer) ([]ingress.Ingress, int) {
	var resources []ingress.Ingress
	for _, path := range paths {
		found, err := parseInput(path, stdin)
		if err != nil {
			var diagnostic *diag.Error
			if errors.As(err, &diagnostic) {
				_, _ = fmt.Fprintf(stderr, "%s: %s: %s\n", diagnostic.Diagnostic.Code, path, diagnostic.Diagnostic.Message)
				return nil, 2
			}
			return nil, configFailure(stderr, fmt.Sprintf("cannot read %s: %v", path, err))
		}
		resources = append(resources, found...)
	}
	return resources, 0
}

// maxManifestBytes caps one manifest input, file or stream, so a huge or
// unending input cannot exhaust memory. It is generous for real manifests.
const maxManifestBytes = 8 << 20

// parseInput reads one manifest input: stdin for -, a file otherwise. The
// input is read through io.LimitReader so it can never exceed maxManifestBytes.
func parseInput(path string, stdin io.Reader) ([]ingress.Ingress, error) {
	var reader io.Reader
	if path == stdinInput {
		reader = stdin
	} else {
		file, err := os.Open(path) //nolint:gosec // the path is the manifest the user asked to plan
		if err != nil {
			return nil, err
		}
		defer func() { _ = file.Close() }()
		reader = file
	}
	content, err := io.ReadAll(io.LimitReader(reader, maxManifestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maxManifestBytes {
		return nil, manifestTooLarge()
	}
	return ingress.Parse(bytes.NewReader(content))
}

// manifestTooLarge builds the VAR-PLAN-0003 diagnostic for an oversized input.
func manifestTooLarge() error {
	message := fmt.Sprintf("manifest is larger than the %d-byte limit varco reads", maxManifestBytes)
	diagnostic, err := diag.New(diag.CodePlanManifest, message)
	if err != nil {
		return errors.New(message)
	}
	return &diag.Error{Diagnostic: diagnostic}
}

// walkableManifest reports whether a directory entry is a manifest varco reads:
// a regular file with a manifest extension. Directories, symlinks (which
// filepath.WalkDir does not follow), and other non-regular files are skipped.
func walkableManifest(entry fs.DirEntry) bool {
	return !entry.IsDir() && entry.Type().IsRegular() && manifestFile(entry.Name())
}

// manifestFile reports whether a directory entry looks like a manifest varco reads.
func manifestFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".yaml", ".yml", ".json":
		return true
	default:
		return false
	}
}
