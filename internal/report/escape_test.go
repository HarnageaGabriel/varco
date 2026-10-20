package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/HarnageaGabriel/varco/internal/compare"
	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/model"
)

// hostile carries every byte class a remote server could smuggle into a
// report: an ANSI sequence, CR, LF, NUL, BEL, DEL, a C1 control (U+009B) and
// an invalid UTF-8 byte.
const hostile = "text\x1b[31m\r\n\x00\x07\x7f\u009b\xff end"

// hostileRunReport is a run report whose every remote field carries hostile
// bytes: the case identity, a compared header, a Location, a certificate
// subject, a cookie name and the probe error.
func hostileRunReport() model.RunReport {
	return model.RunReport{
		SchemaVersion: 1,
		Verdict:       model.OutcomeDifferent,
		Coverage:      model.Coverage{Planned: 2, Executed: 2, Different: 1, Inconclusive: 1},
		Repeat:        1,
		BodyPolicy:    "2xx",
		Cases: []model.Evaluation{
			{
				CaseID:  "case\x1b[31m",
				Host:    "api.example.com\x07",
				Method:  "GET",
				Path:    "/cb\x00",
				Family:  "base",
				Outcome: model.OutcomeDifferent,
				Differences: []model.Difference{
					{Code: diag.CodeCompareHeader, Field: "header.content-type", Old: "text/plain" + hostile, New: "application/json\x07"},
					{Code: diag.CodeCompareLocation, Field: "header.location", Old: "/cb?token=old-secret\x1b[31m", New: "/cb?token=new-secret"},
					{Code: diag.CodeCompareCertificate, Field: "certificate.subject", Old: "CN=evil" + hostile, New: "CN=good"},
					{Code: diag.CodeCompareCookie, Field: "header.set-cookie", Old: "sess\x1b[31m=secret", New: "sess=other"},
				},
			},
			{
				CaseID:  "case2",
				Host:    "api.example.com",
				Method:  "GET",
				Path:    "/health",
				Family:  "base",
				Outcome: model.OutcomeInconclusive,
				Error:   "VAR-NET-0001: probe refused \x1b[31m\r\n",
			},
		},
	}
}

// hostileDifferences is the same hostile data as the diff report sees it.
func hostileDifferences() []compare.Difference {
	return []compare.Difference{
		{Path: "/cb\x00", Code: diag.CodeCompareHeader, Field: "header.content-type", Old: "text/plain" + hostile, New: "application/json\x07"},
		{Path: "/cb\x00", Code: diag.CodeCompareLocation, Field: "header.location", Old: "/cb?token=old-secret\x1b[31m", New: "/cb?token=new-secret"},
		{Path: "/cb\x00", Code: diag.CodeCompareCertificate, Field: "certificate.subject", Old: "CN=evil" + hostile, New: "CN=good"},
		{Path: "/cb\x00", Code: diag.CodeCompareCookie, Field: "header.set-cookie", Old: "sess\x1b[31m=secret", New: "sess=other"},
	}
}

// assertNoControlBytes fails when the output carries a control character the
// renderer did not write itself. The newline and the tab the renderer uses to
// lay the table out are the only control characters a table may contain.
func assertNoControlBytes(t *testing.T, label, output string) {
	t.Helper()
	if !utf8.ValidString(output) {
		t.Fatalf("%s is not valid UTF-8: %q", label, output)
	}
	for _, r := range output {
		if r == '\n' || r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Fatalf("%s carries the control character %U: %q", label, r, output)
		}
	}
}

// wantEscapes are the visible escapes the table must show for the control
// characters of the hostile value.
var wantEscapes = []string{`\x1b[31m`, `\x0d`, `\x0a`, `\x00`, `\x07`, `\x7f`, `\x9b`, `\xff`}

// Source: docs/adr/0006-redaction.md: the report layer is the only place that
// serializes what a remote server sent, so it is the layer that keeps a
// hostile response out of the operator's terminal. A compared header, a
// Location, a certificate subject, a cookie name or a probe error must not be
// able to write ANSI sequences or rewrite a line of the table.
func TestSafeCell(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"empty", "", ""},
		{"printable", "plain text", "plain text"},
		{"non ascii stays", "grüße 日本語", "grüße 日本語"},
		{"replacement character stays", "a\ufffdb", "a\ufffdb"},
		{"escape sequence", "\x1b[31m", `\x1b[31m`},
		{"carriage return", "a\rb", `a\x0db`},
		{"line feed", "a\nb", `a\x0ab`},
		{"tab", "a\tb", `a\x09b`},
		{"null", "a\x00b", `a\x00b`},
		{"bell", "a\x07b", `a\x07b`},
		{"delete", "a\x7fb", `a\x7fb`},
		{"c1 control", "a\u009bb", `a\x9bb`},
		{"invalid utf8", "a\xffb", `a\xffb`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := safeCell(test.value); got != test.want {
				t.Fatalf("safeCell(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}

// Source: docs/adr/0006-redaction.md: the report layer is the only place that
// serializes what a remote server sent, so it is the layer that keeps a
// hostile response out of the operator's terminal. A compared header, a
// Location, a certificate subject, a cookie name or a probe error must not be
// able to write ANSI sequences or rewrite a line of the table.
func TestTableEscapesControlCharacters(t *testing.T) {
	t.Run("run table", func(t *testing.T) {
		var buffer bytes.Buffer
		if err := RunTable(&buffer, hostileRunReport(), Options{}); err != nil {
			t.Fatal(err)
		}
		table := buffer.String()
		assertNoControlBytes(t, "run table", table)
		for _, want := range wantEscapes {
			if !strings.Contains(table, want) {
				t.Fatalf("run table does not show the escape %s:\n%s", want, table)
			}
		}
	})

	t.Run("diff table", func(t *testing.T) {
		var buffer bytes.Buffer
		if err := Table(&buffer, hostileDifferences(), Options{}); err != nil {
			t.Fatal(err)
		}
		table := buffer.String()
		assertNoControlBytes(t, "diff table", table)
		for _, want := range wantEscapes {
			if !strings.Contains(table, want) {
				t.Fatalf("diff table does not show the escape %s:\n%s", want, table)
			}
		}
	})
}

// Source: docs/adr/0006-redaction.md: the JSON report relies on encoding/json,
// which escapes control characters on its own. The table escaping must not
// change the JSON output.
func TestJSONKeepsControlCharactersEscapedByEncodingJSON(t *testing.T) {
	var buffer bytes.Buffer
	if err := RunJSON(&buffer, hostileRunReport(), Options{}); err != nil {
		t.Fatal(err)
	}
	document := buffer.Bytes()
	var parsed map[string]any
	if err := json.Unmarshal(document, &parsed); err != nil {
		t.Fatalf("JSON report is not valid: %v\n%s", err, document)
	}
	if !bytes.Contains(document, []byte(`\u001b`)) {
		t.Fatalf("JSON report does not escape the ANSI sequence:\n%s", document)
	}
}
