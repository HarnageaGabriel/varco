package policy_test

import (
	"testing"
	"time"

	"github.com/HarnageaGabriel/varco/internal/compare"
	"github.com/HarnageaGabriel/varco/internal/model"
	"github.com/HarnageaGabriel/varco/internal/policy"
)

// Source: CONVENTIONS.md "Fuzz every parser"; the exceptions file is a parser.
func FuzzLoad(f *testing.F) {
	f.Add(valid)
	f.Add("version: 1\nexceptions: []\n")
	f.Add("version: 1\nexceptions:\n  - id: a\n    reason: r\n    expires: 2026-01-01\n")
	f.Add("")
	f.Add("version: 1\nexceptions:\n  - id: a\n    reason: r\n    match:\n      path: \"/*\"\n")
	f.Fuzz(func(t *testing.T, data string) {
		file, err := policy.Load([]byte(data))
		if err != nil {
			return
		}
		if file.Version != policy.SchemaVersion {
			t.Fatalf("accepted version %d", file.Version)
		}
		now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		_ = file.ExpiredIDs(now)
		_ = policy.Evaluate(
			model.ProbeCase{ID: "c", Host: "h", Path: "/p", Family: "base", Scheme: "http", Method: "GET"},
			[]compare.Difference{{Field: "status", Old: "200", New: "404"}},
			file,
			now,
			model.Unstable{},
		)
	})
}
