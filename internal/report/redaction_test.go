package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/compare"
	"github.com/HarnageaGabriel/varco/internal/diag"
)

// Source: docs/adr/0006-redaction.md; the Location header is the only
// sensitive field, and a report that hides it must still say whether the two
// values differ in their query, their fragment, or their userinfo.
func TestLocationDifferenceNotes(t *testing.T) {
	tests := []struct {
		name     string
		old      string
		new      string
		wantNote string
	}{
		{
			name:     "query values differ",
			old:      "https://api.example.com/cb?code=old-secret",
			new:      "https://api.example.com/cb?code=new-secret",
			wantNote: "(query values differ)",
		},
		{
			name:     "fragments differ",
			old:      "https://api.example.com/cb#old-secret",
			new:      "https://api.example.com/cb#new-secret",
			wantNote: "(fragments differ)",
		},
		{
			name:     "userinfo differs",
			old:      "https://old-user:secret@api.example.com/cb",
			new:      "https://new-user:secret@api.example.com/cb",
			wantNote: "(userinfo differs)",
		},
		{
			name:     "empty fragment differs",
			old:      "https://api.example.com/cb#",
			new:      "https://api.example.com/cb",
			wantNote: "(sensitive values differ)",
		},
		{
			name:     "unparsable location differs",
			old:      "https://api.example.com/cb\x7f",
			new:      "https://api.example.com/cb\x01",
			wantNote: "(sensitive values differ)",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			differences := []compare.Difference{{
				Path:  "/oauth",
				Code:  diag.CodeCompareLocation,
				Field: "header.location",
				Old:   test.old,
				New:   test.new,
			}}
			var buffer bytes.Buffer
			if err := Table(&buffer, differences, Options{}); err != nil {
				t.Fatal(err)
			}
			output := buffer.String()
			if !strings.Contains(output, test.wantNote) {
				t.Fatalf("table = %q, want the note %q", output, test.wantNote)
			}
			if strings.Contains(output, "secret") {
				t.Fatalf("table = %q, want no raw sensitive value", output)
			}
		})
	}
}

// Source: docs/adr/0006-redaction.md; a Location value that is identical on
// both sides is not a difference, so no note is added.
func TestLocationDifferenceWithoutNote(t *testing.T) {
	differences := []compare.Difference{{
		Path:  "/oauth",
		Code:  diag.CodeCompareLocation,
		Field: "header.location",
		Old:   "https://api.example.com/cb?code=same-secret",
		New:   "https://api.example.com/cb?code=same-secret",
	}}
	var buffer bytes.Buffer
	if err := Table(&buffer, differences, Options{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buffer.String(), "differ)") {
		t.Fatalf("table = %q, want no difference note", buffer.String())
	}
}
