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
// and one for each list of findings.
func PlanTable(w io.Writer, result plan.Plan) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintf(tw, "CASES %d\n", len(result.Cases)); err != nil {
		return err
	}
	if len(result.Cases) != 0 {
		if _, err := fmt.Fprintln(tw, "HOST\tMETHOD\tSCHEME\tPATH\tFAMILY\tID"); err != nil {
			return err
		}
		for _, item := range result.Cases {
			if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", item.Host, item.Method, item.Scheme, item.Path, item.Family, item.ID); err != nil {
				return err
			}
		}
	}
	if _, err := fmt.Fprintf(tw, "UNSUPPORTED %d\n", len(result.Unsupported)); err != nil {
		return err
	}
	if len(result.Unsupported) != 0 {
		if _, err := fmt.Fprintln(tw, "CODE\tINGRESS\tTARGET\tMESSAGE"); err != nil {
			return err
		}
		for _, finding := range result.Unsupported {
			if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", finding.Code, finding.Ingress, findingTarget(finding), finding.Message); err != nil {
				return err
			}
		}
	}
	if _, err := fmt.Fprintf(tw, "WARNINGS %d\n", len(result.Warnings)); err != nil {
		return err
	}
	if len(result.Warnings) != 0 {
		if _, err := fmt.Fprintln(tw, "CODE\tINGRESS\tMESSAGE"); err != nil {
			return err
		}
		for _, warning := range result.Warnings {
			if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\n", warning.Code, warning.Ingress, warning.Message); err != nil {
				return err
			}
		}
	}
	return tw.Flush()
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
