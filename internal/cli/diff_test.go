package cli

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serverReturning(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
}

// Source: local httptest servers stand in for old and new endpoints.
func TestRunDiff(t *testing.T) {
	a, b, c := serverReturning("same"), serverReturning("same"), serverReturning("other")
	defer a.Close()
	defer b.Close()
	defer c.Close()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := l.Addr().String()
	_ = l.Close()
	addr := func(s *httptest.Server) string { return s.Listener.Addr().String() }
	common := []string{"--scheme", "http", "--host", "app.example", "--rate", "1000", "--timeout", "5s"}
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"identical", []string{"--old", addr(a), "--new", addr(b)}, 0, "identical", ""},
		{"different", []string{"--old", addr(a), "--new", addr(c)}, 1, "VAR-CMP-0006", ""},
		{"different json", []string{"--old", addr(a), "--new", addr(c), "--format", "json"}, 1, `"schema_version": 1`, ""},
		{"unreachable", []string{"--old", addr(a), "--new", dead}, 3, "", "VAR-NET-0001"},
		{"hostname", []string{"--old", "example.com", "--new", addr(b)}, 2, "", "VAR-CFG-0001"},
		{"unsafe method", []string{"--old", addr(a), "--new", addr(b), "--method", "POST"}, 2, "", "VAR-RUN-0001"},
		{"unsafe method allowed", []string{"--old", addr(a), "--new", addr(b), "--method", "POST", "--allow-unsafe-methods"}, 0, "identical", ""},
		{"credential header", []string{"--old", addr(a), "--new", addr(b), "--header", "Authorization=x"}, 2, "", "VAR-CFG-0001"},
		{"bad header", []string{"--old", addr(a), "--new", addr(b), "--header", "nope"}, 2, "", "NAME=VALUE"},
		{"bad format", []string{"--old", addr(a), "--new", addr(b), "--format", "xml"}, 2, "", "--format"},
		{"bad scheme", []string{"--old", addr(a), "--new", addr(b), "--scheme", "ftp"}, 2, "", "--scheme"},
		{"bad rate", []string{"--old", addr(a), "--new", addr(b), "--rate", "0"}, 2, "", "--rate"},
		{"bad timeout", []string{"--old", addr(a), "--new", addr(b), "--timeout", "0s"}, 2, "", "--timeout"},
		{"bad path", []string{"--old", addr(a), "--new", addr(b), "x"}, 2, "", "must start with /"},
		{"missing flags", nil, 2, "", "required"},
		{"unknown flag", []string{"--nope"}, 2, "", ""},
		{"bad new", []string{"--old", addr(a), "--new", "nonsense"}, 2, "", "invalid --new"},
		{"ignore header", []string{"--old", addr(a), "--new", addr(b), "--ignore-header", "x-frame-options"}, 0, "identical", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"diff"}, common...)
			args = append(args, test.args...)
			if test.name == "missing flags" {
				args = []string{"diff"}
			}
			var stdout, stderr bytes.Buffer
			if got := Run(args, &stdout, &stderr); got != test.wantCode {
				t.Fatalf("Run() = %d, want %d; stderr=%q", got, test.wantCode, stderr.String())
			}
			if !strings.Contains(stdout.String(), test.wantStdout) {
				t.Errorf("stdout = %q, want %q", stdout.String(), test.wantStdout)
			}
			if !strings.Contains(stderr.String(), test.wantStderr) {
				t.Errorf("stderr = %q, want %q", stderr.String(), test.wantStderr)
			}
		})
	}
}

func TestRunDiffJSONValid(t *testing.T) {
	a, c := serverReturning("a"), serverReturning("b")
	defer a.Close()
	defer c.Close()
	var stdout, stderr bytes.Buffer
	code := Run([]string{"diff", "--scheme", "http", "--host", "h", "--rate", "1000", "--format", "json", "--old", a.Listener.Addr().String(), "--new", c.Listener.Addr().String(), "/", "/x"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code %d %s", code, stderr.String())
	}
	var doc struct {
		SchemaVersion int  `json:"schema_version"`
		Identical     bool `json:"identical"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil || doc.SchemaVersion != 1 || doc.Identical {
		t.Fatalf("doc %+v err %v", doc, err)
	}
}
