package report

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/compare"
	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/model"
	"github.com/HarnageaGabriel/varco/internal/plan"
)

// sectionBreak is the ASCII form feed. text/tabwriter reads it as the end of
// a section, so a rendered value that carries one makes the table flush in
// the middle of a row instead of at the end of the document. It is the only
// way to reach the per-row error path of a renderer.
const sectionBreak = "\f"

// marker is the token the selective writer looks for. It never appears in a
// rendered cell that is not the one under test.
const marker = "varco-marker"

// selectiveFailWriter fails every write whose content contains marker. It
// stands in for a report destination that accepts part of a document and then
// fails, so the renderer must abort instead of returning a partial table.
type selectiveFailWriter struct {
	marker string
}

func (w selectiveFailWriter) Write(payload []byte) (int, error) {
	if strings.Contains(string(payload), w.marker) {
		return 0, os.ErrClosed
	}
	return len(payload), nil
}

// Source: internal/report/plan.go PlanTable; every section header is written
// to the destination, so a destination that fails must abort the render
// instead of producing a partial table.
func TestPlanTableReportsWriterErrors(t *testing.T) {
	tests := []struct {
		name   string
		marker string
	}{
		{name: "cases section", marker: "CASES 1"},
		{name: "unsupported section", marker: "UNSUPPORTED 0"},
		{name: "warnings section", marker: "WARNINGS 0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := PlanTable(selectiveFailWriter{marker: test.marker}, plan.Plan{
				Cases: []plan.Case{{
					ID:     "0123456789ab",
					Host:   "api.example.com",
					Method: plan.MethodGet,
					Scheme: plan.SchemeHTTPS,
					Path:   "/health",
					Family: plan.FamilyBase,
				}},
			})
			if !errors.Is(err, os.ErrClosed) {
				t.Fatalf("PlanTable() error = %v, want %v", err, os.ErrClosed)
			}
		})
	}
}

// Source: internal/report/plan.go PlanTable, internal/report/report.go Table
// and internal/report/run.go RunTable; a cell that carries a section break
// makes text/tabwriter write the current section in the middle of a row, so
// the renderer must surface the writer error instead of dropping it.
func TestRenderersReportWriterErrorsMidRow(t *testing.T) {
	tests := []struct {
		name   string
		render func(io.Writer) error
	}{
		{
			name: "difference row",
			render: func(w io.Writer) error {
				return Table(w, []compare.Difference{{
					Path:  "/" + marker + sectionBreak + "tail",
					Code:  diag.CodeCompareStatus,
					Field: "status",
					Old:   "200",
					New:   "404",
				}}, Options{})
			},
		},
		{
			name: "plan case row",
			render: func(w io.Writer) error {
				return PlanTable(w, plan.Plan{Cases: []plan.Case{{
					ID:     "0123456789ab",
					Host:   "api.example.com",
					Method: plan.MethodGet,
					Scheme: plan.SchemeHTTPS,
					Path:   "/health" + marker + sectionBreak + "tail",
					Family: plan.FamilyBase,
				}}})
			},
		},
		{
			name: "plan finding row",
			render: func(w io.Writer) error {
				return PlanTable(w, plan.Plan{Unsupported: []plan.Finding{{
					Code:    diag.CodePlanRegexSample,
					Ingress: "prod/checkout",
					Message: marker + sectionBreak + " tail",
				}}})
			},
		},
		{
			name: "plan warning row",
			render: func(w io.Writer) error {
				return PlanTable(w, plan.Plan{Warnings: []plan.Warning{{
					Code:    diag.CodePlanWildcardHost,
					Ingress: "prod/checkout",
					Message: marker + sectionBreak + " tail",
				}}})
			},
		},
		{
			name: "run case row",
			render: func(w io.Writer) error {
				return RunTable(w, model.RunReport{Cases: []model.Evaluation{{
					CaseID:  "aaaaaaaaaaaa",
					Host:    "api.example.com",
					Method:  "GET",
					Path:    "/health" + marker + sectionBreak + "tail",
					Family:  plan.FamilyBase,
					Outcome: model.OutcomeDifferent,
				}}}, Options{})
			},
		},
		{
			name: "run difference row",
			render: func(w io.Writer) error {
				return RunTable(w, model.RunReport{Cases: []model.Evaluation{{
					CaseID:  "aaaaaaaaaaaa",
					Host:    "api.example.com",
					Method:  "GET",
					Path:    "/health",
					Family:  plan.FamilyBase,
					Outcome: model.OutcomeDifferent,
					Differences: []model.Difference{{
						Code:  diag.CodeCompareStatus,
						Field: "status",
						Old:   "200" + marker + sectionBreak + "tail",
						New:   "404",
					}},
				}}}, Options{})
			},
		},
		{
			name: "run explanation row",
			render: func(w io.Writer) error {
				return RunTable(w, model.RunReport{Cases: []model.Evaluation{{
					CaseID:  "aaaaaaaaaaaa",
					Host:    "api.example.com",
					Method:  "GET",
					Path:    "/health",
					Family:  plan.FamilyBase,
					Outcome: model.OutcomeDifferent,
					Differences: []model.Difference{{
						Code:  diag.CodeCompareStatus,
						Field: "status",
						Old:   "200",
						New:   "404",
					}},
					Explanations: []model.Explanation{{
						ID:      "nginx-regex-case-insensitive",
						Summary: marker + sectionBreak + " tail",
						Fix:     "correct the path casing",
						Source:  "https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate",
					}},
				}}}, Options{})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.render(selectiveFailWriter{marker: marker})
			if !errors.Is(err, os.ErrClosed) {
				t.Fatalf("render() error = %v, want %v", err, os.ErrClosed)
			}
		})
	}
}
