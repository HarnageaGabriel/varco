package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/version"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "help", args: []string{"help"}, wantCode: 0, wantStdout: "Usage:"},
		{name: "missing", wantCode: 2, wantStderr: "Usage:"},
		{name: "unknown", args: []string{"wat"}, wantCode: 2, wantStderr: "unknown subcommand"},
		{name: "run without input", args: []string{"run"}, wantCode: 2, wantStderr: "requires --plan FILE or -f"},
		{name: "version", args: []string{"version"}, wantCode: 0, wantStdout: "varco " + version.Version},
		{name: "version args", args: []string{"version", "extra"}, wantCode: 2, wantStderr: "unexpected arguments"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := Run(test.args, stdin(), &stdout, &stderr); got != test.wantCode {
				t.Fatalf("Run() = %d, want %d", got, test.wantCode)
			}
			if !strings.Contains(stdout.String(), test.wantStdout) {
				t.Errorf("stdout = %q, want substring %q", stdout.String(), test.wantStdout)
			}
			if !strings.Contains(stderr.String(), test.wantStderr) {
				t.Errorf("stderr = %q, want substring %q", stderr.String(), test.wantStderr)
			}
		})
	}
}

func TestVersionJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := Run([]string{"version", "--json"}, stdin(), &stdout, &stderr); got != 0 {
		t.Fatalf("Run() = %d; stderr = %q", got, stderr.String())
	}
	var got version.Info
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got != version.Current() {
		t.Fatalf("version JSON = %#v, want %#v", got, version.Current())
	}
}

// stdin returns an empty reader for commands that do not read a stream.
func stdin() io.Reader { return strings.NewReader("") }
