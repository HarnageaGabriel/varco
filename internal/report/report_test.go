package report

import (
	"bytes"
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/compare"
	"github.com/HarnageaGabriel/varco/internal/diag"
)

var update = flag.Bool("update", false, "update golden files")

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
		t.Fatalf("%s mismatch:\n%s", name, got)
	}
}

// Source of each case: synthetic differences modelled on the VAR-CMP registry
// entries; "identical" covers exit code 0 and "differences" covers exit code 1.
func TestGolden(t *testing.T) {
	diffs := []compare.Difference{
		{Path: "/", Code: diag.CodeCompareStatus, Field: "status", Old: "200", New: "404"},
		{Path: "/oauth", Code: diag.CodeCompareLocation, Field: "header.location", Old: "/cb?code=old-secret", New: "/cb?code=new-secret"},
		{Path: "/a", Code: diag.CodeCompareHeader, Field: "header.x-frame-options", Old: "DENY", New: "(absent)"},
	}
	tests := []struct {
		name   string
		file   string
		render func(*bytes.Buffer) error
	}{
		{"table differences", "table_differences.golden", func(b *bytes.Buffer) error { return Table(b, diffs, Options{}) }},
		{"table identical", "table_identical.golden", func(b *bytes.Buffer) error { return Table(b, nil, Options{}) }},
		{"json differences", "json_differences.golden", func(b *bytes.Buffer) error { return JSON(b, diffs, Options{}) }},
		{"json identical", "json_identical.golden", func(b *bytes.Buffer) error { return JSON(b, nil, Options{}) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := test.render(&buf); err != nil {
				t.Fatal(err)
			}
			golden(t, test.file, buf.Bytes())
		})
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, os.ErrClosed }

func TestWriteErrors(t *testing.T) {
	d := []compare.Difference{{Path: "/"}}
	if Table(failWriter{}, nil, Options{}) == nil || Table(failWriter{}, d, Options{}) == nil || JSON(failWriter{}, d, Options{}) == nil {
		t.Fatal("expected write errors")
	}
}

func TestShowSensitive(t *testing.T) {
	differences := []compare.Difference{{Path: "/", Code: diag.CodeCompareLocation, Field: "header.location", Old: "/cb?code=old-secret", New: "/cb?code=new-secret"}}
	for _, render := range []func(io.Writer, []compare.Difference, Options) error{Table, JSON} {
		var output bytes.Buffer
		if err := render(&output, differences, Options{ShowSensitive: true}); err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(output.Bytes(), []byte("old-secret")) || !bytes.Contains(output.Bytes(), []byte("new-secret")) {
			t.Fatalf("sensitive output missing: %q", output.String())
		}
	}
}
