package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/kube"
	"github.com/HarnageaGabriel/varco/internal/plan"
)

// warningsInput is the manifest corpus that produces several distinct
// warnings. Source: internal/cli/testdata/warnings/ingress.yaml, the wildcard
// and annotation behaviors of
// https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate.
func warningsInput() string { return filepath.Join("testdata", "warnings", "ingress.yaml") }

// wantWarnings is the sorted warning list of the corpus above: code, then
// Ingress, then message (internal/plan/plan.go lessWarning).
func wantWarnings() []plan.Warning {
	return []plan.Warning{
		{Code: diag.CodePlanWildcardHost, Ingress: "varco/wildcard-one", Message: `wildcard host "*.one.example.com" is planned as "varco-probe.one.example.com"`},
		{Code: diag.CodePlanWildcardHost, Ingress: "varco/wildcard-two", Message: `wildcard host "*.two.example.com" is planned as "varco-probe.two.example.com"`},
		{Code: diag.CodePlanAnnotationBool, Ingress: "varco/wildcard-one", Message: `annotation nginx.ingress.kubernetes.io/force-ssl-redirect has value "perhaps" which is not a boolean; varco treats it as unset`},
		{Code: diag.CodePlanAnnotationBool, Ingress: "varco/wildcard-one", Message: `annotation nginx.ingress.kubernetes.io/ssl-redirect has value "maybe" which is not a boolean; varco treats it as unset`},
	}
}

// Source: internal/cli/plan.go runPlan; a flag the plan command does not know
// is a usage error (exit 2, docs/exit-codes.md).
func TestPlanRejectsUnknownFlag(t *testing.T) {
	code, stdout, stderr := runPlanCLI(t, "", "--bogus")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stdout = %q", code, stdout)
	}
	if !strings.Contains(stderr, "flag provided but not defined") {
		t.Fatalf("stderr = %q, want the flag error", stderr)
	}
}

// Source: internal/cli/plan.go runPlan; the planner profile is nginx or generic
// (docs/adr/0008-planner-profiles.md).
func TestPlanRejectsUnknownProfile(t *testing.T) {
	code, stdout, stderr := runPlanCLI(t, "", "-f", warningsInput(), "--profile", "bogus")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stdout = %q", code, stdout)
	}
	if !strings.Contains(stderr, "--profile must be nginx or generic") {
		t.Fatalf("stderr = %q, want the profile diagnostic", stderr)
	}
}

// Source: internal/cli/plan.go runPlan; --kube-timeout bounds one kubectl
// call, so it must be positive.
func TestPlanRejectsNonPositiveKubeTimeout(t *testing.T) {
	code, stdout, stderr := runPlanCLI(t, "", "-f", warningsInput(), "--kube-timeout", "0")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stdout = %q", code, stdout)
	}
	if !strings.Contains(stderr, "--kube-timeout must be positive") {
		t.Fatalf("stderr = %q, want the timeout diagnostic", stderr)
	}
}

// Source: internal/cli/plan.go loadClusterPlan; a namespace that is not a
// DNS-1123 name is rejected by internal/kube before kubectl is executed, so
// no process starts and no cluster is contacted.
func TestPlanFromClusterRejectsInvalidNamespace(t *testing.T) {
	code, stdout, stderr := runPlanCLI(t, "", "--from-cluster", "--namespace", "Not_A_Namespace")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stdout = %q", code, stdout)
	}
	if !strings.Contains(stderr, "not a valid Kubernetes name") {
		t.Fatalf("stderr = %q, want the kube name diagnostic", stderr)
	}
	if !strings.Contains(stderr, "kube:") {
		t.Fatalf("stderr = %q, want the kube failure prefix", stderr)
	}
}

// Source: internal/cli/plan.go loadClusterPlanWithRunner; the JSON kubectl
// returns must be a list varco can parse.
func TestLoadClusterPlanWithRunnerRejectsUnparsableIngressJSON(t *testing.T) {
	runner := &clusterRunner{responses: map[string]clusterResponse{
		"get ingress -A -o json": {out: []byte("not a kubernetes list")},
	}}
	var stderr strings.Builder
	_, _, code := loadClusterPlanWithRunner(runner, "", time.Second, "", &stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "cannot parse cluster Ingress JSON") {
		t.Fatalf("stderr = %q, want the parse diagnostic", stderr.String())
	}
}

// Source: internal/cli/plan.go loadClusterPlanWithRunner; the controller
// ConfigMap reference is NAMESPACE/NAME.
func TestLoadClusterPlanWithRunnerRejectsBadControllerReference(t *testing.T) {
	runner := &clusterRunner{responses: map[string]clusterResponse{
		"get ingress -A -o json": {out: readKubeFixture(t, "ingresses.json")},
	}}
	for _, reference := range []string{"ingress-nginx", "/controller", "ingress-nginx/"} {
		var stderr strings.Builder
		_, _, code := loadClusterPlanWithRunner(runner, "", time.Second, reference, &stderr)
		if code != 2 {
			t.Fatalf("reference = %q, code = %d, want 2; stderr = %q", reference, code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "--controller-configmap must use NAMESPACE/NAME") {
			t.Fatalf("reference = %q, stderr = %q, want the reference diagnostic", reference, stderr.String())
		}
	}
}

// Source: internal/cli/plan.go loadClusterPlanWithRunner; a failed kubectl
// call for the controller ConfigMap is a kube failure (exit 2).
func TestLoadClusterPlanWithRunnerReportsConfigMapFailure(t *testing.T) {
	runner := &clusterRunner{responses: map[string]clusterResponse{
		"get ingress -A -o json":                    {out: readKubeFixture(t, "ingresses.json")},
		"get configmap controller -n varco -o json": {err: &kube.CommandError{Exit: 1, Stderr: "Error from server (Forbidden): configmaps is forbidden"}},
	}}
	var stderr strings.Builder
	_, _, code := loadClusterPlanWithRunner(runner, "", time.Second, "varco/controller", &stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "kube:") || !strings.Contains(stderr.String(), "forbidden") {
		t.Fatalf("stderr = %q, want the kube failure", stderr.String())
	}
}

// Source: internal/cli/plan.go runPlan; the plan lists its warnings sorted by
// code, then Ingress, then message (internal/plan/plan.go lessWarning).
func TestPlanSortsWarnings(t *testing.T) {
	code, stdout, stderr := runPlanCLI(t, "", "-f", warningsInput(), "--format", "json")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr)
	}
	var document struct {
		Warnings []plan.Warning `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(document.Warnings, wantWarnings()) {
		t.Fatalf("warnings = %#v, want %#v", document.Warnings, wantWarnings())
	}
}

// Source: internal/cli/plan.go kubeFailure; the stderr of kubectl is text a
// remote server controls, so a control byte in it must not reach the operator's
// terminal: it is printed as a visible \xNN escape (docs/adr/0006-redaction.md).
func TestKubeFailureEscapesControlCharacters(t *testing.T) {
	runner := &clusterRunner{responses: map[string]clusterResponse{
		"get ingress -A -o json": {err: &kube.CommandError{
			Exit:   1,
			Stderr: "Error from server: \x1b[31mforbidden\x00\r\n\x1b[0m",
		}},
	}}
	var stderr strings.Builder
	_, _, code := loadClusterPlanWithRunner(runner, "", time.Second, "", &stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2; stderr = %q", code, stderr.String())
	}
	output := stderr.String()
	for _, r := range output {
		if r == '\n' || r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Fatalf("stderr carries the control character %U: %q", r, output)
		}
	}
	for _, want := range []string{`\x1b[31m`, `\x00`, `\x0d`, `\x0a`} {
		if !strings.Contains(output, want) {
			t.Fatalf("stderr = %q, want the escape %s", output, want)
		}
	}
}

// Source: internal/plan/plan.go lessWarning; the CLI comparator orders
// warnings by code, then Ingress, then message.
func TestWarningLess(t *testing.T) {
	tests := []struct {
		name string
		a    plan.Warning
		b    plan.Warning
		want bool
	}{
		{
			name: "code decides",
			a:    plan.Warning{Code: diag.CodePlanWildcardHost, Ingress: "b", Message: "b"},
			b:    plan.Warning{Code: diag.CodePlanAnnotationBool, Ingress: "a", Message: "a"},
			want: true,
		},
		{
			name: "equal code compares the ingress",
			a:    plan.Warning{Code: diag.CodePlanWildcardHost, Ingress: "a"},
			b:    plan.Warning{Code: diag.CodePlanWildcardHost, Ingress: "b"},
			want: true,
		},
		{
			name: "equal ingress compares the message",
			a:    plan.Warning{Code: diag.CodePlanWildcardHost, Ingress: "a", Message: "a"},
			b:    plan.Warning{Code: diag.CodePlanWildcardHost, Ingress: "a", Message: "b"},
			want: true,
		},
		{
			name: "equal warnings are not less",
			a:    plan.Warning{Code: diag.CodePlanWildcardHost, Ingress: "a", Message: "a"},
			b:    plan.Warning{Code: diag.CodePlanWildcardHost, Ingress: "a", Message: "a"},
			want: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := warningLess(test.a, test.b); got != test.want {
				t.Fatalf("warningLess(%#v, %#v) = %v, want %v", test.a, test.b, got, test.want)
			}
		})
	}
}

// Source: internal/cli/plan.go writePlanJSON; empty lists are rendered as []
// so a consumer never has to handle null (docs/plan.md).
func TestWritePlanJSONRendersEmptyLists(t *testing.T) {
	var buffer bytes.Buffer
	if err := writePlanJSON(&buffer, plan.Plan{}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"cases": []`, `"unsupported": []`, `"warnings": []`} {
		if !strings.Contains(buffer.String(), want) {
			t.Fatalf("plan JSON = %s, want %q", buffer.String(), want)
		}
	}
	if strings.Contains(buffer.String(), "null") {
		t.Fatalf("plan JSON = %s, want no null list", buffer.String())
	}
}

// Source: internal/cli/plan.go parseInputs; an unreadable stdin is a usage
// error, not a manifest diagnostic.
func TestPlanReportsUnreadableStdin(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"plan", "-f", "-"}, failingReader{}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2; stdout = %q", code, stdout.String())
	}
	if !strings.Contains(stderr.String(), "cannot read -:") {
		t.Fatalf("stderr = %q, want the read diagnostic", stderr.String())
	}
	if strings.Contains(stderr.String(), "VAR-PLAN-0003") {
		t.Fatalf("stderr = %q, want no manifest diagnostic", stderr.String())
	}
}

// Source: internal/cli/plan.go parseInput; a path the operating system refuses
// to open is reported as a read error, never as a manifest diagnostic.
func TestParseInputReportsUnopenablePath(t *testing.T) {
	if _, err := parseInput("invalid\x00name.yaml", stdin()); err == nil {
		t.Fatal("parseInput() = nil error, want the open error")
	}
}

// Source: internal/cli/plan.go runPlan; a plan report that cannot be written
// is an internal error (exit 4, docs/exit-codes.md).
func TestPlanFailsWhenReportCannotBeWritten(t *testing.T) {
	for _, format := range []string{"table", "json"} {
		t.Run(format, func(t *testing.T) {
			var stderr bytes.Buffer
			args := []string{"plan", "-f", warningsInput(), "--format", format}
			if code := Run(args, stdin(), failingWriter{}, &stderr); code != 4 {
				t.Fatalf("exit code = %d, want 4; stderr = %q", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), "report:") {
				t.Fatalf("stderr = %q, want the report error", stderr.String())
			}
		})
	}
}
