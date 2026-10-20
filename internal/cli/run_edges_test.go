package cli

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/HarnageaGabriel/varco/internal/diag"
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
