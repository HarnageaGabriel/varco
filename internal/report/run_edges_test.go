package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/model"
	"github.com/HarnageaGabriel/varco/internal/plan"
)

// emptyRunReport is a report with nothing to show: no expired exceptions, no
// ignored fields, and one case that failed without an error message.
func emptyRunReport() model.RunReport {
	return model.RunReport{
		SchemaVersion: 1,
		Verdict:       model.OutcomeDifferent,
		Coverage:      model.Coverage{Planned: 1, Executed: 1, Different: 1},
		Repeat:        1,
		BodyPolicy:    "2xx",
		Cases: []model.Evaluation{{
			CaseID:  "aaaaaaaaaaaa",
			Host:    "api.example.com",
			Method:  "GET",
			Path:    "/health",
			Family:  plan.FamilyBase,
			Outcome: model.OutcomeDifferent,
		}},
	}
}

// lineWithPrefix returns the first line of a table that starts with prefix.
func lineWithPrefix(t *testing.T, table, prefix string) string {
	t.Helper()
	for _, line := range strings.Split(table, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("table has no line starting with %q:\n%s", prefix, table)
	return ""
}

// lastField returns the last whitespace-separated field of a table line.
func lastField(line string) string {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

// Source: internal/report/run.go RunTable; a report with no expired exceptions
// and no case error prints the dash placeholder, so a column is never empty.
func TestRunTableUsesDashForEmptyValues(t *testing.T) {
	var buffer bytes.Buffer
	if err := RunTable(&buffer, emptyRunReport(), Options{}); err != nil {
		t.Fatal(err)
	}
	table := buffer.String()
	exceptions := lineWithPrefix(t, table, "EXPIRED EXCEPTIONS")
	if lastField(exceptions) != "-" {
		t.Fatalf("expired exceptions line = %q, want the dash placeholder", exceptions)
	}
	ignored := lineWithPrefix(t, table, "IGNORED FIELDS")
	if fields := strings.Fields(ignored); len(fields) != 2 {
		t.Fatalf("ignored fields line = %q, want no value for an empty list", ignored)
	}
	caseRow := lineWithPrefix(t, table, "aaaaaaaaaaaa")
	if lastField(caseRow) != "-" {
		t.Fatalf("case row = %q, want the dash placeholder for the error column", caseRow)
	}
}

// Source: internal/report/run.go RunJSON; empty lists are rendered as [] so a
// consumer never has to handle null.
func TestRunJSONRendersEmptyLists(t *testing.T) {
	var buffer bytes.Buffer
	if err := RunJSON(&buffer, emptyRunReport(), Options{}); err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(buffer.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"expired_exceptions", "ignored_fields", "unsupported", "cases"} {
		value, ok := document[key].([]any)
		if !ok {
			t.Fatalf("%s = %#v, want a list", key, document[key])
		}
		if key != "cases" && len(value) != 0 {
			t.Fatalf("%s = %#v, want an empty list", key, document[key])
		}
	}
}
