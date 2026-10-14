// Package model defines the value types shared by plan execution, the policy
// layer and reporting.
//
// The package is pure: it performs no I/O, reads no clock, and keeps no
// package-level state. It records what was observed and what policy decided;
// the report layer is the only place that redacts (ADR 0006) and serializes.
//
// The outcome, verdict and exit-code split is recorded in
// docs/adr/0007-verdict-model.md.
package model

import (
	"slices"

	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/plan"
	"github.com/HarnageaGabriel/varco/internal/result"
)

// Endpoint labels name the two sides of a comparison.
const (
	// EndpointOld labels the currently serving endpoint.
	EndpointOld = "old"
	// EndpointNew labels the candidate endpoint.
	EndpointNew = "new"
)

// ProbeCase is one request to send to both endpoints. It is derived from a
// plan.Case; only the fields the probe needs are kept.
type ProbeCase struct {
	ID      string
	Host    string
	SNI     string
	Scheme  string
	Method  string
	Path    string
	Headers []plan.Header
	Family  string
}

// FromPlanCase converts a plan case into the shape the runner probes.
func FromPlanCase(item plan.Case) ProbeCase {
	return ProbeCase{
		ID:      item.ID,
		Host:    item.Host,
		SNI:     item.SNI,
		Scheme:  item.Scheme,
		Method:  item.Method,
		Path:    item.Path,
		Headers: item.Headers,
		Family:  item.Family,
	}
}

// FromPlanCases converts a whole plan, preserving order.
func FromPlanCases(cases []plan.Case) []ProbeCase {
	probeCases := make([]ProbeCase, 0, len(cases))
	for _, item := range cases {
		probeCases = append(probeCases, FromPlanCase(item))
	}
	return probeCases
}

// Observation is one endpoint's answer to a case, or the error that prevented
// one. Err is the raw probe error; the report layer redacts it.
type Observation struct {
	Endpoint string
	Result   result.Result
	Err      string
}

// Outcome is the per-case result of executing a case (ADR 0007).
type Outcome string

// The three per-case outcomes defined by ADR 0007.
const (
	// OutcomeEquivalent means every compared field matched after policy.
	OutcomeEquivalent Outcome = "EQUIVALENT"
	// OutcomeDifferent means at least one difference was not accepted by policy.
	OutcomeDifferent Outcome = "DIFFERENT"
	// OutcomeInconclusive means a case could not be observed or was skipped.
	OutcomeInconclusive Outcome = "INCONCLUSIVE"
)

// Difference is one observed mismatch and the policy decision about it. Old and
// New are the raw display values as produced by the comparator; the report
// layer redacts them per ADR 0006. An accepted difference stays in the report
// with its reason and exception id.
type Difference struct {
	Code        diag.Code `json:"code"`
	Field       string    `json:"field"`
	Old         string    `json:"old"`
	New         string    `json:"new"`
	Accepted    bool      `json:"accepted"`
	Reason      string    `json:"reason,omitempty"`
	ExceptionID string    `json:"exception_id,omitempty"`
}

// Evaluation is the complete result of one executed case.
type Evaluation struct {
	CaseID      string       `json:"case_id"`
	Outcome     Outcome      `json:"outcome"`
	Differences []Difference `json:"differences"`
	Error       string       `json:"error,omitempty"`

	// Host, Method, Path and Family identify the case in table output. The JSON
	// report keeps identifying a case by its id (the plan carries the request
	// shape), so these fields never reach JSON.
	Host   string `json:"-"`
	Method string `json:"-"`
	Path   string `json:"-"`
	Family string `json:"-"`

	// Unstable records the fields that differed between repeated observations of
	// one endpoint. It drives --suggest-exceptions and is not part of the JSON
	// contract.
	Unstable Unstable `json:"-"`
}

// Unstable names the fields that differed between repeated observations of the
// old and new endpoint.
type Unstable struct {
	Old []string `json:"-"`
	New []string `json:"-"`
}

// Side reports which endpoint's repeated observations made field unstable and
// the description to use in an acceptance reason.
func (u Unstable) Side(field string) (string, bool) {
	oldUnstable := slices.Contains(u.Old, field)
	newUnstable := slices.Contains(u.New, field)
	switch {
	case oldUnstable && newUnstable:
		return "old and new", true
	case oldUnstable:
		return "old", true
	case newUnstable:
		return "new", true
	}
	return "", false
}

// UnstableField is one (host, field) pair observed unstable across a run.
type UnstableField struct {
	Host  string `json:"-"`
	Field string `json:"-"`
}

// Coverage states how much of the plan a run observed (ADR 0007).
type Coverage struct {
	Planned      int `json:"planned"`
	Executed     int `json:"executed"`
	Equivalent   int `json:"equivalent"`
	Different    int `json:"different"`
	Inconclusive int `json:"inconclusive"`
	Skipped      int `json:"skipped"`
}

// RunReport is the complete, versioned result of `varco run`. Cases are sorted
// by the same key plan uses, so output never depends on completion order.
type RunReport struct {
	SchemaVersion     int            `json:"schema_version"`
	Verdict           Outcome        `json:"verdict"`
	Coverage          Coverage       `json:"coverage"`
	Repeat            int            `json:"repeat"`
	BodyPolicy        string         `json:"body_policy"`
	Excluded          int            `json:"excluded"`
	UnsupportedCount  int            `json:"unsupported_count"`
	Unsupported       []plan.Finding `json:"unsupported"`
	ExpiredExceptions []string       `json:"expired_exceptions"`
	IgnoredFields     []string       `json:"ignored_fields"`
	Cases             []Evaluation   `json:"cases"`
	Redacted          bool           `json:"redacted"`

	// Unstable lists the (host, field) pairs observed unstable, in stable order.
	// It drives --suggest-exceptions and is not part of the JSON contract.
	Unstable []UnstableField `json:"-"`
}

// Verdict derives the aggregate outcome from per-case outcomes (ADR 0007): a
// proven difference outweighs missing data, and missing data outweighs a clean
// run. It never hides either.
func Verdict(report RunReport) Outcome {
	verdict := OutcomeEquivalent
	for _, item := range report.Cases {
		switch item.Outcome {
		case OutcomeDifferent:
			return OutcomeDifferent
		case OutcomeInconclusive:
			verdict = OutcomeInconclusive
		case OutcomeEquivalent:
		}
	}
	return verdict
}

// Summarize counts the outcomes of a run.
func Summarize(cases []Evaluation, planned, skipped int) Coverage {
	coverage := Coverage{Planned: planned, Skipped: skipped, Executed: planned - skipped}
	for _, item := range cases {
		switch item.Outcome {
		case OutcomeEquivalent:
			coverage.Equivalent++
		case OutcomeDifferent:
			coverage.Different++
		case OutcomeInconclusive:
			coverage.Inconclusive++
		}
	}
	return coverage
}
