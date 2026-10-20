package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/HarnageaGabriel/varco/internal/model"
	"github.com/HarnageaGabriel/varco/internal/redact"
)

// explanationIndent is the number of empty cells an explanation row is
// indented by, so the explanation lines up under the difference columns.
const explanationIndent = 10

// RunTable writes the run summary block followed by one row per
// non-EQUIVALENT case and its differences. Accepted differences stay visible
// and are marked with their exception id (ADR 0007).
func RunTable(w io.Writer, report model.RunReport, options Options) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	coverage := report.Coverage
	if err := writeRow(tw, "VERDICT", string(report.Verdict)); err != nil {
		return err
	}
	if err := writeRow(tw, "COVERAGE", fmt.Sprintf("planned=%d executed=%d equivalent=%d different=%d inconclusive=%d skipped=%d",
		coverage.Planned, coverage.Executed, coverage.Equivalent, coverage.Different, coverage.Inconclusive, coverage.Skipped)); err != nil {
		return err
	}
	if err := writeRow(tw, "BODY POLICY", report.BodyPolicy); err != nil {
		return err
	}
	if err := writeRow(tw, "REPEAT", fmt.Sprintf("%d", report.Repeat)); err != nil {
		return err
	}
	if err := writeRow(tw, "EXCLUDED", fmt.Sprintf("%d", report.Excluded)); err != nil {
		return err
	}
	if err := writeRow(tw, "UNSUPPORTED", fmt.Sprintf("%d", report.UnsupportedCount)); err != nil {
		return err
	}
	if err := writeRow(tw, "EXPIRED EXCEPTIONS", orDash(report.ExpiredExceptions)); err != nil {
		return err
	}
	if err := writeRow(tw, "IGNORED FIELDS", strings.Join(report.IgnoredFields, ", ")); err != nil {
		return err
	}
	rows := reportableCases(report)
	if _, err := fmt.Fprintf(tw, "CASES %d\n", len(rows)); err != nil {
		return err
	}
	if len(rows) != 0 {
		if err := writeRow(tw, "ID", "HOST", "METHOD", "PATH", "FAMILY", "OUTCOME", "FIELD", "CODE", "OLD", "NEW", "NOTE"); err != nil {
			return err
		}
	}
	for _, evaluation := range rows {
		if len(evaluation.Differences) == 0 {
			if err := writeRow(tw,
				evaluation.CaseID, evaluation.Host, evaluation.Method, evaluation.Path, evaluation.Family,
				string(evaluation.Outcome), "-", "-", "-", "-", runError(evaluation, options)); err != nil {
				return err
			}
			continue
		}
		for _, difference := range evaluation.Differences {
			oldValue, newValue := displayValue(difference.Field, difference.Old, difference.New, options)
			oldValue, newValue = shortenBody(difference.Field, oldValue), shortenBody(difference.Field, newValue)
			if err := writeRow(tw,
				evaluation.CaseID, evaluation.Host, evaluation.Method, evaluation.Path, evaluation.Family,
				string(evaluation.Outcome), difference.Field, string(difference.Code), oldValue, newValue, differenceNote(difference)); err != nil {
				return err
			}
		}
		for _, explanation := range evaluation.Explanations {
			cells := append(make([]string, explanationIndent, explanationIndent+1),
				fmt.Sprintf("why: %s  fix: %s  see: %s", explanation.Summary, explanation.Fix, explanation.Source))
			if err := writeRow(tw, cells...); err != nil {
				return err
			}
		}
	}
	return tw.Flush()
}

// shortenBody keeps the table readable: a body value shows the first 12 hex
// characters of its sha256 plus its size. The JSON report keeps the full value.
func shortenBody(field, value string) string {
	if field != "body" || value == "" {
		return value
	}
	parts := strings.Fields(value)
	short := make([]string, 0, len(parts))
	for _, part := range parts {
		switch {
		case strings.HasPrefix(part, "sha256="):
			hash := strings.TrimPrefix(part, "sha256=")
			if len(hash) > 12 {
				hash = hash[:12]
			}
			short = append(short, "sha256="+hash)
		case part == "truncated=false":
		default:
			short = append(short, part)
		}
	}
	return strings.Join(short, " ")
}

// RunJSON writes the versioned JSON run report. Every display value is redacted
// per ADR 0006 unless the caller opted into raw values.
func RunJSON(w io.Writer, report model.RunReport, options Options) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(displayReport(report, options))
}

// displayReport returns a copy of the report with raw values redacted and the
// redaction choice recorded.
func displayReport(report model.RunReport, options Options) model.RunReport {
	display := report
	display.Redacted = !options.ShowSensitive
	display.Unsupported = nonNilFindings(report.Unsupported)
	display.ExpiredExceptions = nonNilStrings(report.ExpiredExceptions)
	display.IgnoredFields = nonNilStrings(report.IgnoredFields)
	display.Cases = make([]model.Evaluation, len(report.Cases))
	for i, evaluation := range report.Cases {
		item := evaluation
		item.Error = redact.Text(evaluation.Error)
		item.Differences = make([]model.Difference, len(evaluation.Differences))
		for j, difference := range evaluation.Differences {
			converted := difference
			converted.Old, converted.New = displayValue(difference.Field, difference.Old, difference.New, options)
			item.Differences[j] = converted
		}
		display.Cases[i] = item
	}
	return display
}

// reportableCases selects the cases the table lists: every case that is not
// EQUIVALENT, plus a case that is EQUIVALENT only because policy accepted its
// differences. ADR 0007 requires accepted differences to stay visible.
func reportableCases(report model.RunReport) []model.Evaluation {
	rows := make([]model.Evaluation, 0, len(report.Cases))
	for _, evaluation := range report.Cases {
		if evaluation.Outcome != model.OutcomeEquivalent || len(evaluation.Differences) != 0 {
			rows = append(rows, evaluation)
		}
	}
	return rows
}

func differenceNote(difference model.Difference) string {
	if !difference.Accepted {
		return "-"
	}
	return fmt.Sprintf("accepted (%s): %s", difference.ExceptionID, difference.Reason)
}

func runError(evaluation model.Evaluation, options Options) string {
	if evaluation.Error == "" {
		return "-"
	}
	if options.ShowSensitive {
		return evaluation.Error
	}
	return redact.Text(evaluation.Error)
}

func orDash(values []string) string {
	if len(values) == 0 {
		return "-"
	}
	return strings.Join(values, ", ")
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
