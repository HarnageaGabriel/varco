// Fuzz and property tests for the report renderers.
//
// The renderers format untrusted remote data for the operator's terminal, so
// every string a remote server controls is hostile input.
//
// Source: docs/adr/0006-redaction.md - the report layer is the only place
// that serializes what a remote server sent, so it is the layer that keeps a
// hostile response out of the operator's terminal.
// Source: docs/adr/0007-verdict-model.md - the run table lists every case
// that is not EQUIVALENT and every case whose differences were accepted.
// Source: internal/report/escape.go - safeCell rewrites every control byte
// and every invalid UTF-8 byte into a visible escape before a value reaches
// the table writer.
package report

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/HarnageaGabriel/varco/internal/compare"
	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/model"
	"github.com/HarnageaGabriel/varco/internal/plan"
	"github.com/HarnageaGabriel/varco/internal/redact"
)

// fuzzFieldSeparator splits the fuzz input into the fields of a report. It is
// a control byte that no hostile value carries, so every byte of the input
// stays inside the field it belongs to and no field loses a byte to the
// split.
const fuzzFieldSeparator = "\x1f"

// runTableLines is the number of lines the run table of the fuzz report has:
// eight section lines, the CASES line, the column header, four difference
// rows, one explanation row and one row for the case that failed.
const runTableLines = 16

// diffTableLines is the number of lines the diff table of the fuzz report
// has: the column header and one row per difference.
const diffTableLines = 4

// fuzzFields holds every string of a report a remote server can control.
type fuzzFields struct {
	host        string
	path        string
	caseID      string
	family      string
	headerOld   string
	headerNew   string
	locationOld string
	locationNew string
	probeError  string
	subject     string
	issuer      string
	cookie      string
	explanation string
	finding     string
	expired     string
	ignored     string
}

// splitFuzzFields splits the fuzz input into the report fields. A field the
// input does not carry stays empty.
func splitFuzzFields(data []byte) fuzzFields {
	parts := strings.Split(string(data), fuzzFieldSeparator)
	field := func(index int) string {
		if index < len(parts) {
			return parts[index]
		}
		return ""
	}
	return fuzzFields{
		host:        field(0),
		path:        field(1),
		caseID:      field(2),
		family:      field(3),
		headerOld:   field(4),
		headerNew:   field(5),
		locationOld: field(6),
		locationNew: field(7),
		probeError:  field(8),
		subject:     field(9),
		issuer:      field(10),
		cookie:      field(11),
		explanation: field(12),
		finding:     field(13),
		expired:     field(14),
		ignored:     field(15),
	}
}

// shape returns the fields with every remote value replaced by the
// placeholder. The placeholder holds no space, so every cell of the shape
// rendering is one whitespace-separated token and the number of tokens of a
// line is the number of columns of that line.
func (f fuzzFields) shape() fuzzFields {
	const placeholder = "x"
	return fuzzFields{
		host:        placeholder,
		path:        placeholder,
		caseID:      placeholder,
		family:      placeholder,
		headerOld:   placeholder,
		headerNew:   placeholder,
		locationOld: placeholder,
		locationNew: placeholder,
		probeError:  placeholder,
		subject:     placeholder,
		issuer:      placeholder,
		cookie:      placeholder,
		explanation: placeholder,
		finding:     placeholder,
		expired:     placeholder,
		ignored:     placeholder,
	}
}

// runReport builds the run report the fuzz targets render. The structure is
// fixed so that the rendered table has a known shape: one case with four
// differences and one explanation, and one case that failed.
func (f fuzzFields) runReport() model.RunReport {
	return model.RunReport{
		SchemaVersion:    1,
		Verdict:          model.OutcomeDifferent,
		Coverage:         model.Coverage{Planned: 2, Executed: 2, Different: 1, Inconclusive: 1},
		Repeat:           1,
		BodyPolicy:       "2xx",
		UnsupportedCount: 1,
		Unsupported: []plan.Finding{{
			Code:       diag.CodePlanAnnotationNotTestable,
			Ingress:    "api",
			Annotation: "nginx.ingress.kubernetes.io/proxy-read-timeout",
			Message:    f.finding,
		}},
		ExpiredExceptions: []string{f.expired},
		IgnoredFields:     []string{f.ignored},
		Cases: []model.Evaluation{
			{
				CaseID:  f.caseID,
				Host:    f.host,
				Method:  "GET",
				Path:    f.path,
				Family:  f.family,
				Outcome: model.OutcomeDifferent,
				Differences: []model.Difference{
					{
						Code: diag.CodeCompareHeader, Field: "header.content-type",
						Old: f.headerOld, New: f.headerNew,
						Accepted: true, ExceptionID: "compression", Reason: "unstable",
					},
					{Code: diag.CodeCompareLocation, Field: "header.location", Old: f.locationOld, New: f.locationNew},
					{Code: diag.CodeCompareCertificate, Field: "certificate.subject", Old: f.subject, New: f.issuer},
					{Code: diag.CodeCompareCookie, Field: "header.set-cookie", Old: f.cookie, New: f.cookie},
				},
				Explanations: []model.Explanation{{
					ID:      "fuzz-explanation",
					Summary: f.explanation,
					Fix:     "fix",
					Source:  "source",
				}},
			},
			{
				CaseID:  "case-error",
				Host:    f.host,
				Method:  "GET",
				Path:    f.path,
				Family:  f.family,
				Outcome: model.OutcomeInconclusive,
				Error:   f.probeError,
			},
		},
	}
}

// differences builds the differences the diff report renders.
func (f fuzzFields) differences() []compare.Difference {
	return []compare.Difference{
		{Path: f.path, Code: diag.CodeCompareLocation, Field: "header.location", Old: f.locationOld, New: f.locationNew},
		{Path: f.path, Code: diag.CodeCompareHeader, Field: "header.content-type", Old: f.headerOld, New: f.headerNew},
		{Path: f.path, Code: diag.CodeCompareCookie, Field: "header.set-cookie", Old: f.cookie, New: f.subject},
	}
}

// values returns every remote value of the fields, so a check can ask
// whether the input held a byte the output must not carry.
func (f fuzzFields) values() string {
	return strings.Join([]string{
		f.host, f.path, f.caseID, f.family, f.headerOld, f.headerNew,
		f.locationOld, f.locationNew, f.probeError, f.subject, f.issuer,
		f.cookie, f.explanation, f.finding, f.expired, f.ignored,
	}, fuzzFieldSeparator)
}

// FuzzRunTable renders the run table for a report whose every remote string
// comes from the fuzz input.
//
// The table must not panic, must not carry a control byte the renderer did
// not write itself, must stay valid UTF-8, must render the same bytes twice
// and must keep its column layout.
func FuzzRunTable(f *testing.F) {
	f.Add([]byte(fuzzBaseline))
	f.Fuzz(func(t *testing.T, data []byte) {
		fields := splitFuzzFields(data)
		report := fields.runReport()

		var first, second bytes.Buffer
		if err := RunTable(&first, report, Options{}); err != nil {
			t.Fatalf("RunTable: %v", err)
		}
		if err := RunTable(&second, report, Options{}); err != nil {
			t.Fatalf("RunTable: %v", err)
		}
		table := first.String()
		if table != second.String() {
			t.Fatalf("RunTable is not deterministic:\n%s\n---\n%s", table, second.String())
		}
		assertNoControlBytes(t, "run table", table)
		if lines := strings.Count(table, "\n"); lines != runTableLines {
			t.Fatalf("run table has %d lines, want %d:\n%s", lines, runTableLines, table)
		}

		var shaped bytes.Buffer
		if err := RunTable(&shaped, fields.shape().runReport(), Options{}); err != nil {
			t.Fatalf("RunTable: %v", err)
		}
		assertRunTableColumns(t, shaped.String())
	})
}

// FuzzDiffTable renders the diff table for differences whose every remote
// string comes from the fuzz input.
//
// The table must not panic, must not carry a control byte the renderer did
// not write itself, must stay valid UTF-8, must render the same bytes twice
// and must keep its column layout.
func FuzzDiffTable(f *testing.F) {
	f.Add([]byte(fuzzBaseline))
	f.Fuzz(func(t *testing.T, data []byte) {
		fields := splitFuzzFields(data)
		differences := fields.differences()

		var first, second bytes.Buffer
		if err := Table(&first, differences, Options{}); err != nil {
			t.Fatalf("Table: %v", err)
		}
		if err := Table(&second, differences, Options{}); err != nil {
			t.Fatalf("Table: %v", err)
		}
		table := first.String()
		if table != second.String() {
			t.Fatalf("Table is not deterministic:\n%s\n---\n%s", table, second.String())
		}
		assertNoControlBytes(t, "diff table", table)
		if lines := strings.Count(table, "\n"); lines != diffTableLines {
			t.Fatalf("diff table has %d lines, want %d:\n%s", lines, diffTableLines, table)
		}

		var shaped bytes.Buffer
		if err := Table(&shaped, fields.shape().differences(), Options{}); err != nil {
			t.Fatalf("Table: %v", err)
		}
		assertDiffTableColumns(t, shaped.String())
	})
}

// FuzzRunJSON renders the run report as JSON for a report whose every remote
// string comes from the fuzz input.
//
// The document must be valid JSON, must carry no raw control byte the
// renderer did not write itself, must stay valid UTF-8 and must carry every
// string field of the report unchanged.
func FuzzRunJSON(f *testing.F) {
	f.Add([]byte(fuzzBaseline))
	f.Fuzz(func(t *testing.T, data []byte) {
		fields := splitFuzzFields(data)
		report := fields.runReport()

		var document bytes.Buffer
		if err := RunJSON(&document, report, Options{ShowSensitive: true}); err != nil {
			t.Fatalf("RunJSON: %v", err)
		}
		encoded := document.Bytes()
		if !json.Valid(encoded) {
			t.Fatalf("run JSON is not valid:\n%s", encoded)
		}
		if !utf8.Valid(encoded) {
			t.Fatalf("run JSON is not valid UTF-8:\n%q", encoded)
		}
		assertJSONControlBytes(t, "run JSON", encoded)
		assertNoRawControlAdded(t, "run JSON", encoded, []byte(fields.values()))

		var parsed model.RunReport
		if err := json.Unmarshal(encoded, &parsed); err != nil {
			t.Fatalf("run JSON does not parse: %v\n%s", err, encoded)
		}
		assertRunReportRoundTrip(t, fields, parsed)

		var redacted bytes.Buffer
		if err := RunJSON(&redacted, report, Options{}); err != nil {
			t.Fatalf("RunJSON: %v", err)
		}
		if !json.Valid(redacted.Bytes()) {
			t.Fatalf("redacted run JSON is not valid:\n%s", redacted.Bytes())
		}
		assertJSONControlBytes(t, "redacted run JSON", redacted.Bytes())
	})
}

// fuzzBaseline is the plain input every fuzz target starts from: it names a
// host, a path, a case id, a family, two compared header values, two
// Locations, a probe error, a certificate subject and issuer, a cookie name,
// an explanation, a finding, an expired exception and an ignored field.
const fuzzBaseline = "api.example.com\x1f/cb\x1fcase-1\x1fbase\x1ftext/plain\x1fapplication/json" +
	"\x1f/cb?code=old-secret\x1f/cb?code=new-secret\x1fVAR-NET-0001: probe refused" +
	"\x1fCN=old\x1fCN=new\x1fsession\x1fthe ingress rewrites the path" +
	"\x1ftimeouts are not observable\x1fserver-header\x1fcontent-length"

// assertRunTableColumns checks the column count of every line of the shape
// rendering of the run table.
//
// The shape rendering replaces every remote value with a one-character
// placeholder. The tabwriter pads the columns with spaces, so every cell of
// the shape is exactly one whitespace-separated token and the number of
// tokens of a line is the number of columns of that line.
//
// Source: internal/report/run.go RunTable - the table writes eight section
// lines (VERDICT, COVERAGE, BODY POLICY, REPEAT, EXCLUDED, UNSUPPORTED,
// EXPIRED EXCEPTIONS and IGNORED FIELDS), one CASES line, one column header
// line with eleven columns, one row per difference, one row per explanation
// and one row per case without a difference.
func assertRunTableColumns(t *testing.T, shape string) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(shape, "\n"), "\n")
	if len(lines) != runTableLines {
		t.Fatalf("shape run table has %d lines, want %d:\n%s", len(lines), runTableLines, shape)
	}
	plain, accepted := 0, 0
	for _, line := range lines {
		fields := strings.Fields(line)
		switch {
		case strings.HasPrefix(line, "COVERAGE"), strings.HasPrefix(line, "CASES "):
			// A section header whose value the renderer computes. The counter
			// string carries spaces, so the line has no countable columns.
			continue
		case sectionColumns(line) > 0:
			// A section line: the label and the value. The label of a section
			// may hold more than one word, so the line has one column per
			// word of the label plus the value column.
			if columns := sectionColumns(line); len(fields) != columns {
				t.Fatalf("section line %q has %d columns, want %d", line, len(fields), columns)
			}
		case strings.HasPrefix(line, "ID "):
			if len(fields) != 11 {
				t.Fatalf("column header %q has %d columns, want 11", line, len(fields))
			}
		case strings.HasPrefix(strings.TrimLeft(line, " "), "why:"):
			// The explanation row indents ten empty cells and writes one cell
			// with three labels and three values.
			if len(fields) != 6 {
				t.Fatalf("explanation line %q has %d columns, want 6", line, len(fields))
			}
		default:
			// A case row holds eleven cells. The NOTE cell holds the accepted
			// note of an accepted difference (three tokens) or the dash.
			switch len(fields) {
			case 11:
				plain++
			case 13:
				accepted++
			default:
				t.Fatalf("case line %q has %d columns, want 11", line, len(fields))
			}
		}
	}
	if plain != 4 || accepted != 1 {
		t.Fatalf("shape run table has %d plain case lines and %d accepted case lines, want 4 and 1", plain, accepted)
	}
}

// runSectionLabels are the section lines of the run table whose value column
// follows the label.
var runSectionLabels = []string{
	"VERDICT",
	"BODY POLICY",
	"REPEAT",
	"EXCLUDED",
	"UNSUPPORTED",
	"EXPIRED EXCEPTIONS",
	"IGNORED FIELDS",
}

// sectionColumns returns the number of columns a section line of the run
// table has, or zero when the line is not a section line.
//
// A section line holds the label and the value, so it has one column per
// word of the label plus the value column.
func sectionColumns(line string) int {
	for _, label := range runSectionLabels {
		if strings.HasPrefix(line, label+" ") {
			return len(strings.Fields(label)) + 1
		}
	}
	return 0
}

// assertDiffTableColumns checks the column count of every line of the shape
// rendering of the diff table.
//
// Source: internal/report/report.go Table - the table writes one column
// header line with five columns and one row per difference.
func assertDiffTableColumns(t *testing.T, shape string) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(shape, "\n"), "\n")
	if len(lines) != diffTableLines {
		t.Fatalf("shape diff table has %d lines, want %d:\n%s", len(lines), diffTableLines, shape)
	}
	for _, line := range lines {
		if columns := len(strings.Fields(line)); columns != 5 {
			t.Fatalf("diff line %q has %d columns, want 5", line, columns)
		}
	}
}

// assertJSONControlBytes checks that the JSON document carries no raw control
// byte.
//
// Source: encoding/json escapes every byte below 0x20 and the two line
// separators, so the only control byte the document may carry raw is the
// newline the encoder writes at the end.
func assertJSONControlBytes(t *testing.T, label string, document []byte) {
	t.Helper()
	for index, char := range document {
		if char == '\n' {
			continue
		}
		if char < 0x20 {
			t.Fatalf("%s carries the raw control byte %#x at offset %d:\n%s", label, char, index, document)
		}
	}
}

// assertNoRawControlAdded checks that the document carries no raw delete byte
// and no raw C1 control that the input did not carry.
//
// Source: encoding/json escapes the bytes below 0x20 but leaves the delete
// byte and the C1 controls raw, so the document may only carry them when the
// input carried them.
func assertNoRawControlAdded(t *testing.T, label string, document, input []byte) {
	t.Helper()
	if carriesRawControl(input) || !carriesRawControl(document) {
		return
	}
	t.Fatalf("%s carries a raw delete byte or C1 control the input does not have:\n%q", label, document)
}

// carriesRawControl reports whether the bytes hold a raw delete byte or a raw
// C1 control character.
func carriesRawControl(raw []byte) bool {
	for _, char := range raw {
		if char == 0x7f {
			return true
		}
	}
	for _, code := range string(raw) {
		if code >= 0x80 && code <= 0x9f {
			return true
		}
	}
	return false
}

// jsonString returns the string the JSON encoder writes for a value and the
// JSON decoder reads back. encoding/json replaces every invalid UTF-8 byte
// with the replacement character, so a value that is not valid UTF-8 does
// not survive the round-trip unchanged.
func jsonString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var decoded string
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return value
	}
	return decoded
}

// assertEqualString fails when two strings differ.
func assertEqualString(t *testing.T, label, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %q, want %q", label, got, want)
	}
}

// assertRunReportRoundTrip checks that every string field of the JSON report
// survives the round-trip through encoding/json.
//
// The host, method, path and family of a case are not part of the JSON
// contract (internal/model/model.go), so they are not compared. The error of
// a case always passes through redact.Text (internal/report/run.go
// displayReport), so its expectation is the redacted text.
func assertRunReportRoundTrip(t *testing.T, fields fuzzFields, parsed model.RunReport) {
	t.Helper()
	if parsed.SchemaVersion != 1 {
		t.Fatalf("schema version = %d, want 1", parsed.SchemaVersion)
	}
	assertEqualString(t, "verdict", string(parsed.Verdict), string(model.OutcomeDifferent))
	assertEqualString(t, "body policy", parsed.BodyPolicy, jsonString("2xx"))
	if parsed.Redacted {
		t.Fatal("redacted flag is true, want false")
	}
	if len(parsed.ExpiredExceptions) != 1 {
		t.Fatalf("expired exceptions = %d, want 1", len(parsed.ExpiredExceptions))
	}
	assertEqualString(t, "expired exception", parsed.ExpiredExceptions[0], jsonString(fields.expired))
	if len(parsed.IgnoredFields) != 1 {
		t.Fatalf("ignored fields = %d, want 1", len(parsed.IgnoredFields))
	}
	assertEqualString(t, "ignored field", parsed.IgnoredFields[0], jsonString(fields.ignored))
	if len(parsed.Unsupported) != 1 {
		t.Fatalf("unsupported findings = %d, want 1", len(parsed.Unsupported))
	}
	assertEqualString(t, "finding message", parsed.Unsupported[0].Message, jsonString(fields.finding))

	if len(parsed.Cases) != 2 {
		t.Fatalf("cases = %d, want 2", len(parsed.Cases))
	}
	first, second := parsed.Cases[0], parsed.Cases[1]
	assertEqualString(t, "case id", first.CaseID, jsonString(fields.caseID))
	assertEqualString(t, "outcome", string(first.Outcome), string(model.OutcomeDifferent))
	assertEqualString(t, "case error", first.Error, "")
	if len(first.Differences) != 4 {
		t.Fatalf("differences = %d, want 4", len(first.Differences))
	}
	assertDifference(t, "header difference", first.Differences[0], "header.content-type", fields.headerOld, fields.headerNew)
	if !first.Differences[0].Accepted {
		t.Fatal("the header difference is not marked accepted")
	}
	assertEqualString(t, "accepted exception id", first.Differences[0].ExceptionID, "compression")
	assertEqualString(t, "accepted reason", first.Differences[0].Reason, "unstable")
	assertDifference(t, "location difference", first.Differences[1], "header.location", fields.locationOld, fields.locationNew)
	assertDifference(t, "certificate difference", first.Differences[2], "certificate.subject", fields.subject, fields.issuer)
	assertDifference(t, "cookie difference", first.Differences[3], "header.set-cookie", fields.cookie, fields.cookie)
	if len(first.Explanations) != 1 {
		t.Fatalf("explanations = %d, want 1", len(first.Explanations))
	}
	assertEqualString(t, "explanation summary", first.Explanations[0].Summary, jsonString(fields.explanation))
	assertEqualString(t, "explanation fix", first.Explanations[0].Fix, "fix")
	assertEqualString(t, "explanation source", first.Explanations[0].Source, "source")

	assertEqualString(t, "failed case id", second.CaseID, "case-error")
	assertEqualString(t, "failed case outcome", string(second.Outcome), string(model.OutcomeInconclusive))
	assertEqualString(t, "failed case error", second.Error, jsonString(redact.Text(fields.probeError)))
}

// assertDifference checks that a difference of the JSON report carries the
// values the report was built from.
func assertDifference(t *testing.T, label string, difference model.Difference, field, oldValue, newValue string) {
	t.Helper()
	assertEqualString(t, label+" field", difference.Field, field)
	assertEqualString(t, label+" old", difference.Old, jsonString(oldValue))
	assertEqualString(t, label+" new", difference.New, jsonString(newValue))
}

// plainText reports whether the value holds no control byte, no C1 control
// and no invalid UTF-8. Such a value is the fast path of safeCell: it is
// returned unchanged.
func plainText(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, code := range value {
		if code < 0x20 || code == 0x7f || (code >= 0x80 && code <= 0x9f) {
			return false
		}
	}
	return true
}

// TestSafeCellProperties checks the two properties of the escaping that the
// table renderer relies on.
//
// Source: internal/report/escape.go - safeCell returns the value unchanged
// when it holds no control byte and no invalid UTF-8 (the fast path), and it
// rewrites every control byte and every invalid UTF-8 byte into a visible
// escape otherwise.
//
// The escaped text is a fixed point of safeCell. The escape safeCell writes
// is a backslash, an x and two hex digits, and none of those is a control
// byte, so the second pass finds nothing to rewrite. The backslashes
// themselves survive unchanged because safeCell rewrites control bytes only,
// never a backslash. The property asserted here is therefore the strong one:
// escaping the escaped output changes neither the backslashes nor anything
// else.
func TestSafeCellProperties(t *testing.T) {
	base := []string{
		"",
		"plain text",
		"grüße 日本語",
		"a\ufffdb",
		"\x1b[31m",
		"a\rb",
		"a\nb",
		"a\tb",
		"a\x00b",
		"a\x07b",
		"a\x7fb",
		"a\u009bb",
		"a\xffb",
		"a\u202eb",
		"a\u200bb",
		"a\U0001f389b",
		"\x1b]0;title\x07",
		"https://example.com/?code=secret",
	}
	random := rand.New(rand.NewSource(1))
	values := make([]string, 0, len(base)+4000)
	values = append(values, base...)
	for range 4000 {
		buffer := make([]byte, random.Intn(48))
		for index := range buffer {
			buffer[index] = byte(random.Intn(256))
		}
		values = append(values, string(buffer))
	}
	for _, value := range values {
		escaped := safeCell(value)
		if second := safeCell(escaped); second != escaped {
			t.Fatalf("safeCell is not idempotent for %q: %q != %q", value, escaped, second)
		}
		if !utf8.ValidString(escaped) {
			t.Fatalf("safeCell(%q) = %q is not valid UTF-8", value, escaped)
		}
		for _, code := range escaped {
			if code < 0x20 || code == 0x7f || (code >= 0x80 && code <= 0x9f) {
				t.Fatalf("safeCell(%q) = %q holds the control character %U", value, escaped, code)
			}
		}
		if plainText(value) && escaped != value {
			t.Fatalf("safeCell(%q) = %q, want the value unchanged", value, escaped)
		}
	}
}
