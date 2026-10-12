// Package report renders deterministic comparison output.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/HarnageaGabriel/varco/internal/compare"
)

// JSONDocument is the stable JSON report envelope.
type JSONDocument struct {
	SchemaVersion int                  `json:"schema_version"`
	Identical     bool                 `json:"identical"`
	Differences   []compare.Difference `json:"differences"`
}

// Table writes aligned plain-text output.
func Table(w io.Writer, differences []compare.Difference) error {
	if len(differences) == 0 {
		_, err := fmt.Fprintln(w, "identical")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "PATH\tCODE\tFIELD\tOLD\tNEW"); err != nil {
		return err
	}
	for _, d := range differences {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", d.Path, d.Code, d.Field, d.Old, d.New); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// JSON writes the versioned JSON report.
func JSON(w io.Writer, differences []compare.Difference) error {
	if differences == nil {
		differences = []compare.Difference{}
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(JSONDocument{SchemaVersion: 1, Identical: len(differences) == 0, Differences: differences})
}
