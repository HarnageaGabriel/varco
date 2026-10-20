package cli

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/ingress"
	"github.com/HarnageaGabriel/varco/internal/plan"
)

// runCLI drives the run command with the given plan document on stdin.
func runCLI(t *testing.T, planDocument string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(append([]string{"run"}, args...), strings.NewReader(planDocument), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// dryRunArgs are the arguments of a run that never touches the network.
func dryRunArgs(planPath string) []string {
	return []string{"--plan", planPath, "--old", "127.0.0.1:8080", "--new", "127.0.0.1:8080", "--dry-run"}
}

// TestRunRejectsOversizedPlanInput covers the input cap: a plan document
// larger than the 8 MiB limit is a VAR-PLAN-0003 error, not a partial plan.
func TestRunRejectsOversizedPlanInput(t *testing.T) {
	oversized := strings.Repeat("a", ingress.MaxInputBytes+1)
	code, stdout, stderr := runCLI(t, oversized, dryRunArgs("-")...)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stdout = %q", code, stdout)
	}
	if !strings.Contains(stderr, "VAR-PLAN-0003") {
		t.Fatalf("stderr = %q, want a VAR-PLAN-0003 diagnostic", stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want nothing", stdout)
	}
}

// TestRunReportsOversizedPlanWithItsFullDiagnostic locks the whole message
// of the plan failure: the VAR-PLAN-0003 code, the plan path, and the
// reason, in that order.
func TestRunReportsOversizedPlanWithItsFullDiagnostic(t *testing.T) {
	oversized := strings.Repeat("a", ingress.MaxInputBytes+1)
	code, stdout, stderr := runCLI(t, oversized, dryRunArgs("-")...)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stdout = %q", code, stdout)
	}
	want := fmt.Sprintf("VAR-PLAN-0003: -: plan is larger than the %d-byte limit varco reads\n", ingress.MaxInputBytes)
	if stderr != want {
		t.Fatalf("stderr = %q, want %q", stderr, want)
	}
}

// TestRunRejectsOversizedPlanFile covers the same cap for a plan file.
func TestRunRejectsOversizedPlanFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", ingress.MaxInputBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCLI(t, "", dryRunArgs(path)...)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stdout = %q", code, stdout)
	}
	if !strings.Contains(stderr, "VAR-PLAN-0003") {
		t.Fatalf("stderr = %q, want a VAR-PLAN-0003 diagnostic", stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want nothing", stdout)
	}
}

// TestRunAcceptsPlanAtInputCap covers the boundary: a plan document of
// exactly the cap still loads.
func TestRunAcceptsPlanAtInputCap(t *testing.T) {
	document := `{"schema_version":1,"cases":[]}`
	document += strings.Repeat(" ", ingress.MaxInputBytes-len(document))
	if len(document) != ingress.MaxInputBytes {
		t.Fatalf("plan document is %d bytes, want %d", len(document), ingress.MaxInputBytes)
	}
	code, stdout, stderr := runCLI(t, document, dryRunArgs("-")...)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "TOTAL 0") {
		t.Fatalf("stdout = %q, want a dry-run summary", stdout)
	}
}

// TestRunRejectsOversizedExceptionsFile covers the input cap for the
// exceptions file: it is a VAR-CFG-0002 error.
func TestRunRejectsOversizedExceptionsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exceptions.yaml")
	prefix := "version: 1\nexceptions: []\n"
	content := prefix + "#" + strings.Repeat("x", ingress.MaxInputBytes+1-len(prefix)-1)
	if len(content) != ingress.MaxInputBytes+1 {
		t.Fatalf("exceptions file is %d bytes, want %d", len(content), ingress.MaxInputBytes+1)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCLI(t, planJSON(t, nil), append(dryRunArgs("-"), "--exceptions", path)...)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stdout = %q", code, stdout)
	}
	if !strings.Contains(stderr, "VAR-CFG-0002") {
		t.Fatalf("stderr = %q, want a VAR-CFG-0002 diagnostic", stderr)
	}
}

// TestRunAcceptsExceptionsFileAtInputCap covers the boundary: an exceptions
// file of exactly the cap still loads.
func TestRunAcceptsExceptionsFileAtInputCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exceptions.yaml")
	prefix := "version: 1\nexceptions: []\n"
	content := prefix + "#" + strings.Repeat("x", ingress.MaxInputBytes-len(prefix)-1)
	if len(content) != ingress.MaxInputBytes {
		t.Fatalf("exceptions file is %d bytes, want %d", len(content), ingress.MaxInputBytes)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCLI(t, planJSON(t, nil), append(dryRunArgs("-"), "--exceptions", path)...)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stdout = %q stderr = %q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "TOTAL 0") {
		t.Fatalf("stdout = %q, want a dry-run summary", stdout)
	}
}

// TestRunRejectsInvalidPlanCases covers the plan validation: a case that
// carries control bytes in a field the CLI prints is refused, and the error
// names the case index without echoing the value.
func TestRunRejectsInvalidPlanCases(t *testing.T) {
	valid := plan.Case{ID: "aaaaaaaaaaaa", Host: "app.example", Scheme: "http", Method: "GET", Path: "/", Family: "base"}
	withField := func(change func(item *plan.Case)) plan.Case {
		item := valid
		change(&item)
		return item
	}
	tests := []struct {
		name   string
		item   plan.Case
		marker string
	}{
		{name: "host with CRLF", item: withField(func(item *plan.Case) { item.Host = "app.example\r\nX-Injected: 1" }), marker: "X-Injected"},
		{name: "host with ESC", item: withField(func(item *plan.Case) { item.Host = "app.example\x1b[31m" }), marker: "\x1b"},
		{name: "path with CRLF", item: withField(func(item *plan.Case) { item.Path = "/a\r\nX-Injected: 1" }), marker: "X-Injected"},
		{name: "path with ESC", item: withField(func(item *plan.Case) { item.Path = "/a\x1b[31m" }), marker: "\x1b"},
		{name: "id with CRLF", item: withField(func(item *plan.Case) { item.ID = "aaaaaaaaaaaa\r\n" }), marker: "\r\n"},
		{name: "id with ESC", item: withField(func(item *plan.Case) { item.ID = "aaaaaaaaaaaa\x1b" }), marker: "\x1b"},
		{name: "family with CRLF", item: withField(func(item *plan.Case) { item.Family = "base\r\n" }), marker: "\r\n"},
		{name: "family with ESC", item: withField(func(item *plan.Case) { item.Family = "base\x1b" }), marker: "\x1b"},
		{name: "note with CRLF", item: withField(func(item *plan.Case) { item.Note = "note\r\nX-Injected: 1" }), marker: "X-Injected"},
		{name: "note with ESC", item: withField(func(item *plan.Case) { item.Note = "note\x1b[31m" }), marker: "\x1b"},
		{name: "header name with CRLF", item: withField(func(item *plan.Case) {
			item.Headers = []plan.Header{{Name: "X-Test\r\n", Values: []string{"v"}}}
		}), marker: "X-Test"},
		{name: "header name with ESC", item: withField(func(item *plan.Case) {
			item.Headers = []plan.Header{{Name: "X-Test\x1b", Values: []string{"v"}}}
		}), marker: "\x1b"},
		{name: "header value with CRLF", item: withField(func(item *plan.Case) {
			item.Headers = []plan.Header{{Name: "X-Test", Values: []string{"v\r\nX-Injected: 1"}}}
		}), marker: "X-Injected"},
		{name: "header value with ESC", item: withField(func(item *plan.Case) {
			item.Headers = []plan.Header{{Name: "X-Test", Values: []string{"v\x1b[31m"}}}
		}), marker: "\x1b"},
		{name: "Host override with CRLF", item: withField(func(item *plan.Case) {
			item.Headers = []plan.Header{{Name: "Host", Values: []string{"app.example\r\nX-Injected: 1"}}}
		}), marker: "X-Injected"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(t, planJSON(t, []plan.Case{test.item}), dryRunArgs("-")...)
			if code != 2 {
				t.Fatalf("exit code = %d, want 2; stdout = %q", code, stdout)
			}
			if !strings.Contains(stderr, "VAR-CFG-0001") {
				t.Fatalf("stderr = %q, want a VAR-CFG-0001 diagnostic", stderr)
			}
			if !strings.Contains(stderr, "index 0") {
				t.Fatalf("stderr = %q, want the case index", stderr)
			}
			if strings.Contains(stderr, test.marker) {
				t.Fatalf("stderr = %q, want no echo of the invalid value", stderr)
			}
			if stdout != "" {
				t.Fatalf("stdout = %q, want nothing", stdout)
			}
		})
	}
}

// TestRunRejectsInvalidPlanFindingsAndWarnings covers the plan validation of
// the free text an unsupported finding or a warning carries: a control byte
// in a field the report prints is refused, and the error names the index and
// the field without echoing the value.
func TestRunRejectsInvalidPlanFindingsAndWarnings(t *testing.T) {
	tests := []struct {
		name      string
		findings  []plan.Finding
		warnings  []plan.Warning
		wantField string
		marker    string
	}{
		{name: "finding code with ESC", findings: []plan.Finding{{Code: "VAR-PLAN-0005\x1b"}}, wantField: "code", marker: "\x1b"},
		{name: "finding ingress with CRLF", findings: []plan.Finding{{Code: diag.CodePlanRuleWithoutHost, Ingress: "dev/internal\r\nX-Injected: 1"}}, wantField: "ingress", marker: "X-Injected"},
		{name: "finding annotation with ESC", findings: []plan.Finding{{Code: diag.CodePlanAnnotationNotTestable, Annotation: "nginx.ingress.kubernetes.io/proxy-body-size\x1b"}}, wantField: "annotation", marker: "\x1b"},
		{name: "finding path with CRLF", findings: []plan.Finding{{Code: diag.CodePlanRegexSample, Path: "/a\r\nX-Injected: 1"}}, wantField: "path", marker: "X-Injected"},
		{name: "finding message with ESC", findings: []plan.Finding{{Code: diag.CodePlanRuleWithoutHost, Message: "rule has no host\x1b[31m"}}, wantField: "message", marker: "\x1b"},
		{name: "warning code with DEL", warnings: []plan.Warning{{Code: "VAR-PLAN-0005\x7f"}}, wantField: "code", marker: "\x7f"},
		{name: "warning ingress with CRLF", warnings: []plan.Warning{{Code: diag.CodePlanWildcardHost, Ingress: "dev/internal\r\nX-Injected: 1"}}, wantField: "ingress", marker: "X-Injected"},
		{name: "warning message with ESC", warnings: []plan.Warning{{Code: diag.CodePlanWildcardHost, Message: "wildcard host\x1b[31m"}}, wantField: "message", marker: "\x1b"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := planDocumentJSON(t, test.findings, test.warnings)
			code, stdout, stderr := runCLI(t, document, dryRunArgs("-")...)
			if code != 2 {
				t.Fatalf("exit code = %d, want 2; stdout = %q", code, stdout)
			}
			if !strings.Contains(stderr, "VAR-CFG-0001") {
				t.Fatalf("stderr = %q, want a VAR-CFG-0001 diagnostic", stderr)
			}
			if !strings.Contains(stderr, "index 0") {
				t.Fatalf("stderr = %q, want the finding index", stderr)
			}
			if !strings.Contains(stderr, "invalid "+test.wantField) {
				t.Fatalf("stderr = %q, want the %s field", stderr, test.wantField)
			}
			if strings.Contains(stderr, test.marker) {
				t.Fatalf("stderr = %q, want no echo of the invalid value", stderr)
			}
			if stdout != "" {
				t.Fatalf("stdout = %q, want nothing", stdout)
			}
		})
	}
}

// TestRunAcceptsPlanFindingsAndWarnings covers the other side of the plan
// validation: the findings and warnings varco writes itself still load.
func TestRunAcceptsPlanFindingsAndWarnings(t *testing.T) {
	document := planDocumentJSON(t,
		[]plan.Finding{{Code: diag.CodePlanRuleWithoutHost, Ingress: "dev/internal", Message: "rule has no host"}},
		[]plan.Warning{{Code: diag.CodePlanWildcardHost, Ingress: "dev/internal", Message: `wildcard host "*.one.example.com" is planned as "varco-probe.one.example.com"`}},
	)
	code, stdout, stderr := runCLI(t, document, dryRunArgs("-")...)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stdout = %q stderr = %q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "TOTAL 0") {
		t.Fatalf("stdout = %q, want a dry-run summary", stdout)
	}
}

// TestRunAcceptsValidPlanFromTestdata covers the other side of the plan
// validation: the plans varco writes itself still load, host-variant Host
// header overrides included.
func TestRunAcceptsValidPlanFromTestdata(t *testing.T) {
	for _, name := range []string{
		filepath.Join("plan", "app-root-redirect.golden.json"),
		filepath.Join("plan", "multi-regex-host.golden.json"),
		filepath.Join("e2e", "plan-identical.json"),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", "testdata", name)
			code, stdout, stderr := runCLI(t, "", dryRunArgs(path)...)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr)
			}
			if !strings.Contains(stdout, "TOTAL") {
				t.Fatalf("stdout = %q, want a dry-run summary", stdout)
			}
		})
	}
}

// TestRunReportFileMode covers the report file mode: the report is written
// with 0600, like the suggested exceptions file.
func TestRunReportFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file mode bits are not meaningful on Windows")
	}
	server := statusServer(http.StatusOK)
	defer server.Close()
	address := server.Listener.Addr().String()
	output := filepath.Join(t.TempDir(), "report.json")
	document := planJSON(t, []plan.Case{{ID: "aaaaaaaaaaaa", Host: "app.example", Scheme: "http", Method: "GET", Path: "/", Family: "base"}})
	args := []string{"--plan", "-", "--format", "json", "--output", output, "--old", address, "--new", address}
	code, stdout, stderr := runCLI(t, document, args...)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stdout = %q stderr = %q", code, stdout, stderr)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("report file mode = %v, want 0600", mode)
	}
}
