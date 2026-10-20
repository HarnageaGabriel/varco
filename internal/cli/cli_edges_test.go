package cli

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// failingWriter fails every write. It stands in for a stdout or a report file
// that cannot be written, so the report-failure path of a command can be
// tested without touching the disk.
type failingWriter struct{}

func (failingWriter) Write(_ []byte) (int, error) { return 0, errors.New("write failed") }

// failingReader fails every read. It stands in for an unreadable stdin.
type failingReader struct{}

func (failingReader) Read(_ []byte) (int, error) { return 0, errors.New("read failed") }

// Source: internal/cli/cli.go Run; the help subcommand takes no arguments, so
// an extra one is a usage error (exit 2, docs/exit-codes.md).
func TestRunHelpRejectsExtraArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := Run([]string{"help", "extra"}, stdin(), &stdout, &stderr); got != 2 {
		t.Fatalf("Run() = %d, want 2; stderr = %q", got, stderr.String())
	}
	if !strings.Contains(stderr.String(), "help: unexpected arguments") {
		t.Fatalf("stderr = %q, want the unexpected-arguments diagnostic", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want nothing", stdout.String())
	}
}

// Source: internal/cli/cli.go runVersion; a flag the version command does not
// know is a usage error (exit 2).
func TestRunVersionRejectsUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := Run([]string{"version", "--bogus"}, stdin(), &stdout, &stderr); got != 2 {
		t.Fatalf("Run() = %d, want 2; stderr = %q", got, stderr.String())
	}
	if !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("stderr = %q, want the flag error", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want nothing", stdout.String())
	}
}

// Source: internal/cli/cli.go runVersion; a version report that cannot be
// written is an internal error (exit 4, docs/exit-codes.md).
func TestRunVersionJSONFailsWhenStdoutFails(t *testing.T) {
	var stderr bytes.Buffer
	if got := Run([]string{"version", "--json"}, stdin(), failingWriter{}, &stderr); got != 4 {
		t.Fatalf("Run() = %d, want 4; stderr = %q", got, stderr.String())
	}
	if !strings.Contains(stderr.String(), "version:") {
		t.Fatalf("stderr = %q, want the version report error", stderr.String())
	}
}

// Source: internal/cli/cli.go parseHeaders; a request header that is neither a
// credential nor a header varco owns is accepted and sent to the endpoint.
func TestRunDiffSendsAcceptedRequestHeader(t *testing.T) {
	var seen atomic.Value
	newServer := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen.Store(r.Header.Get("X-Trace"))
			_, _ = w.Write([]byte("same"))
		}))
	}
	oldServer, newEndpoint := newServer(), newServer()
	defer oldServer.Close()
	defer newEndpoint.Close()
	var stdout, stderr bytes.Buffer
	args := []string{"diff", "--scheme", "http", "--host", "app.example", "--rate", "1000", "--header", "X-Trace=1", "--old", oldServer.Listener.Addr().String(), "--new", newEndpoint.Listener.Addr().String()}
	if code := Run(args, stdin(), &stdout, &stderr); code != 0 {
		t.Fatalf("code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "identical") {
		t.Fatalf("stdout = %q, want the identical report", stdout.String())
	}
	if got, _ := seen.Load().(string); got != "1" {
		t.Fatalf("X-Trace header = %q, want %q", got, "1")
	}
}

// Source: internal/cli/cli.go runDiff; a difference report that cannot be
// written is an internal error (exit 4, docs/exit-codes.md).
func TestRunDiffFailsWhenReportCannotBeWritten(t *testing.T) {
	oldServer, newServer := serverReturning("old"), serverReturning("new")
	defer oldServer.Close()
	defer newServer.Close()
	var stderr bytes.Buffer
	args := []string{"diff", "--scheme", "http", "--host", "app.example", "--rate", "1000", "--old", oldServer.Listener.Addr().String(), "--new", newServer.Listener.Addr().String()}
	if code := Run(args, stdin(), failingWriter{}, &stderr); code != 4 {
		t.Fatalf("code = %d, want 4; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "report:") {
		t.Fatalf("stderr = %q, want the report error", stderr.String())
	}
}
