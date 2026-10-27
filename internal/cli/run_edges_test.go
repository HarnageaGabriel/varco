package cli

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/engine"
	"github.com/HarnageaGabriel/varco/internal/model"
	"github.com/HarnageaGabriel/varco/internal/plan"
)

// runBase is the argument prefix every run test shares: a high rate and no
// progress lines keep the test fast and its stderr readable.
var runBase = []string{"run", "--rate", "10000", "--concurrency", "4", "--quiet"}

// Source: internal/cli/run.go runRun; the flag set of the run command, and the
// usage errors every unknown or invalid value produces (docs/exit-codes.md).
func TestRunFlagErrors(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStderr string
	}{
		{name: "help", args: []string{"-h"}, wantCode: 0, wantStderr: "Usage"},
		{name: "unknown flag", args: []string{"--bogus"}, wantCode: 2, wantStderr: "flag provided but not defined"},
		{name: "unknown profile", args: []string{"--plan", "-", "--profile", "bogus"}, wantCode: 2, wantStderr: "--profile must be nginx or generic"},
		{name: "positional argument", args: []string{"extra-arg"}, wantCode: 2, wantStderr: "run takes no positional arguments"},
		{name: "zero timeout", args: []string{"--plan", "-", "--timeout", "0"}, wantCode: 2, wantStderr: "--timeout must be positive"},
		{name: "zero kube timeout", args: []string{"--plan", "-", "--kube-timeout", "0"}, wantCode: 2, wantStderr: "--kube-timeout must be positive"},
		{name: "zero rate", args: []string{"--plan", "-", "--rate", "0"}, wantCode: 2, wantStderr: "--rate"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := append(append([]string{}, runBase...), test.args...)
			if code := Run(args, stdin(), &stdout, &stderr); code != test.wantCode {
				t.Fatalf("Run() = %d, want %d; stdout = %q stderr = %q", code, test.wantCode, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), test.wantStderr) {
				t.Fatalf("stderr = %q, want substring %q", stderr.String(), test.wantStderr)
			}
		})
	}
}

// Source: internal/cli/run.go runRun; --show-sensitive warns before the run
// starts, because the report then carries raw sensitive values.
func TestRunWarnsAboutSensitiveOutput(t *testing.T) {
	server := statusServer(http.StatusOK)
	defer server.Close()
	address := server.Listener.Addr().String()
	oneCase := planJSON(t, []plan.Case{{ID: "aaaaaaaaaaaa", Host: "app.example", Scheme: "http", Method: "GET", Path: "/", Family: "base"}})
	var stdout, stderr bytes.Buffer
	args := append(append([]string{}, runBase...), "--show-sensitive", "--plan", "-", "--old", address, "--new", address)
	if code := Run(args, strings.NewReader(oneCase), &stdout, &stderr); code != 0 {
		t.Fatalf("Run() = %d, want 0; stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "warning: --show-sensitive exposes raw sensitive values") {
		t.Fatalf("stderr = %q, want the sensitive-output warning", stderr.String())
	}
}

// Source: internal/cli/run.go writeRunReport; a report that cannot be written
// is an internal error (exit 4, docs/exit-codes.md).
func TestRunFailsWhenReportCannotBeWritten(t *testing.T) {
	oldServer, newServer := statusServer(http.StatusOK), statusServer(http.StatusNotFound)
	defer oldServer.Close()
	defer newServer.Close()
	oneCase := planJSON(t, []plan.Case{{ID: "aaaaaaaaaaaa", Host: "app.example", Scheme: "http", Method: "GET", Path: "/", Family: "base"}})
	var stderr bytes.Buffer
	args := append(append([]string{}, runBase...), "--plan", "-", "--old", oldServer.Listener.Addr().String(), "--new", newServer.Listener.Addr().String())
	if code := Run(args, strings.NewReader(oneCase), failingWriter{}, &stderr); code != 4 {
		t.Fatalf("Run() = %d, want 4; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "report:") {
		t.Fatalf("stderr = %q, want the report error", stderr.String())
	}
}

// Source: internal/cli/run.go writeRunReport; --output names a file varco
// creates, and a path it cannot create is an internal error.
func TestRunFailsWhenOutputFileCannotBeCreated(t *testing.T) {
	server := statusServer(http.StatusOK)
	defer server.Close()
	address := server.Listener.Addr().String()
	oneCase := planJSON(t, []plan.Case{{ID: "aaaaaaaaaaaa", Host: "app.example", Scheme: "http", Method: "GET", Path: "/", Family: "base"}})
	output := filepath.Join(t.TempDir(), "missing", "report.json")
	var stdout, stderr bytes.Buffer
	args := append(append([]string{}, runBase...), "--plan", "-", "--output", output, "--old", address, "--new", address)
	if code := Run(args, strings.NewReader(oneCase), &stdout, &stderr); code != 4 {
		t.Fatalf("Run() = %d, want 4; stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "report:") {
		t.Fatalf("stderr = %q, want the report error", stderr.String())
	}
}

// failingCloseFile is a file that accepts every write but fails to close. It
// stands in for a report file that cannot be flushed, so the close path of a
// command can be tested without touching the disk.
type failingCloseFile struct {
	io.Writer
}

func (failingCloseFile) Close() error { return errors.New("close failed") }

// replaceOutputFile points the CLI at a file that fails to close, for the
// duration of one test only.
func replaceOutputFile(t *testing.T, file outputFile) {
	t.Helper()
	original := openOutputFile
	openOutputFile = func(string, int, os.FileMode) (outputFile, error) {
		return file, nil
	}
	t.Cleanup(func() { openOutputFile = original })
}

// Source: internal/cli/run.go writeRunReport; a report file that cannot be
// flushed is an internal error (exit 4, docs/exit-codes.md).
func TestRunFailsWhenReportFileCannotBeClosed(t *testing.T) {
	server := statusServer(http.StatusOK)
	defer server.Close()
	address := server.Listener.Addr().String()
	oneCase := planJSON(t, []plan.Case{{ID: "aaaaaaaaaaaa", Host: "app.example", Scheme: "http", Method: "GET", Path: "/", Family: "base"}})
	replaceOutputFile(t, failingCloseFile{Writer: io.Discard})
	var stdout, stderr bytes.Buffer
	args := append(append([]string{}, runBase...), "--plan", "-", "--output", filepath.Join(t.TempDir(), "report.json"), "--old", address, "--new", address)
	if code := Run(args, strings.NewReader(oneCase), &stdout, &stderr); code != 4 {
		t.Fatalf("Run() = %d, want 4; stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "report:") {
		t.Fatalf("stderr = %q, want the report error", stderr.String())
	}
}

// Source: internal/cli/run.go writeSuggestions; a suggested exceptions file
// that cannot be flushed is an internal error (exit 4, docs/exit-codes.md).
func TestRunFailsWhenSuggestionsFileCannotBeClosed(t *testing.T) {
	server := statusServer(http.StatusOK)
	defer server.Close()
	address := server.Listener.Addr().String()
	oneCase := planJSON(t, []plan.Case{{ID: "aaaaaaaaaaaa", Host: "app.example", Scheme: "http", Method: "GET", Path: "/", Family: "base"}})
	replaceOutputFile(t, failingCloseFile{Writer: io.Discard})
	var stdout, stderr bytes.Buffer
	args := append(append([]string{}, runBase...), "--plan", "-", "--suggest-exceptions", filepath.Join(t.TempDir(), "exceptions.yaml"), "--old", address, "--new", address)
	if code := Run(args, strings.NewReader(oneCase), &stdout, &stderr); code != 4 {
		t.Fatalf("Run() = %d, want 4; stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "suggest-exceptions:") {
		t.Fatalf("stderr = %q, want the suggest-exceptions error", stderr.String())
	}
}

// Source: internal/cli/run.go writeSuggestions; a suggestions file varco cannot
// create is an internal error (exit 4).
func TestRunSuggestExceptionsFailsWhenFileCannotBeCreated(t *testing.T) {
	server := statusServer(http.StatusOK)
	defer server.Close()
	address := server.Listener.Addr().String()
	oneCase := planJSON(t, []plan.Case{{ID: "aaaaaaaaaaaa", Host: "app.example", Scheme: "http", Method: "GET", Path: "/", Family: "base"}})
	suggestions := filepath.Join(t.TempDir(), "missing", "exceptions.yaml")
	var stdout, stderr bytes.Buffer
	args := append(append([]string{}, runBase...), "--plan", "-", "--suggest-exceptions", suggestions, "--old", address, "--new", address)
	if code := Run(args, strings.NewReader(oneCase), &stdout, &stderr); code != 4 {
		t.Fatalf("Run() = %d, want 4; stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "suggest-exceptions:") {
		t.Fatalf("stderr = %q, want the suggest-exceptions error", stderr.String())
	}
}

// Source: internal/cli/run.go loadRunPlan; a plan file that cannot be read is a
// usage error (exit 2).
func TestRunReportsUnreadablePlanFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.json")
	var stdout, stderr bytes.Buffer
	args := []string{"run", "--plan", missing, "--old", "127.0.0.1", "--new", "127.0.0.1"}
	if code := Run(args, stdin(), &stdout, &stderr); code != 2 {
		t.Fatalf("Run() = %d, want 2; stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "cannot read plan") {
		t.Fatalf("stderr = %q, want the plan read diagnostic", stderr.String())
	}
}

// Source: internal/cli/run.go loadRunPlan; a manifest that cannot be read is a
// usage error (exit 2).
func TestRunReportsUnreadableManifest(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	var stdout, stderr bytes.Buffer
	args := []string{"run", "-f", missing, "--old", "127.0.0.1", "--new", "127.0.0.1"}
	if code := Run(args, stdin(), &stdout, &stderr); code != 2 {
		t.Fatalf("Run() = %d, want 2; stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "cannot read") {
		t.Fatalf("stderr = %q, want the manifest read diagnostic", stderr.String())
	}
}

// Source: internal/cli/run.go loadRunPlan; a manifest that cannot be parsed
// keeps its VAR-PLAN-0003 diagnostic (docs/diagnostics/VAR-PLAN-0003.md).
func TestRunReportsUnparsableManifest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ingress.yaml")
	if err := os.WriteFile(path, []byte("not: [a manifest"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	args := []string{"run", "-f", path, "--old", "127.0.0.1", "--new", "127.0.0.1"}
	if code := Run(args, stdin(), &stdout, &stderr); code != 2 {
		t.Fatalf("Run() = %d, want 2; stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "VAR-PLAN-0003") {
		t.Fatalf("stderr = %q, want a VAR-PLAN-0003 diagnostic", stderr.String())
	}
}

// Source: internal/cli/run.go endpointPair; --old-http and --old-https replace
// the base endpoint of one scheme (docs/adr/0004-endpoint-syntax.md).
func TestRunEndpointOverrides(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{name: "invalid http override", args: []string{"--old-http", "not-an-ip", "--old-https", "127.0.0.1"}, wantStderr: "invalid --old-http"},
		{name: "invalid https override", args: []string{"--old-http", "127.0.0.1", "--old-https", "not-an-ip"}, wantStderr: "invalid --old-https"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := append(append([]string{}, runBase...), "--plan", "-")
			args = append(args, test.args...)
			if code := Run(args, stdin(), &stdout, &stderr); code != 2 {
				t.Fatalf("Run() = %d, want 2; stdout = %q stderr = %q", code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), test.wantStderr) {
				t.Fatalf("stderr = %q, want substring %q", stderr.String(), test.wantStderr)
			}
		})
	}
}

// Source: internal/cli/run.go loadRunPlanFromCluster; a namespace that is not a
// DNS-1123 name is rejected by internal/kube before kubectl is executed, so
// no process starts and no cluster is contacted.
func TestRunFromClusterRejectsInvalidNamespace(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := []string{"run", "--from-cluster", "--namespace", "Not_A_Namespace", "--old", "127.0.0.1", "--new", "127.0.0.1"}
	if code := Run(args, stdin(), &stdout, &stderr); code != 2 {
		t.Fatalf("Run() = %d, want 2; stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "not a valid Kubernetes name") {
		t.Fatalf("stderr = %q, want the kube name diagnostic", stderr.String())
	}
}

// clusterIngressJSON is the JSON a fake kubectl returns for
// "get ingress -A -o json". It holds two Ingress resources that warn for
// different reasons, so the cluster plan sorts more than one warning.
// Source: internal/cli/testdata/warnings/ingress.yaml and
// https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate.
const clusterIngressJSON = `{
  "apiVersion": "v1",
  "kind": "List",
  "items": [
    {
      "apiVersion": "networking.k8s.io/v1",
      "kind": "Ingress",
      "metadata": {
        "name": "wildcard-one",
        "namespace": "varco",
        "annotations": {"nginx.ingress.kubernetes.io/ssl-redirect": "maybe"}
      },
      "spec": {
        "rules": [
          {
            "host": "*.one.example.com",
            "http": {"paths": [{"path": "/", "pathType": "Prefix"}]}
          }
        ]
      }
    },
    {
      "apiVersion": "networking.k8s.io/v1",
      "kind": "Ingress",
      "metadata": {"name": "wildcard-two", "namespace": "varco"},
      "spec": {
        "rules": [
          {
            "host": "*.two.example.com",
            "http": {"paths": [{"path": "/", "pathType": "Prefix"}]}
          }
        ]
      }
    }
  ]
}`

// Source: internal/cli/run.go sortWarnings; the cluster plan sorts its
// warnings by code, then Ingress, then message (internal/plan/plan.go
// lessWarning).
func TestLoadRunPlanFromClusterSortsWarnings(t *testing.T) {
	runner := &clusterRunner{responses: map[string]clusterResponse{
		"get ingress -A -o json": {out: []byte(clusterIngressJSON)},
	}}
	var stderr strings.Builder
	result, code := loadRunPlanFromClusterWithRunner(runner, "", time.Second, "", plan.Options{}, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr = %q", code, stderr.String())
	}
	want := []plan.Warning{
		{Code: diag.CodePlanWildcardHost, Ingress: "varco/wildcard-one", Message: `wildcard host "*.one.example.com" is planned as "varco-probe.one.example.com"`},
		{Code: diag.CodePlanWildcardHost, Ingress: "varco/wildcard-two", Message: `wildcard host "*.two.example.com" is planned as "varco-probe.two.example.com"`},
		{Code: diag.CodePlanAnnotationBool, Ingress: "varco/wildcard-one", Message: `annotation nginx.ingress.kubernetes.io/ssl-redirect has value "maybe" which is not a boolean; varco treats it as unset`},
	}
	if !reflect.DeepEqual(result.Warnings, want) {
		t.Fatalf("warnings = %#v, want %#v", result.Warnings, want)
	}
}

// Source: internal/cli/run.go printDryRun; a plan that carries a control byte
// in a field the dry-run line prints cannot put it on the terminal: the
// printer escapes it.
func TestPrintDryRunEscapesControlBytes(t *testing.T) {
	var stdout bytes.Buffer
	printDryRun(&stdout, []engine.DryRequest{{
		Side:     "old",
		Scheme:   "http",
		Endpoint: "127.0.0.1:8080",
		Method:   "GET",
		Host:     "app\x1b[31m.example",
		Path:     "/",
	}})
	if strings.Contains(stdout.String(), "\x1b") {
		t.Fatalf("stdout = %q, want no escape byte", stdout.String())
	}
	if !strings.Contains(stdout.String(), `\x1b`) {
		t.Fatalf("stdout = %q, want the escape byte rendered as text", stdout.String())
	}
}

// Source: internal/cli/run.go unsupportedMethodFailure; the case id and the
// method come from the plan, so the VAR-RUN-0001 message escapes them.
func TestUnsupportedMethodFailureEscapesControlBytes(t *testing.T) {
	var stderr bytes.Buffer
	code := unsupportedMethodFailure([]model.ProbeCase{{
		ID:     "aaaaaaaaaaaa\x1b[31m",
		Host:   "app.example",
		Scheme: "http",
		Method: "DELETE",
		Path:   "/",
	}}, &stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "VAR-RUN-0001") {
		t.Fatalf("stderr = %q, want a VAR-RUN-0001 diagnostic", stderr.String())
	}
	if strings.Contains(stderr.String(), "\x1b") {
		t.Fatalf("stderr = %q, want no escape byte", stderr.String())
	}
	if !strings.Contains(stderr.String(), `\x1b`) {
		t.Fatalf("stderr = %q, want the escape byte rendered as text", stderr.String())
	}
}

// Source: internal/cli/run.go runRun; a case the prober refuses is reported
// with its diagnostic and fails the run (exit 2, docs/exit-codes.md).
func TestRunReportsProbeDiagnostic(t *testing.T) {
	oneCase := planJSON(t, []plan.Case{{ID: "aaaaaaaaaaaa", Host: "bad host", Scheme: "http", Method: "GET", Path: "/", Family: "base"}})
	var stdout, stderr bytes.Buffer
	args := append(append([]string{}, runBase...), "--plan", "-", "--old", "127.0.0.1", "--new", "127.0.0.1")
	if code := Run(args, strings.NewReader(oneCase), &stdout, &stderr); code != 2 {
		t.Fatalf("Run() = %d, want 2; stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "VAR-CFG-0001") || !strings.Contains(stderr.String(), "invalid host") {
		t.Fatalf("stderr = %q, want the probe diagnostic", stderr.String())
	}
}
