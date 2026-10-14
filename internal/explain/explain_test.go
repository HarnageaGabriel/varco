package explain

import (
	"reflect"
	"strings"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/model"
)

func TestExplainRules(t *testing.T) {
	tests := []struct {
		name        string
		family      string
		differences []model.Difference
		wantID      string
	}{
		{
			name:        "case flip",
			family:      "case-flip",
			differences: []model.Difference{{Field: "status", Old: "200", New: "404"}},
			wantID:      "nginx-regex-case-insensitive",
		},
		{
			name:        "prefix boundary",
			family:      "prefix-boundary",
			differences: []model.Difference{{Field: "status", Old: "404", New: "200"}},
			wantID:      "nginx-regex-prefix-host-wide",
		},
		{
			name:        "subpath",
			family:      "subpath",
			differences: []model.Difference{{Field: "status", Old: "404", New: "200"}},
			wantID:      "nginx-regex-prefix-host-wide",
		},
		{
			name:        "trailing slash old 301",
			family:      "trailing-slash",
			differences: []model.Difference{{Field: "status", Old: "301", New: "200"}},
			wantID:      "nginx-trailing-slash-redirect",
		},
		{
			name:        "dot segments",
			family:      "dot-segments",
			differences: []model.Difference{{Field: "status", Old: "301", New: "200"}},
			wantID:      "nginx-url-normalization",
		},
		{
			name:        "double slash location",
			family:      "double-slash",
			differences: []model.Difference{{Field: "header.location", Old: "/a", New: "/b"}},
			wantID:      "nginx-url-normalization",
		},
		{
			name:        "rewrite body",
			family:      "rewrite",
			differences: []model.Difference{{Field: "body", Old: "old", New: "new"}},
			wantID:      "ingress2gateway-rewrite-target",
		},
		{
			name:        "http redirect status",
			family:      "http-redirect",
			differences: []model.Difference{{Field: "status", Old: "308", New: "301"}},
			wantID:      "nginx-http-redirect",
		},
		{
			name:        "default backend status",
			family:      "default-backend",
			differences: []model.Difference{{Field: "status", Old: "404", New: "200"}},
			wantID:      "nginx-default-backend",
		},
		{
			name:        "default backend body",
			family:      "default-backend",
			differences: []model.Difference{{Field: "body", Old: "old", New: "new"}},
			wantID:      "nginx-default-backend",
		},
		{
			name:        "default backend certificate",
			family:      "default-backend",
			differences: []model.Difference{{Field: "certificate.fingerprint", Old: "old", New: "new"}},
			wantID:      "nginx-default-backend",
		},
		{
			name:        "cors preflight header",
			family:      "cors-preflight",
			differences: []model.Difference{{Field: "header.access-control-allow-origin", Old: "*", New: "https://one.invalid"}},
			wantID:      "ingress-nginx-cors-defaults",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evaluation := model.Evaluation{
				Outcome:     model.OutcomeDifferent,
				Differences: test.differences,
			}
			got := Explain(evaluation, test.family)
			if len(got) != 1 {
				t.Fatalf("Explain() returned %d explanations, want 1: %#v", len(got), got)
			}
			want := ruleByID(t, test.wantID)
			wantExplanation := model.Explanation{
				ID:      want.id,
				Summary: want.summary,
				Fix:     want.fix,
				Source:  want.source,
			}
			if !reflect.DeepEqual(got[0], wantExplanation) {
				t.Fatalf("Explain() = %#v, want %#v", got[0], wantExplanation)
			}
		})
	}
}

func TestExplainNegativeCases(t *testing.T) {
	differences := []model.Difference{{Code: diag.CodeCompareStatus, Field: "status", Old: "200", New: "404"}}
	tests := []struct {
		name       string
		evaluation model.Evaluation
		family     string
	}{
		{
			name:       "equivalent",
			evaluation: model.Evaluation{Outcome: model.OutcomeEquivalent, Differences: differences},
			family:     "case-flip",
		},
		{
			name:       "unrelated family",
			evaluation: model.Evaluation{Outcome: model.OutcomeDifferent, Differences: differences},
			family:     "base",
		},
		{
			name:       "trailing slash without old 301",
			evaluation: model.Evaluation{Outcome: model.OutcomeDifferent, Differences: []model.Difference{{Field: "status", Old: "302", New: "200"}}},
			family:     "trailing-slash",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Explain(test.evaluation, test.family); got != nil {
				t.Fatalf("Explain() = %#v, want nil", got)
			}
		})
	}
}

func TestUnsourcedRulesCarryVerifyWording(t *testing.T) {
	for _, id := range []string{"nginx-http-redirect", "nginx-default-backend", "ingress-nginx-cors-defaults"} {
		item := ruleByID(t, id)
		if !strings.Contains(item.summary, "VERIFY") {
			t.Fatalf("%s summary must contain VERIFY: summary=%q", id, item.summary)
		}
	}
}

func TestRuleOrderIsStable(t *testing.T) {
	want := []string{
		"nginx-regex-case-insensitive",
		"nginx-regex-prefix-host-wide",
		"nginx-trailing-slash-redirect",
		"nginx-url-normalization",
		"ingress2gateway-rewrite-target",
		"nginx-http-redirect",
		"nginx-default-backend",
		"ingress-nginx-cors-defaults",
	}
	got := make([]string, len(rules))
	for i, item := range rules {
		got[i] = item.id
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rule order = %v, want %v", got, want)
	}
}

func ruleByID(t *testing.T, id string) rule {
	t.Helper()
	for _, item := range rules {
		if item.id == id {
			return item
		}
	}
	t.Fatalf("rule %q not found", id)
	return rule{}
}
