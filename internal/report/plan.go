package report

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/HarnageaGabriel/varco/internal/plan"
)

// PlanDocument is the stable JSON envelope for a plan.
type PlanDocument struct {
	SchemaVersion int            `json:"schema_version"`
	Cases         []plan.Case    `json:"cases"`
	Unsupported   []plan.Finding `json:"unsupported"`
	Warnings      []plan.Warning `json:"warnings"`
}

// PlanJSON writes the versioned JSON plan. Empty lists are rendered as [] so
// consumers never have to handle null.
func PlanJSON(w io.Writer, result plan.Plan) error {
	document := PlanDocument{
		SchemaVersion: 1,
		Cases:         nonNilCases(result.Cases),
		Unsupported:   nonNilFindings(result.Unsupported),
		Warnings:      nonNilWarnings(result.Warnings),
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(document)
}

// PlanTable writes aligned plain-text plan output: one section for the cases
// and one for each list of findings. Every cell is escaped on the way in, so
// a finding message, an annotation value or a warning detail that carries a
// control byte cannot reach the operator's terminal (docs/adr/0006-redaction.md).
func PlanTable(w io.Writer, result plan.Plan) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	for _, row := range planRows(result) {
		if err := writeRow(tw, row...); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// planRows returns the rows of the plan table in order: the cases, the
// unsupported findings and the warnings. Every section is named by its count,
// and a section that holds no entry shows the count alone.
func planRows(result plan.Plan) [][]string {
	rows := [][]string{{fmt.Sprintf("CASES %d", len(result.Cases))}}
	if len(result.Cases) != 0 {
		rows = append(rows, []string{"HOST", "METHOD", "SCHEME", "PATH", "FAMILY", "ID"})
		for _, item := range result.Cases {
			rows = append(rows, []string{item.Host, item.Method, item.Scheme, item.Path, item.Family, item.ID})
		}
	}
	rows = append(rows, []string{fmt.Sprintf("UNSUPPORTED %d", len(result.Unsupported))})
	if len(result.Unsupported) != 0 {
		rows = append(rows, []string{"CODE", "INGRESS", "TARGET", "MESSAGE"})
		for _, finding := range result.Unsupported {
			rows = append(rows, []string{string(finding.Code), finding.Ingress, findingTarget(finding), finding.Message})
		}
	}
	rows = append(rows, []string{fmt.Sprintf("WARNINGS %d", len(result.Warnings))})
	if len(result.Warnings) != 0 {
		rows = append(rows, []string{"CODE", "INGRESS", "MESSAGE"})
		for _, warning := range result.Warnings {
			rows = append(rows, []string{string(warning.Code), warning.Ingress, warning.Message})
		}
	}
	return rows
}

// findingTarget names the annotation or path a finding refers to.
func findingTarget(finding plan.Finding) string {
	switch {
	case finding.Annotation != "":
		return finding.Annotation
	case finding.Path != "":
		return finding.Path
	default:
		return "-"
	}
}

func nonNilCases(cases []plan.Case) []plan.Case {
	if cases == nil {
		return []plan.Case{}
	}
	return cases
}

func nonNilFindings(findings []plan.Finding) []plan.Finding {
	if findings == nil {
		return []plan.Finding{}
	}
	return findings
}

func nonNilWarnings(warnings []plan.Warning) []plan.Warning {
	if warnings == nil {
		return []plan.Warning{}
	}
	return warnings
}
