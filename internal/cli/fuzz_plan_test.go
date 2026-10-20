package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/HarnageaGabriel/varco/internal/plan"
)

// FuzzLoadPlan feeds arbitrary bytes to the plan loader used by
// `varco run --plan` and checks the promises that loader makes:
//
//   - it never panics, whatever the document contains;
//   - it always returns within a deadline;
//   - its memory use stays bounded by the size of the document;
//   - a document it accepts is a document its own validation accepts again.
//
// Sources cited per invariant:
//   - internal/cli/run.go:261-291 (loadRunPlan: read, decode, schema_version)
//   - internal/cli/run.go:293-299 (readRunInput: the plan file is read as-is)
//   - internal/cli/cli.go:218-222 (configFailure: every rejection is reported)
//   - testdata/e2e/plan-identical.json (a plan document varco produced)
func FuzzLoadPlan(f *testing.F) {
	for _, seed := range fuzzPlanSeeds {
		f.Add([]byte(seed))
	}

	directory := f.TempDir()
	planPath := filepath.Join(directory, "plan.json")
	roundTripPath := filepath.Join(directory, "round-trip.json")

	f.Fuzz(func(t *testing.T, data []byte) {
		if err := os.WriteFile(planPath, data, 0o600); err != nil {
			t.Fatal(err)
		}

		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		loaded := loadPlanWithinDeadline(t, planPath)
		runtime.ReadMemStats(&after)
		if used := after.TotalAlloc - before.TotalAlloc; used > planMemoryBudget(len(data)) {
			t.Fatalf("plan loader allocated %d bytes for a %d-byte document", used, len(data))
		}

		if loaded.code != 0 {
			// A rejected document is reported, never swallowed.
			if loaded.stderr == "" {
				t.Fatalf("rejected plan without a diagnostic: %q", string(data))
			}
			return
		}

		// The loader accepts a document only when it decodes and declares
		// schema_version 1. The same rules must hold for the test.
		var document planEnvelope
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatalf("loader accepted a document the decoder rejects: %v", err)
		}
		if document.SchemaVersion != 1 {
			t.Fatalf("loader accepted schema_version %d, want 1", document.SchemaVersion)
		}
		want := plan.Plan{
			Profile:     document.Profile,
			Cases:       document.Cases,
			Unsupported: document.Unsupported,
			Warnings:    document.Warnings,
		}
		if !reflect.DeepEqual(loaded.plan, want) {
			t.Fatalf("loaded plan = %#v, want %#v", loaded.plan, want)
		}

		// The plan the loader produced must pass the loader's own validation
		// when it is handed back to it.
		encoded, err := json.Marshal(planEnvelope{
			SchemaVersion: 1,
			Profile:       want.Profile,
			Cases:         want.Cases,
			Unsupported:   want.Unsupported,
			Warnings:      want.Warnings,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(roundTripPath, encoded, 0o600); err != nil {
			t.Fatal(err)
		}
		again := loadPlanWithinDeadline(t, roundTripPath)
		if again.code != 0 {
			t.Fatalf("the loader rejected the plan it produced: %s", again.stderr)
		}
		if !reflect.DeepEqual(again.plan, loaded.plan) {
			t.Fatalf("round trip changed the plan: %#v vs %#v", again.plan, loaded.plan)
		}
	})
}

// planEnvelope mirrors the plan document the run loader reads
// (internal/cli/run.go:267).
type planEnvelope struct {
	SchemaVersion int            `json:"schema_version"`
	Profile       string         `json:"profile"`
	Cases         []plan.Case    `json:"cases"`
	Unsupported   []plan.Finding `json:"unsupported"`
	Warnings      []plan.Warning `json:"warnings"`
}

// planLoadResult is the outcome of one plan load.
type planLoadResult struct {
	plan   plan.Plan
	code   int
	stderr string
}

// loadPlanWithinDeadline loads the plan document at path and fails the test
// when the loader does not return within the deadline.
func loadPlanWithinDeadline(t *testing.T, path string) planLoadResult {
	t.Helper()
	results := make(chan planLoadResult, 1)
	go func() {
		var stderr bytes.Buffer
		loaded, code := loadRunPlan(path, nil, nil, plan.Options{}, &stderr)
		results <- planLoadResult{plan: loaded, code: code, stderr: stderr.String()}
	}()
	select {
	case result := <-results:
		return result
	case <-time.After(planLoadDeadline):
		t.Fatalf("plan loader did not return within %s", planLoadDeadline)
		return planLoadResult{}
	}
}

// planMemoryBudget is the memory a plan load may use for a document of the
// given size: a fixed head room plus a linear multiple of the document. A
// loader that grows faster than the document fails the budget.
func planMemoryBudget(size int) uint64 {
	return planMemoryHeadroom + uint64(size)*planMemoryFactor
}

const (
	// planLoadDeadline bounds one plan load.
	planLoadDeadline = 5 * time.Second
	// planMemoryHeadroom is the memory a plan load may use on top of the
	// document itself.
	planMemoryHeadroom = 32 << 20
	// planMemoryFactor is the multiple of the document size a plan load may
	// allocate.
	planMemoryFactor = 64
)

// fuzzPlanSeeds are the hand-written seeds of the plan loader. The same
// documents are committed under testdata/fuzz/FuzzLoadPlan.
var fuzzPlanSeeds = []string{
	// A plan varco produced (testdata/e2e/plan-identical.json).
	`{"schema_version": 1, "profile": "nginx", "cases": [{"id": "aaaaaaaaaaaa", "host": "app.example.com", "scheme": "http", "method": "GET", "path": "/", "family": "base", "source": {"ingress": "e2e/spec", "rule": 0, "path": 0}}], "unsupported": [], "warnings": []}`,
	// A plan without cases (testdata/plan/hostless.golden.json).
	`{"schema_version": 1, "profile": "nginx", "cases": [], "unsupported": [{"code": "VAR-PLAN-0005", "ingress": "dev/internal", "message": "rule has no host"}], "warnings": []}`,
	// A truncated document.
	`{"schema_version": 1, "profile": "nginx", "cases": [{"id": "a", "host": "h"`,
	// A document followed by trailing garbage.
	`{"schema_version": 1, "cases": []} trailing`,
	// Deeply nested arrays, past the depth the JSON decoder accepts.
	`{"schema_version": 1, "cases": ` + strings.Repeat("[", 10001) + strings.Repeat("]", 10001) + `}`,
	// A number too large for an int.
	`{"schema_version": 99999999999999999999, "cases": []}`,
	// The same key twice, with different values.
	`{"schema_version": 1, "schema_version": 2, "cases": []}`,
	// A field of the wrong type.
	`{"schema_version": 1, "cases": "not-a-list"}`,
	// A NUL byte inside a string.
	`{"schema_version": 1, "cases": [{"id": "a\u0000b", "host": "h", "scheme": "http", "method": "GET", "path": "/"}]}`,
	// A very long path.
	`{"schema_version": 1, "cases": [{"id": "a", "host": "h", "scheme": "http", "method": "GET", "path": "/` + strings.Repeat("a", 16384) + `"}]}`,
	// An unsupported schema version.
	`{"schema_version": 2, "cases": []}`,
	// A document that is not JSON at all.
	"not json at all",
	// An empty plan document.
	"",
}
