package model_test

import (
	"testing"

	"github.com/HarnageaGabriel/varco/internal/model"
	"github.com/HarnageaGabriel/varco/internal/plan"
)

// Source: docs/adr/0007-verdict-model.md: DIFFERENT wins over INCONCLUSIVE,
// which wins over EQUIVALENT; there is no percentage threshold.
func TestVerdict(t *testing.T) {
	tests := []struct {
		name  string
		cases []model.Evaluation
		want  model.Outcome
	}{
		{"no cases", nil, model.OutcomeEquivalent},
		{"equivalent only", []model.Evaluation{{Outcome: model.OutcomeEquivalent}}, model.OutcomeEquivalent},
		{"inconclusive only", []model.Evaluation{{Outcome: model.OutcomeInconclusive}}, model.OutcomeInconclusive},
		{
			"one different beats many equivalent",
			[]model.Evaluation{
				{Outcome: model.OutcomeEquivalent}, {Outcome: model.OutcomeDifferent}, {Outcome: model.OutcomeEquivalent},
			},
			model.OutcomeDifferent,
		},
		{
			"different beats inconclusive",
			[]model.Evaluation{{Outcome: model.OutcomeInconclusive}, {Outcome: model.OutcomeDifferent}},
			model.OutcomeDifferent,
		},
		{
			"inconclusive beats equivalent",
			[]model.Evaluation{{Outcome: model.OutcomeEquivalent}, {Outcome: model.OutcomeInconclusive}},
			model.OutcomeInconclusive,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := model.Verdict(model.RunReport{Cases: test.cases}); got != test.want {
				t.Fatalf("Verdict() = %s, want %s", got, test.want)
			}
		})
	}
}

// Source: docs/adr/0007-verdict-model.md: coverage is reported beside the verdict.
func TestSummarize(t *testing.T) {
	cases := []model.Evaluation{
		{Outcome: model.OutcomeEquivalent},
		{Outcome: model.OutcomeEquivalent},
		{Outcome: model.OutcomeDifferent},
		{Outcome: model.OutcomeInconclusive},
		{Outcome: model.OutcomeInconclusive, Error: "cancelled"},
	}
	got := model.Summarize(cases, 6, 1)
	want := model.Coverage{Planned: 6, Executed: 5, Equivalent: 2, Different: 1, Inconclusive: 2, Skipped: 1}
	if got != want {
		t.Fatalf("Summarize() = %+v, want %+v", got, want)
	}
}

// Source: internal/plan Case; the runner keeps only the fields a probe needs.
func TestFromPlanCases(t *testing.T) {
	items := []plan.Case{
		{ID: "b", Host: "h", SNI: "h", Scheme: "https", Method: "GET", Path: "/b", Family: "base", Headers: []plan.Header{{Name: "X", Values: []string{"1"}}}},
		{ID: "a", Host: "h", Scheme: "http", Method: "HEAD", Path: "/a", Family: "subpath"},
	}
	got := model.FromPlanCases(items)
	if len(got) != 2 || got[0].ID != "b" || got[1].ID != "a" {
		t.Fatalf("FromPlanCases() = %+v", got)
	}
	if got[0].Headers[0].Name != "X" || got[0].Family != "base" || got[0].Scheme != "https" {
		t.Fatalf("case lost fields: %+v", got[0])
	}
	if got[1].Headers != nil {
		t.Fatalf("expected nil headers, got %+v", got[1].Headers)
	}
}
