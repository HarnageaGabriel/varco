package policy_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/HarnageaGabriel/varco/internal/compare"
	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/model"
	"github.com/HarnageaGabriel/varco/internal/policy"
)

const valid = `
version: 1
exceptions:
  - id: server-header
    reason: "Envoy sets its own Server header"
    expires: 2026-12-31
    match:
      host: "*.example.com"
      path: "/api/*"
      family: trailing-slash
      case: 3f2a9c1b04de
      field: "header:Server"
`

// Source: docs/adr/0007-verdict-model.md and docs/run.md fields-file schema.
func TestLoadValid(t *testing.T) {
	file, err := policy.Load([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	if file.Version != 1 || len(file.Exceptions) != 1 {
		t.Fatalf("file = %+v", file)
	}
	exception := file.Exceptions[0]
	if exception.ID != "server-header" || exception.Reason != "Envoy sets its own Server header" {
		t.Fatalf("exception = %+v", exception)
	}
	if !exception.Expires.Set || exception.Expires.Date != time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("expires = %+v", exception.Expires)
	}
	if exception.Match.Host != "*.example.com" || exception.Match.Path != "/api/*" || exception.Match.Field != "header:Server" {
		t.Fatalf("match = %+v", exception.Match)
	}
}

// Source: CONVENTIONS.md diagnostics; every bad exceptions file is VAR-CFG-0002.
func TestLoadRejects(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"empty", "", "empty"},
		{"not a mapping", "- a\n", "cannot parse"},
		{"unknown top key", "version: 1\nnope: 1\n", "cannot parse"},
		{"unknown match key", "version: 1\nexceptions:\n  - id: a\n    reason: r\n    match:\n      nope: 1\n", "cannot parse"},
		{"bad version", "version: 2\nexceptions: []\n", "unsupported exceptions version"},
		{"missing version", "exceptions: []\n", "unsupported exceptions version"},
		{"missing id", "version: 1\nexceptions:\n  - reason: r\n", "missing a required id"},
		{"duplicate id", "version: 1\nexceptions:\n  - id: a\n    reason: r\n  - id: a\n    reason: r\n", "duplicate exception id"},
		{"missing reason", "version: 1\nexceptions:\n  - id: a\n", "missing a required reason"},
		{"bad date", "version: 1\nexceptions:\n  - id: a\n    reason: r\n    expires: tomorrow\n", "not a YYYY-MM-DD"},
		{"bad host glob", "version: 1\nexceptions:\n  - id: a\n    reason: r\n    match:\n      host: \"[\"\n", "invalid glob"},
		{"bad path glob", "version: 1\nexceptions:\n  - id: a\n    reason: r\n    match:\n      path: \"[\"\n", "invalid glob"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := policy.Load([]byte(test.content))
			if err == nil {
				t.Fatal("expected an error")
			}
			var diagnosticError *diag.Error
			if !errors.As(err, &diagnosticError) {
				t.Fatalf("error %v is not a diagnostic", err)
			}
			if diagnosticError.Diagnostic.Code != diag.CodeExceptionsFile {
				t.Fatalf("code = %s, want %s", diagnosticError.Diagnostic.Code, diag.CodeExceptionsFile)
			}
			if !strings.Contains(diagnosticError.Diagnostic.Message, test.want) {
				t.Fatalf("message %q does not contain %q", diagnosticError.Diagnostic.Message, test.want)
			}
		})
	}
}

func TestLoadAcceptsNoExceptions(t *testing.T) {
	file, err := policy.Load([]byte("version: 1\n"))
	if err != nil || len(file.Exceptions) != 0 {
		t.Fatalf("file=%+v err=%v", file, err)
	}
}

// Source: docs/run.md; an exception past its expires date is ignored and listed.
func TestExpiredIDs(t *testing.T) {
	content := "version: 1\nexceptions:\n  - id: past\n    reason: r\n    expires: 2026-01-01\n  - id: future\n    reason: r\n    expires: 2030-01-01\n  - id: forever\n    reason: r\n"
	file, err := policy.Load([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	ids := file.ExpiredIDs(now)
	if len(ids) != 1 || ids[0] != "past" {
		t.Fatalf("ExpiredIDs = %v", ids)
	}
	if ids := file.ExpiredIDs(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); len(ids) != 0 {
		t.Fatalf("expires is inclusive of its own day: %v", ids)
	}
}

func probeCase() model.ProbeCase {
	return model.ProbeCase{ID: "3f2a9c1b04de", Host: "api.example.com", Path: "/api/x", Family: "trailing-slash", Scheme: "https", Method: "GET"}
}

func difference(field string) compare.Difference {
	return compare.Difference{Code: diag.CodeCompareStatus, Field: field, Old: "200", New: "404"}
}

// Source: docs/adr/0007-verdict-model.md: accepted differences stay visible.
func TestEvaluateAccepts(t *testing.T) {
	file, err := policy.Load([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		item  model.ProbeCase
		field string
		want  bool
	}{
		{"all match", probeCase(), "header.server", true},
		{"field shorthand", probeCase(), "header.Server", true},
		{"exact compare field", probeCase(), "header.server", true},
		{"case mismatch", model.ProbeCase{ID: "other", Host: "api.example.com", Path: "/api/x", Family: "trailing-slash"}, "header.server", false},
		{"host glob mismatch", model.ProbeCase{ID: "3f2a9c1b04de", Host: "api.other.com", Path: "/api/x", Family: "trailing-slash"}, "header.server", false},
		{"path glob mismatch", model.ProbeCase{ID: "3f2a9c1b04de", Host: "api.example.com", Path: "/other", Family: "trailing-slash"}, "header.server", false},
		{"family mismatch", model.ProbeCase{ID: "3f2a9c1b04de", Host: "api.example.com", Path: "/api/x", Family: "base"}, "header.server", false},
		{"field mismatch", probeCase(), "status", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evaluation := policy.Evaluate(test.item, []compare.Difference{difference(test.field)}, file, now)
			if evaluation.CaseID != test.item.ID {
				t.Fatalf("case id = %s", evaluation.CaseID)
			}
			if len(evaluation.Differences) != 1 {
				t.Fatalf("differences = %+v", evaluation.Differences)
			}
			got := evaluation.Differences[0].Accepted
			if got != test.want {
				t.Fatalf("accepted = %v, want %v", got, test.want)
			}
			if test.want {
				if evaluation.Outcome != model.OutcomeEquivalent {
					t.Fatalf("outcome = %s, want EQUIVALENT", evaluation.Outcome)
				}
				if evaluation.Differences[0].ExceptionID != "server-header" || evaluation.Differences[0].Reason == "" {
					t.Fatalf("difference = %+v", evaluation.Differences[0])
				}
			} else if evaluation.Outcome != model.OutcomeDifferent {
				t.Fatalf("outcome = %s, want DIFFERENT", evaluation.Outcome)
			}
		})
	}
}

// Source: docs/run.md field reference; shorthand forms map to comparator names.
func TestEvaluateFieldShorthands(t *testing.T) {
	tests := []struct {
		pattern string
		field   string
		want    bool
	}{
		{"status", "status", true},
		{"location", "header.location", true},
		{"header:Server", "header.server", true},
		{"header:server", "header.server", true},
		{"cookie", "header.set-cookie", true},
		{"set-cookie", "header.set-cookie", true},
		{"tls-version", "tls.version", true},
		{"certificate", "certificate.subject", true},
		{"header.location", "header.location", true},
		{"body", "body", true},
		{"location", "header.via", false},
	}
	for _, test := range tests {
		content := "version: 1\nexceptions:\n  - id: e\n    reason: r\n    match:\n      field: \"" + test.pattern + "\"\n"
		file, err := policy.Load([]byte(content))
		if err != nil {
			t.Fatalf("%s: %v", test.pattern, err)
		}
		evaluation := policy.Evaluate(model.ProbeCase{ID: "c"}, []compare.Difference{difference(test.field)}, file, time.Now())
		if got := evaluation.Differences[0].Accepted; got != test.want {
			t.Fatalf("pattern %q field %q accepted = %v, want %v", test.pattern, test.field, got, test.want)
		}
	}
}

// Source: docs/run.md; an expired exception never accepts a difference.
func TestEvaluateIgnoresExpired(t *testing.T) {
	content := "version: 1\nexceptions:\n  - id: old\n    reason: r\n    expires: 2026-01-01\n    match:\n      field: status\n"
	file, err := policy.Load([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	evaluation := policy.Evaluate(model.ProbeCase{ID: "c"}, []compare.Difference{difference("status")}, file, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if evaluation.Differences[0].Accepted || evaluation.Outcome != model.OutcomeDifferent {
		t.Fatalf("expired exception accepted: %+v", evaluation)
	}
}

// Source: docs/adr/0006/0007; the ignored-field list must be reported, and a
// caller must not be able to mutate it.
func TestIgnoredFields(t *testing.T) {
	fields := policy.IgnoredFields()
	if len(fields) != 5 || fields[0] != "Date" {
		t.Fatalf("IgnoredFields = %v", fields)
	}
	fields[0] = "mutated"
	if policy.IgnoredFields()[0] != "Date" {
		t.Fatal("IgnoredFields is not a copy")
	}
}
