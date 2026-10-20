package e2e

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/cli"
)

// update rewrites the golden files under ../../testdata/e2e, following the
// repo convention (CONVENTIONS.md: goldens under testdata with -update).
var update = flag.Bool("update", false, "rewrite the golden files under ../../testdata/e2e")

// runResult is everything one cli.Run invocation produced. cmd/varco/main.go
// calls cli.Run with the process streams; the suite captures io buffers
// instead so no process is forked.
type runResult struct {
	exit   int
	stdout string
	stderr string
}

// runCLI drives cli.Run exactly the way cmd/varco/main.go wires it: arguments
// after the program name, a stdin reader, and separate stdout and stderr
// streams. stdin is empty because every scenario loads its plan from --plan.
func runCLI(t *testing.T, args []string) runResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	exit := cli.Run(args, strings.NewReader(""), &stdout, &stderr)
	return runResult{exit: exit, stdout: stdout.String(), stderr: stderr.String()}
}

// testDataPath resolves a name relative to the package directory.
func testDataPath(name string) string {
	return filepath.Join("..", "..", "testdata", "e2e", name)
}

// portOf returns the port of a 127.0.0.1:<port> address for normalization.
func portOf(addr string) string {
	return addr[strings.LastIndexByte(addr, ':')+1:]
}

// refuseRegexp matches the OS-specific tail of a refused dial so goldens stay
// platform stable; the placeholder is inserted after the normalized port.
var refuseRegexp = regexp.MustCompile(`dial tcp 127\.0\.0\.1:<new-port>: [^"\r\n]*`)

// normalizeOutput replaces the random server ports with stable placeholders
// and neutralizes the OS-specific connection-refused message, so golden
// comparison is deterministic across runs and platforms.
func normalizeOutput(value, oldAddr, newAddr string) string {
	normalized := strings.ReplaceAll(value, ":"+portOf(oldAddr), ":<old-port>")
	normalized = strings.ReplaceAll(normalized, ":"+portOf(newAddr), ":<new-port>")
	return refuseRegexp.ReplaceAllString(normalized, "dial tcp 127.0.0.1:<new-port>: <connection refused>")
}

// golden compares got against testdata/e2e/<name>, rewriting the file first
// when -update was passed.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := testDataPath(name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil { //nolint:gosec // the path is the golden name this test chose
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), []byte(got)) {
		t.Fatalf("%s mismatch:\n%s", name, got)
	}
}
