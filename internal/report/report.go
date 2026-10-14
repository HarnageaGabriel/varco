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
	if _, err := fmt.Fprintln(tw, "PATH\tCODE\tFIELD\tOLD\tNEW"); err != nil {
		return err
	}
	for _, raw := range differences {
		d := displayDifference(raw, options)
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", d.Path, d.Code, d.Field, d.Old, d.New); err != nil {
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
	if options.ShowSensitive || difference.Field != "header.location" {
		return difference
	}
	oldRaw, newRaw := difference.Old, difference.New
	difference.Old = redact.URL(oldRaw)
	difference.New = redact.URL(newRaw)
	if oldRaw != newRaw && difference.Old == difference.New {
		difference.New += redactedDifferenceNote(oldRaw, newRaw)
	}
	return difference
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
