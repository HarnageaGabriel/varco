package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/plan"
	"github.com/HarnageaGabriel/varco/internal/report"
)

func planJSON(t *testing.T, cases []plan.Case) string {
	t.Helper()
	document := report.PlanDocument{SchemaVersion: 1, Cases: cases, Unsupported: []plan.Finding{}, Warnings: []plan.Warning{}}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// planDocumentJSON encodes a plan document that carries the given findings
// and warnings, so a test can hand the loader untrusted free text.
func planDocumentJSON(t *testing.T, findings []plan.Finding, warnings []plan.Warning) string {
	t.Helper()
	document := report.PlanDocument{SchemaVersion: 1, Cases: []plan.Case{}, Unsupported: findings, Warnings: warnings}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func statusServer(status int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte("body"))
	}))
}

// Source: docs/run.md and docs/adr/0007-verdict-model.md: `varco run` executes a
// plan from JSON or manifests and returns the documented exit codes.
func TestRunCommand(t *testing.T) {
	old, other, same := statusServer(http.StatusOK), statusServer(http.StatusNotFound), statusServer(http.StatusOK)
	defer old.Close()
	defer other.Close()
	defer same.Close()
	oldAddr, otherAddr := old.Listener.Addr().String(), other.Listener.Addr().String()

	oneCase := planJSON(t, []plan.Case{{ID: "aaaaaaaaaaaa", Host: "app.example", Scheme: "http", Method: "GET", Path: "/", Family: "base"}})
	deadListener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddress := deadListener.Addr().String()
	_ = deadListener.Close()

	dir := t.TempDir()
	exceptionPath := filepath.Join(dir, "exceptions.yaml")
	if err := os.WriteFile(exceptionPath, []byte("version: 1\nexceptions:\n  - id: status\n    reason: \"known 404\"\n    match:\n      field: status\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	badExceptionPath := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(badExceptionPath, []byte("version: 9\nexceptions: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(dir, "report.json")
	manifestPath := filepath.Join(dir, "ingress.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	base := []string{"run", "--rate", "10000", "--concurrency", "4", "--quiet"}
	tests := []struct {
		name       string
		planJSON   string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"equivalent", oneCase, []string{"--plan", "-", "--old", oldAddr, "--new", oldAddr}, 0, "VERDICT", ""},
		{"equivalent json", oneCase, []string{"--plan", "-", "--format", "json", "--old", oldAddr, "--new", oldAddr}, 0, `"verdict": "EQUIVALENT"`, ""},
		{"different", oneCase, []string{"--plan", "-", "--old", oldAddr, "--new", otherAddr}, 1, "VAR-CMP-0001", ""},
		{"accepted by exception", oneCase, []string{"--plan", "-", "--old", oldAddr, "--new", otherAddr, "--exceptions", exceptionPath}, 0, "accepted (status): known 404", ""},
		{"inconclusive", oneCase, []string{"--plan", "-", "--old", oldAddr, "--new", deadAddress}, 3, "VAR-NET-0001", ""},
		{"bad exceptions", oneCase, []string{"--plan", "-", "--old", oldAddr, "--new", oldAddr, "--exceptions", badExceptionPath}, 2, "", "VAR-CFG-0002"},
		{"missing exceptions file", oneCase, []string{"--plan", "-", "--old", oldAddr, "--new", oldAddr, "--exceptions", filepath.Join(dir, "nope.yaml")}, 2, "", "nope.yaml"},
		{"unsupported plan version", `{"schema_version": 2, "cases": []}`, []string{"--plan", "-", "--old", oldAddr, "--new", oldAddr}, 2, "", "unsupported plan schema_version"},
		{"malformed plan", "not json", []string{"--plan", "-", "--old", oldAddr, "--new", oldAddr}, 2, "", "cannot parse plan JSON"},
		{"unsafe method", planJSON(t, []plan.Case{{ID: "a", Host: "h", Scheme: "http", Method: "POST", Path: "/"}}), []string{"--plan", "-", "--old", oldAddr, "--new", oldAddr}, 2, "", "VAR-RUN-0001"},
		{"no input", "", []string{"--old", oldAddr, "--new", oldAddr}, 2, "", "requires --plan FILE or -f"},
		{"cluster and plan", oneCase, []string{"--from-cluster", "--plan", "-", "--old", oldAddr, "--new", oldAddr}, 2, "", "use either --from-cluster"},
		{"cluster and file", oneCase, []string{"--from-cluster", "-f", manifestPath, "--old", oldAddr, "--new", oldAddr}, 2, "", "use either --from-cluster"},
		{"both inputs", oneCase, []string{"--plan", "-", "-f", manifestPath, "--old", oldAddr, "--new", oldAddr}, 2, "", "not both"},
		{"missing endpoint", oneCase, []string{"--plan", "-", "--old", oldAddr}, 2, "", "--new is required"},
		{"bad concurrency", oneCase, []string{"--plan", "-", "--concurrency", "99", "--old", oldAddr, "--new", oldAddr}, 2, "", "--concurrency"},
		{"bad format", oneCase, []string{"--plan", "-", "--format", "xml", "--old", oldAddr, "--new", oldAddr}, 2, "", "--format"},
		{"hostname endpoint", oneCase, []string{"--plan", "-", "--old", "example.com", "--new", oldAddr}, 2, "", "--old"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := append(append([]string{}, base...), test.args...)
			var stdout, stderr bytes.Buffer
			if got := Run(args, strings.NewReader(test.planJSON), &stdout, &stderr); got != test.wantCode {
				t.Fatalf("Run() = %d, want %d; stdout=%q stderr=%q", got, test.wantCode, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), test.wantStdout) {
				t.Errorf("stdout = %q, want substring %q", stdout.String(), test.wantStdout)
			}
			if !strings.Contains(stderr.String(), test.wantStderr) {
				t.Errorf("stderr = %q, want substring %q", stderr.String(), test.wantStderr)
			}
		})
	}

	t.Run("manifests produce equivalent cases", func(t *testing.T) {
		addr := same.Listener.Addr().String()
		var stdout, stderr bytes.Buffer
		code := Run(append(append([]string{}, base...), "-f", manifestPath, "--old", addr, "--new", addr), strings.NewReader(""), &stdout, &stderr)
		if code != 0 || !strings.Contains(stdout.String(), "VERDICT") || !strings.Contains(stdout.String(), "EQUIVALENT") {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})

	t.Run("progress lines", func(t *testing.T) {
		addr := old.Listener.Addr().String()
		var stdout, stderr bytes.Buffer
		args := []string{"run", "--rate", "10000", "--plan", "-", "--old", addr, "--new", addr}
		if code := Run(args, strings.NewReader(oneCase), &stdout, &stderr); code != 0 {
			t.Fatalf("code=%d stderr=%q", code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "[1/1] app.example GET / ... EQUIVALENT") {
			t.Fatalf("stderr = %q", stderr.String())
		}
	})

	t.Run("output file", func(t *testing.T) {
		addr := old.Listener.Addr().String()
		var stdout, stderr bytes.Buffer
		args := append(append([]string{}, base...), "--plan", "-", "--format", "json", "--output", outputPath, "--old", addr, "--new", addr)
		if code := Run(args, strings.NewReader(oneCase), &stdout, &stderr); code != 0 {
			t.Fatalf("code=%d stderr=%q", code, stderr.String())
		}
		if stdout.Len() != 0 {
			t.Fatalf("stdout = %q, want empty", stdout.String())
		}
		content, err := os.ReadFile(outputPath)
		if err != nil || !strings.Contains(string(content), `"schema_version": 1`) {
			t.Fatalf("output file = %q err=%v", string(content), err)
		}
	})

	t.Run("plan from file", func(t *testing.T) {
		planPath := filepath.Join(dir, "plan.json")
		if err := os.WriteFile(planPath, []byte(oneCase), 0o600); err != nil {
			t.Fatal(err)
		}
		addr := old.Listener.Addr().String()
		var stdout, stderr bytes.Buffer
		args := append(append([]string{}, base...), "--plan", planPath, "--old", addr, "--new", addr)
		if code := Run(args, strings.NewReader(""), &stdout, &stderr); code != 0 {
			t.Fatalf("code=%d stderr=%q", code, stderr.String())
		}
	})
}

// Source: docs/exit-codes.md; a proven difference (1) outranks missing data (3).
func TestRunCommandDiffExitPrecedence(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/bad") {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer other.Close()
	same := statusServer(http.StatusOK)
	defer same.Close()
	var stdout, stderr bytes.Buffer
	code := Run([]string{"diff", "--scheme", "http", "--host", "h", "--rate", "10000", "--old", same.Listener.Addr().String(), "--new", other.Listener.Addr().String(), "/ok", "/bad"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code = %d, want 1 (difference wins over failure); stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "VAR-NET-0001") {
		t.Fatalf("failure not reported: %q", stderr.String())
	}
}

const manifest = `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: app
spec:
  rules:
    - host: app.example
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: app
                port:
                  number: 80
`
