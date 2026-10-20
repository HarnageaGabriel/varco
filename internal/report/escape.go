package report

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/HarnageaGabriel/varco/internal/redact"
)

// safeCell makes one table cell safe to print. Every control character and
// every invalid UTF-8 byte becomes a visible \xNN escape, so a hostile or
// compromised server cannot put ANSI sequences, carriage returns or line
// breaks into the operator's terminal through the table report. Printable
// runes and valid UTF-8 stay unchanged.
//
// The escaping itself lives in the pure redact package, so the CLI prints the
// remote text it shows to the operator the same way (docs/adr/0006-redaction.md).
func safeCell(value string) string {
	return redact.EscapeControl(value)
}

// writeRow writes one row of cells to the table. Every cell is escaped on the
// way in, so this is the one place where text reaches the table writer.
func writeRow(writer *tabwriter.Writer, cells ...string) error {
	escaped := make([]string, len(cells))
	for index, cell := range cells {
		escaped[index] = safeCell(cell)
	}
	_, err := fmt.Fprintln(writer, strings.Join(escaped, "\t"))
	return err
}
