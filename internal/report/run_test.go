package report

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/model"
	"github.com/HarnageaGabriel/varco/internal/plan"
	"github.com/HarnageaGabriel/varco/internal/policy"
)

// Source: docs/adr/0007-verdict-model.md (outcomes, coverage, accepted
// differences) and docs/adr/0006-redaction.md (Location redaction).
func runFixture() model.RunReport {
	return model.RunReport{
		SchemaVersion:    1,
		Verdict:          model.OutcomeDifferent,
		Coverage:         model.Coverage{Planned: 3, Executed: 3, Equivalent: 1, Different: 1, Inconclusive: 1},
		Repeat:           3,
		BodyPolicy:       "2xx",
		Excluded:         1,
		UnsupportedCount: 1,
		Unsupported: []plan.Finding{{
			Code: diag.CodePlanAnnotationNotTestable, Ingress: "api", Annotation: "nginx.ingress.kubernetes.io/proxy-read-timeout", Message: "timeouts are not observable black-box",
		}},
		ExpiredExceptions: []string{"server-header"},
		IgnoredFields:     policy.IgnoredFields(),
		Cases: []model.Evaluation{
			{CaseID: "aaaaaaaaaaaa", Host: "app.example.com", Method: "GET", Path: "/", Family: "base", Outcome: model.OutcomeEquivalent, Differences: []model.Difference{}},
			{
				CaseID:  "bbbbbbbbbbbb",
				Host:    "api.example.com",
				Method:  "GET",
				Path:    "/cb",
				Family:  "base",
				Outcome: model.OutcomeDifferent,
				Differences: []model.Difference{
					{Code: diag.CodeCompareStatus, Field: "status", Old: "200", New: "404"},
					{Code: diag.CodeCompareLocation, Field: "header.location", Old: "https://api.example.com/cb?code=old-secret", New: "https://api.example.com/cb?code=new-secret"},
					{Code: diag.CodeCompareHeader, Field: "header.content-encoding", Old: "gzip", New: "br", Accepted: true, Reason: "Envoy compresses differently", ExceptionID: "compression"},
					{Code: diag.CodeCompareHeader, Field: "header.etag", Old: "v1", New: "v2", Accepted: true, Reason: "unstable: differs between repeated observations of new", ExceptionID: "auto:unstable"},
					{Code: diag.CodeCompareBody, Field: "body", Old: "sha256=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa size=1234 truncated=false", New: "sha256=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb size=1234 truncated=false"},
				},
			},
			{CaseID: "cccccccccccc", Host: "api.example.com", Method: "GET", Path: "/cb", Family: "base", Outcome: model.OutcomeInconclusive, Differences: []model.Difference{}, Error: "VAR-NET-0001: probe 10.0.0.2:80: dial tcp: connection refused"},
		},
	}
}

func goldenRun(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "run", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
		t.Fatalf("%s mismatch:\n%s", name, got)
	}
}

func TestRunTableGolden(t *testing.T) {
	var buffer bytes.Buffer
	if err := RunTable(&buffer, runFixture(), Options{}); err != nil {
		t.Fatal(err)
	}
	goldenRun(t, "table.golden", buffer.Bytes())
}

func TestRunJSONGolden(t *testing.T) {
	var buffer bytes.Buffer
	if err := RunJSON(&buffer, runFixture(), Options{}); err != nil {
		t.Fatal(err)
	}
	goldenRun(t, "json.golden", buffer.Bytes())
}

// Source: docs/adr/0006-redaction.md; every Location value is redacted unless
// the caller opts in, in table and JSON alike.
func TestRunRedaction(t *testing.T) {
	for _, format := range []string{"table", "json"} {
		t.Run(format, func(t *testing.T) {
			var redacted, sensitive bytes.Buffer
			render := func(w *bytes.Buffer, options Options) {
				var err error
				if format == "json" {
					err = RunJSON(w, runFixture(), options)
				} else {
					err = RunTable(w, runFixture(), options)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			render(&redacted, Options{})
			if strings.Contains(redacted.String(), "old-secret") || !strings.Contains(redacted.String(), "REDACTED") {
				t.Fatalf("redacted output = %s", redacted.String())
			}
			render(&sensitive, Options{ShowSensitive: true})
			if !strings.Contains(sensitive.String(), "old-secret") {
				t.Fatalf("show-sensitive output = %s", sensitive.String())
			}
			if format == "json" {
				if !strings.Contains(redacted.String(), `"redacted": true`) || !strings.Contains(sensitive.String(), `"redacted": false`) {
					t.Fatalf("redacted flag missing")
				}
			}
		})
	}
}

// Source: docs/adr/0007-verdict-model.md; accepted differences stay visible.
func TestRunTableMarksAccepted(t *testing.T) {
	var buffer bytes.Buffer
	if err := RunTable(&buffer, runFixture(), Options{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buffer.String(), "accepted (compression): Envoy compresses differently") {
		t.Fatalf("accepted difference not marked:\n%s", buffer.String())
	}
}
