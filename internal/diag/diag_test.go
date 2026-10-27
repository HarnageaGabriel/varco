package diag

import (
	"errors"
	"strings"
	"testing"
)

func TestRegistry(t *testing.T) {
	tests := []struct {
		code     Code
		area     string
		severity Severity
	}{
		{CodeInvalidFlag, "CFG", SeverityError},
		{CodeEndpointUnreachable, "NET", SeverityError},
		{CodeUnsupportedMethod, "RUN", SeverityError},
		{CodeCompareStatus, "CMP", SeverityInfo},
		{CodeCompareLocation, "CMP", SeverityInfo},
		{CodeCompareHeader, "CMP", SeverityInfo},
		{CodeCompareTLSVersion, "CMP", SeverityInfo},
		{CodeCompareCertificate, "CMP", SeverityInfo},
		{CodeCompareBody, "CMP", SeverityInfo},
		{CodeCompareCookie, "CMP", SeverityInfo},
		{CodePlanConfigMap, "PLAN", SeverityWarning},
		{CodePlanIngressClassExcluded, "PLAN", SeverityWarning},
	}

	for _, test := range tests {
		t.Run(string(test.code), func(t *testing.T) {
			entry, ok := Lookup(test.code)
			if !ok {
				t.Fatal("Lookup() did not find registered code")
			}
			if entry.Area != test.area || entry.Severity != test.severity {
				t.Fatalf("Lookup() = %#v", entry)
			}
		})
	}
	if _, ok := Lookup("VAR-CFG-9999"); ok {
		t.Fatal("Lookup() found unknown code")
	}
}

// TestPlanIngressClassExcludedEntry checks the registry entry of
// VAR-PLAN-0011: the planner reports an Ingress the class filter excluded, so
// the entry has to name the condition, the fix and the reason the exclusion
// matters (an excluded use-regex Ingress changes the host scope).
//
// Sources: docs/dev/add-diagnostic.md ("The Entry has an Area, a Title, a
// Severity, a Description and a Remediation") and docs/plan.md (the known
// limitation that regex scope is computed over the included resources only).
func TestPlanIngressClassExcludedEntry(t *testing.T) {
	entry, ok := Lookup(CodePlanIngressClassExcluded)
	if !ok {
		t.Fatal("Lookup() did not find VAR-PLAN-0011")
	}
	if entry.Title == "" || entry.Description == "" || entry.Remediation == "" {
		t.Fatalf("Lookup() = %#v, want a title, a description and a remediation", entry)
	}
	if !strings.Contains(entry.Description, "excluded") {
		t.Fatalf("description = %q, want it to name the exclusion", entry.Description)
	}
	if !strings.Contains(entry.Remediation, "use-regex") {
		t.Fatalf("remediation = %q, want it to name the excluded use-regex Ingress", entry.Remediation)
	}
}

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		code    Code
		message string
		wantErr bool
	}{
		{name: "default message", code: CodeInvalidFlag},
		{name: "custom message", code: CodeEndpointUnreachable, message: "dial failed"},
		{name: "unknown", code: "VAR-TLS-9999", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := New(test.code, test.message)
			if (err != nil) != test.wantErr {
				t.Fatalf("New() error = %v, wantErr %v", err, test.wantErr)
			}
			if err == nil && (got.Code != test.code || got.Message == "") {
				t.Fatalf("New() = %#v", got)
			}
		})
	}
}

func TestCollectorAndError(t *testing.T) {
	diagnostic, err := New(CodeEndpointUnreachable, "dial failed")
	if err != nil {
		t.Fatal(err)
	}
	var collector Collector
	collector.Add(diagnostic)
	got := collector.List()
	got[0].Message = "changed"
	if collector.List()[0].Message != "dial failed" {
		t.Fatal("List returned mutable collector storage")
	}

	cause := errors.New("network failure")
	diagnosticError := &Error{Diagnostic: diagnostic, Cause: cause}
	if !errors.Is(diagnosticError, cause) {
		t.Fatal("Error does not unwrap its cause")
	}
}
