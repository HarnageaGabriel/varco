// Package report renders deterministic plan and comparison output.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"text/tabwriter"

	"github.com/HarnageaGabriel/varco/internal/compare"
	"github.com/HarnageaGabriel/varco/internal/redact"
)

// Options controls sensitive report output.
type Options struct {
	ShowSensitive bool
}

// JSONDocument is the stable JSON report envelope.
type JSONDocument struct {
	SchemaVersion int                  `json:"schema_version"`
	Redacted      bool                 `json:"redacted"`
	Identical     bool                 `json:"identical"`
	Differences   []compare.Difference `json:"differences"`
}

// Table writes aligned plain-text output.
func Table(w io.Writer, differences []compare.Difference, options Options) error {
	if len(differences) == 0 {
		_, err := fmt.Fprintln(w, "identical")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if err := writeRow(tw, "PATH", "CODE", "FIELD", "OLD", "NEW"); err != nil {
		return err
	}
	for _, raw := range differences {
		d := displayDifference(raw, options)
		if err := writeRow(tw, d.Path, string(d.Code), d.Field, d.Old, d.New); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// JSON writes the versioned JSON report.
func JSON(w io.Writer, differences []compare.Difference, options Options) error {
	if differences == nil {
		differences = []compare.Difference{}
	}
	display := make([]compare.Difference, len(differences))
	for i, difference := range differences {
		display[i] = displayDifference(difference, options)
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(JSONDocument{SchemaVersion: 1, Redacted: !options.ShowSensitive, Identical: len(differences) == 0, Differences: display})
}

func displayDifference(difference compare.Difference, options Options) compare.Difference {
	difference.Old, difference.New = displayValue(difference.Field, difference.Old, difference.New, options)
	return difference
}

// displayValue redacts a difference's two display values when they are
// sensitive and the caller did not opt in. Per ADR 0006 it only redacts the
// Location header; every other field is printed as observed.
func displayValue(field, oldValue, newValue string, options Options) (string, string) {
	if options.ShowSensitive || field != "header.location" {
		return oldValue, newValue
	}
	redactedOld := redact.URL(oldValue)
	redactedNew := redact.URL(newValue)
	if oldValue != newValue && redactedOld == redactedNew {
		redactedNew += redactedDifferenceNote(oldValue, newValue)
	}
	return redactedOld, redactedNew
}

func redactedDifferenceNote(oldRaw, newRaw string) string {
	oldURL, oldErr := url.Parse(oldRaw)
	newURL, newErr := url.Parse(newRaw)
	if oldErr == nil && newErr == nil {
		if oldURL.RawQuery != newURL.RawQuery {
			return " (query values differ)"
		}
		if oldURL.Fragment != newURL.Fragment || oldURL.RawFragment != newURL.RawFragment {
			return " (fragments differ)"
		}
		if fmt.Sprint(oldURL.User) != fmt.Sprint(newURL.User) {
			return " (userinfo differs)"
		}
	}
	return " (sensitive values differ)"
}
