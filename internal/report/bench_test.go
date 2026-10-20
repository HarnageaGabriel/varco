package report

import (
	"fmt"
	"io"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/model"
)

// benchRunReport builds a run report with caseCount cases. Every case carries
// one difference, so the renderer writes a full row per case and the JSON
// document holds every case and difference.
func benchRunReport(caseCount int) model.RunReport {
	cases := make([]model.Evaluation, 0, caseCount)
	for index := 0; index < caseCount; index++ {
		cases = append(cases, model.Evaluation{
			CaseID:  fmt.Sprintf("case-%04d", index),
			Outcome: model.OutcomeDifferent,
			Host:    fmt.Sprintf("app-%04d.example.com", index),
			Method:  "GET",
			Path:    fmt.Sprintf("/svc-%04d", index),
			Family:  "base",
			Differences: []model.Difference{{
				Code:  diag.CodeCompareStatus,
				Field: "status",
				Old:   "200",
				New:   "404",
			}},
		})
	}
	return model.RunReport{
		SchemaVersion: 1,
		Verdict:       model.OutcomeDifferent,
		Coverage:      model.Coverage{Planned: caseCount, Executed: caseCount, Different: caseCount},
		Cases:         cases,
	}
}

// BenchmarkRenderTable measures the table rendering of a report with 1000
// cases. The table is written to io.Discard, so only the rendering is
// measured.
func BenchmarkRenderTable(b *testing.B) {
	report := benchRunReport(1000)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := RunTable(io.Discard, report, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRenderJSON measures the JSON rendering of a report with 1000 cases.
func BenchmarkRenderJSON(b *testing.B) {
	report := benchRunReport(1000)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := RunJSON(io.Discard, report, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}
