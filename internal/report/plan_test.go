package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/plan"
)

// fixture is a small plan with one case per renderable shape. Sources: the case
// families in docs/adr/0005-plan-case-families.md, whose behaviors come from
// https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate, and
// the diagnostic registry in internal/diag.
func fixture() plan.Plan {
	return plan.Plan{
		Cases: []plan.Case{
			{
				ID:     "0123456789ab",
				Host:   "api.example.com",
				SNI:    "api.example.com",
				Scheme: plan.SchemeHTTPS,
				Method: plan.MethodGet,
				Path:   "/health",
				Family: plan.FamilyBase,
				Source: plan.Source{Ingress: "prod/checkout", Rule: 0, Path: 1},
			},
			{
				ID:      "cdef01234567",
				Host:    "api.example.com",
				Scheme:  plan.SchemeHTTP,
				Method:  plan.MethodOptions,
				Path:    "/health",
				Headers: []plan.Header{{Name: "Access-Control-Request-Method", Values: []string{"GET"}}, {Name: "Origin", Values: []string{"https://varco.invalid"}}},
				Family:  plan.FamilyCORSPreflight,
				Source:  plan.Source{Ingress: "prod/checkout", Rule: 0, Path: 1, Annotation: "nginx.ingress.kubernetes.io/enable-cors"},
				Note:    "enable-cors answers an OPTIONS preflight",
			},
		},
		Unsupported: []plan.Finding{
			{Code: diag.CodePlanAnnotationNotTestable, Ingress: "prod/checkout", Annotation: "nginx.ingress.kubernetes.io/proxy-body-size", Message: "annotation nginx.ingress.kubernetes.io/proxy-body-size cannot be tested black-box by varco"},
			{Code: diag.CodePlanRegexSample, Ingress: "prod/checkout", Path: "/api/v[0-9]+", Message: "cannot derive a sample request path from regex \"/api/v[0-9]+\"; varco never guesses a matching path"},
			{Code: diag.CodePlanRuleWithoutHost, Ingress: "prod/checkout", Message: "rule has no host and no --default-host was given; no case can be planned for it"},
		},
		Warnings: []plan.Warning{
			{Code: diag.CodePlanWildcardHost, Ingress: "prod/checkout", Message: "wildcard host \"*.example.com\" is planned as \"varco-probe.example.com\""},
		},
	}
}

func TestPlanGolden(t *testing.T) {
	tests := []struct {
		name   string
		file   string
		render func(*bytes.Buffer) error
	}{
		{"plan table", "plan_table.golden", func(b *bytes.Buffer) error { return PlanTable(b, fixture()) }},
		{"plan json", "plan_json.golden", func(b *bytes.Buffer) error { return PlanJSON(b, fixture()) }},
		{"plan table empty", "plan_table_empty.golden", func(b *bytes.Buffer) error { return PlanTable(b, plan.Plan{}) }},
		{"plan json empty", "plan_json_empty.golden", func(b *bytes.Buffer) error { return PlanJSON(b, plan.Plan{}) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := test.render(&buf); err != nil {
				t.Fatal(err)
			}
			golden(t, test.file, buf.Bytes())
		})
	}
}

// TestPlanJSONEmptyLists checks that empty lists render as [] rather than null,
// which keeps the schema stable for consumers.
func TestPlanJSONEmptyLists(t *testing.T) {
	var buf bytes.Buffer
	if err := PlanJSON(&buf, plan.Plan{}); err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(buf.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"cases", "unsupported", "warnings"} {
		value, ok := document[key].([]any)
		if !ok || len(value) != 0 {
			t.Fatalf("%s = %#v, want an empty list", key, document[key])
		}
	}
	if document["schema_version"] != float64(1) {
		t.Fatalf("schema_version = %#v", document["schema_version"])
	}
}

// TestPlanTableSections checks the table always names every section, even when
// it is empty.
func TestPlanTableSections(t *testing.T) {
	var buf bytes.Buffer
	if err := PlanTable(&buf, plan.Plan{}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"CASES 0", "UNSUPPORTED 0", "WARNINGS 0"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("table = %q, want %q", buf.String(), want)
		}
	}
	var populated bytes.Buffer
	if err := PlanTable(&populated, fixture()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"CASES 2", "UNSUPPORTED 3", "WARNINGS 1", "HOST", "TARGET"} {
		if !strings.Contains(populated.String(), want) {
			t.Fatalf("table = %q, want %q", populated.String(), want)
		}
	}
}
