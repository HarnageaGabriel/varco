// Package engine orchestrates varco operations across pure and I/O layers.
package engine

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/HarnageaGabriel/varco/internal/clock"
	"github.com/HarnageaGabriel/varco/internal/compare"
	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/endpoint"
	"github.com/HarnageaGabriel/varco/internal/explain"
	"github.com/HarnageaGabriel/varco/internal/model"
	"github.com/HarnageaGabriel/varco/internal/plan"
	"github.com/HarnageaGabriel/varco/internal/policy"
	"github.com/HarnageaGabriel/varco/internal/probe"
	"github.com/HarnageaGabriel/varco/internal/result"
)

// Request bounds for a run.
const (
	// MinConcurrency and MaxConcurrency bound --concurrency.
	MinConcurrency = 1
	// MaxConcurrency is the largest number of in-flight cases.
	MaxConcurrency = 64
	// MinRepeat and MaxRepeat bound --repeat.
	MinRepeat = 1
	// MaxRepeat is the largest number of observations made of one endpoint.
	MaxRepeat = 5
)

// Case outcome errors that count as skipped rather than executed.
const (
	cancelledReason      = "cancelled"
	budgetReason         = "budget"
	unstableStatusReason = "unstable status"
)

// Endpoints holds the HTTP and HTTPS endpoints of one side. A case picks the
// endpoint that matches its scheme.
type Endpoints struct {
	HTTP  endpoint.Endpoint
	HTTPS endpoint.Endpoint
}

// ForScheme returns the endpoint a case with this scheme probes.
func (e Endpoints) ForScheme(scheme string) endpoint.Endpoint {
	if scheme == "http" {
		return e.HTTP
	}
	return e.HTTPS
}

// RunConfig configures one plan execution.
type RunConfig struct {
	Cases       []model.ProbeCase
	Old, New    Endpoints
	Timeout     time.Duration
	Rate        float64
	Concurrency int
	// Repeat is how many times each endpoint is probed per case (1..5).
	Repeat int
	// BodyPolicy selects when the response body is compared.
	BodyPolicy compare.BodyPolicy
	// MaxRequests caps the total requests a run sends; 0 means unlimited.
	MaxRequests int
	// Excluded counts the cases removed before the run by --exclude-path and
	// --family; the report lists it beside coverage.
	Excluded    int
	Exceptions  policy.File
	Unsupported []plan.Finding
	// Now is the injected clock used to expire exceptions (ADR 0007).
	Now time.Time
	// Progress, if set, is called as each case completes.
	Progress func(done, total int, item model.ProbeCase, outcome model.Outcome)
}

// ValidateConcurrency reports whether concurrency is usable.
func ValidateConcurrency(concurrency int) error {
	if concurrency < MinConcurrency || concurrency > MaxConcurrency {
		return fmt.Errorf("concurrency %d is outside %d..%d", concurrency, MinConcurrency, MaxConcurrency)
	}
	return nil
}

// ValidateRepeat reports whether repeat is usable.
func ValidateRepeat(repeat int) error {
	if repeat < MinRepeat || repeat > MaxRepeat {
		return fmt.Errorf("repeat %d is outside %d..%d", repeat, MinRepeat, MaxRepeat)
	}
	return nil
}

// normalizeBodyPolicy turns an unset policy into the 2xx default.
func normalizeBodyPolicy(bodyPolicy compare.BodyPolicy) compare.BodyPolicy {
	if bodyPolicy == "" {
		return compare.BodyPolicy2xx
	}
	return bodyPolicy
}

// Run executes every case against both endpoints and returns a versioned
// report. Cases are sorted by the plan's order key, so the report never depends
// on completion order. A probe failure makes its case INCONCLUSIVE (VAR-NET-0001)
// and the run continues; context cancellation stops scheduling and reports the
// remaining cases as skipped.
func Run(ctx context.Context, config RunConfig, clk clock.Clock, probeFn ProbeFunc) (model.RunReport, error) {
	if err := ValidateRate(config.Rate); err != nil {
		return model.RunReport{}, err
	}
	if err := ValidateConcurrency(config.Concurrency); err != nil {
		return model.RunReport{}, err
	}
	if config.Repeat == 0 {
		config.Repeat = MinRepeat
	}
	if err := ValidateRepeat(config.Repeat); err != nil {
		return model.RunReport{}, err
	}
	config.BodyPolicy = normalizeBodyPolicy(config.BodyPolicy)
	cases := sortedCases(config.Cases)
	limiter := newRateLimiter(clk.NewTicker(time.Duration(float64(time.Second) / config.Rate)))
	defer limiter.stop()

	evaluations := make([]model.Evaluation, len(cases))
	scheduled := make([]bool, len(cases))
	var (
		mu      sync.Mutex
		done    int
		wait    sync.WaitGroup
		aborted error
	)
	slots := make(chan struct{}, config.Concurrency)
	requestsPerCase := 2 * config.Repeat
	used := 0
	for i, item := range cases {
		if ctx.Err() != nil {
			break
		}
		if config.MaxRequests > 0 && used+requestsPerCase > config.MaxRequests {
			// The next case would exceed the budget: it and every later case are
			// skipped, never probed.
			for j := i; j < len(cases); j++ {
				evaluations[j] = budgetEvaluation(cases[j])
			}
			break
		}
		select {
		case <-ctx.Done():
		case slots <- struct{}{}:
			scheduled[i] = true
			used += requestsPerCase
			wait.Add(1)
			go func(i int, item model.ProbeCase) {
				defer wait.Done()
				defer func() { <-slots }()
				evaluation, err := runCase(ctx, item, config, limiter, probeFn)
				mu.Lock()
				if err != nil {
					if aborted == nil {
						aborted = err
					}
				} else {
					evaluations[i] = evaluation
				}
				done++
				completed := done
				// The progress callback runs under the mutex: workers must never
				// enter it at the same time, and the reported count must stay
				// monotone.
				if err == nil && config.Progress != nil {
					config.Progress(completed, len(cases), item, evaluation.Outcome)
				}
				mu.Unlock()
			}(i, item)
			continue
		}
		break
	}
	wait.Wait()
	if aborted != nil {
		return model.RunReport{}, aborted
	}

	// A case cancelled while in flight is skipped too: it was never observed on
	// both endpoints, so it must not count as executed.
	skipped := 0
	for i := range cases {
		if !scheduled[i] && evaluations[i].CaseID == "" {
			evaluations[i] = cancelledEvaluation(cases[i])
		}
		if evaluations[i].Error == cancelledReason || evaluations[i].Error == budgetReason {
			skipped++
		}
	}
	report := model.RunReport{
		SchemaVersion:     1,
		Coverage:          model.Summarize(evaluations, len(cases), skipped),
		Repeat:            config.Repeat,
		BodyPolicy:        string(config.BodyPolicy),
		Excluded:          config.Excluded,
		UnsupportedCount:  len(config.Unsupported),
		Unsupported:       config.Unsupported,
		ExpiredExceptions: config.Exceptions.ExpiredIDs(config.Now),
		IgnoredFields:     policy.IgnoredFields(),
		Cases:             evaluations,
		Unstable:          collectUnstable(evaluations),
	}
	report.Verdict = model.Verdict(report)
	return report, nil
}

// runCase probes the old and new endpoints back to back for one case, Repeat
// times each. A probe error makes the case INCONCLUSIVE; a cancellation of the
// run context makes it a skipped case. A field that differs between repeated
// observations of one endpoint is unstable, and an unstable status makes the
// case INCONCLUSIVE.
func runCase(ctx context.Context, item model.ProbeCase, config RunConfig, limiter *rateLimiter, probeFn ProbeFunc) (model.Evaluation, error) {
	targets := []endpoint.Endpoint{config.Old.ForScheme(item.Scheme), config.New.ForScheme(item.Scheme)}
	labels := []string{model.EndpointOld, model.EndpointNew}
	observations := make([][]result.Result, len(targets))
	for i, target := range targets {
		results := make([]result.Result, 0, config.Repeat)
		for range config.Repeat {
			if err := limiter.wait(ctx); err != nil {
				return cancelledEvaluation(item), nil //nolint:nilerr // cancellation is a skipped case, not a run error
			}
			request := probe.Request{
				Endpoint: target,
				Scheme:   item.Scheme,
				Host:     item.Host,
				SNI:      item.SNI,
				Method:   item.Method,
				Path:     item.Path,
				Headers:  probeHeaders(item.Headers),
				Timeout:  config.Timeout,
			}
			captured, err := probeFn(ctx, request)
			if err != nil {
				if ctx.Err() != nil {
					return cancelledEvaluation(item), nil //nolint:nilerr // cancellation is a skipped case, not a run error
				}
				var diagnosticError *diag.Error
				if errors.As(err, &diagnosticError) {
					return model.Evaluation{}, err
				}
				message := fmt.Sprintf("probe %s: %v", labels[i], err)
				diagnostic, diagErr := diag.New(diag.CodeEndpointUnreachable, message)
				if diagErr != nil {
					return model.Evaluation{}, diagErr
				}
				evaluation := baseEvaluation(item)
				evaluation.Outcome = model.OutcomeInconclusive
				evaluation.Differences = []model.Difference{}
				evaluation.Error = fmt.Sprintf("%s: %s", diagnostic.Code, diagnostic.Message)
				return evaluation, nil
			}
			results = append(results, captured)
		}
		observations[i] = results
	}
	unstable := model.Unstable{
		Old: unstableFields(item.Path, observations[0]),
		New: unstableFields(item.Path, observations[1]),
	}
	if slices.Contains(unstable.Old, "status") || slices.Contains(unstable.New, "status") {
		evaluation := baseEvaluation(item)
		evaluation.Outcome = model.OutcomeInconclusive
		evaluation.Differences = []model.Difference{}
		evaluation.Error = unstableStatusReason
		evaluation.Unstable = unstable
		return evaluation, nil
	}
	differences := compare.Results(item.Path, observations[0][0], observations[1][0], nil, config.BodyPolicy)
	evaluation := policy.Evaluate(item, differences, config.Exceptions, config.Now, unstable)
	evaluation.Unstable = unstable
	if evaluation.Outcome == model.OutcomeDifferent {
		evaluation.Explanations = explain.Explain(evaluation, evaluation.Family)
	}
	return evaluation, nil
}

// unstableFields lists the fields that differ between repeated observations of
// one endpoint, using the first observation as the reference. Body instability
// is detected regardless of the run's body policy.
func unstableFields(path string, results []result.Result) []string {
	if len(results) < 2 {
		return nil
	}
	seen := make(map[string]bool)
	var fields []string
	for _, other := range results[1:] {
		for _, difference := range compare.Results(path, results[0], other, nil, compare.BodyPolicyHash) {
			if !seen[difference.Field] {
				seen[difference.Field] = true
				fields = append(fields, difference.Field)
			}
		}
	}
	sort.Strings(fields)
	return fields
}

func baseEvaluation(item model.ProbeCase) model.Evaluation {
	return model.Evaluation{CaseID: item.ID, Host: item.Host, Method: item.Method, Path: item.Path, Family: item.Family}
}

func cancelledEvaluation(item model.ProbeCase) model.Evaluation {
	evaluation := baseEvaluation(item)
	evaluation.Outcome = model.OutcomeInconclusive
	evaluation.Differences = []model.Difference{}
	evaluation.Error = cancelledReason
	return evaluation
}

func budgetEvaluation(item model.ProbeCase) model.Evaluation {
	evaluation := baseEvaluation(item)
	evaluation.Outcome = model.OutcomeInconclusive
	evaluation.Differences = []model.Difference{}
	evaluation.Error = budgetReason
	return evaluation
}

// collectUnstable lists the (host, field) pairs found unstable across the run,
// sorted by host then field, de-duplicated.
func collectUnstable(evaluations []model.Evaluation) []model.UnstableField {
	type key struct{ host, field string }
	seen := make(map[key]bool)
	var unstable []model.UnstableField
	for _, evaluation := range evaluations {
		for _, field := range unionFields(evaluation.Unstable) {
			pair := key{evaluation.Host, field}
			if seen[pair] {
				continue
			}
			seen[pair] = true
			unstable = append(unstable, model.UnstableField{Host: evaluation.Host, Field: field})
		}
	}
	sort.Slice(unstable, func(i, j int) bool {
		if unstable[i].Host != unstable[j].Host {
			return unstable[i].Host < unstable[j].Host
		}
		return unstable[i].Field < unstable[j].Field
	})
	return unstable
}

func unionFields(unstable model.Unstable) []string {
	seen := make(map[string]bool, len(unstable.Old)+len(unstable.New))
	for _, field := range unstable.Old {
		seen[field] = true
	}
	for _, field := range unstable.New {
		seen[field] = true
	}
	fields := make([]string, 0, len(seen))
	for field := range seen {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

func probeHeaders(headers []plan.Header) []result.Header {
	if len(headers) == 0 {
		return nil
	}
	converted := make([]result.Header, 0, len(headers))
	for _, header := range headers {
		converted = append(converted, result.Header{Name: header.Name, Values: header.Values})
	}
	return converted
}

// sortedCases orders cases by the same key plan uses so a run is deterministic
// regardless of completion order. Source: internal/plan lessCase.
func sortedCases(cases []model.ProbeCase) []model.ProbeCase {
	ordered := append([]model.ProbeCase(nil), cases...)
	sort.SliceStable(ordered, func(i, j int) bool { return lessCase(ordered[i], ordered[j]) })
	return ordered
}

// lessCase orders cases by host, path, scheme, method, family, then ID, the
// order key documented for plans. Source: docs/plan.md.
func lessCase(a, b model.ProbeCase) bool {
	if a.Host != b.Host {
		return a.Host < b.Host
	}
	if a.Path != b.Path {
		return a.Path < b.Path
	}
	if a.Scheme != b.Scheme {
		return a.Scheme < b.Scheme
	}
	if a.Method != b.Method {
		return a.Method < b.Method
	}
	if a.Family != b.Family {
		return a.Family < b.Family
	}
	return a.ID < b.ID
}

// rateLimiter is one global limiter shared by both endpoints: every request
// consumes one token.
type rateLimiter struct {
	mu     sync.Mutex
	ticker clock.Ticker
	first  bool
}

func newRateLimiter(ticker clock.Ticker) *rateLimiter {
	return &rateLimiter{ticker: ticker, first: true}
}

func (l *rateLimiter) stop() { l.ticker.Stop() }

func (l *rateLimiter) wait(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.first {
		l.first = false
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-l.ticker.C():
		return nil
	}
}

// DryRequest is one request a run would send.
type DryRequest struct {
	Side     string
	Scheme   string
	Endpoint string
	Method   string
	Host     string
	SNI      string
	Path     string
}

// Line renders the request as the one-line dry-run report.
func (r DryRequest) Line() string {
	sni := r.SNI
	if sni == "" {
		sni = "-"
	}
	return fmt.Sprintf("%s %s %s %s %s %s %s", r.Side, r.Scheme, r.Endpoint, r.Method, r.Host, sni, r.Path)
}

// DryRun lists the requests a run with this config would send, in the order it
// would send them, honoring --max-requests. It never touches the network: it
// resolves each case's endpoint from the config and repeats it Repeat times per
// side.
func DryRun(config RunConfig) []DryRequest {
	repeat := config.Repeat
	if repeat < MinRepeat {
		repeat = MinRepeat
	}
	var requests []DryRequest
	used := 0
	requestsPerCase := 2 * repeat
	for _, item := range sortedCases(config.Cases) {
		if config.MaxRequests > 0 && used+requestsPerCase > config.MaxRequests {
			break
		}
		used += requestsPerCase
		for _, side := range []struct {
			label     string
			endpoints Endpoints
		}{
			{model.EndpointOld, config.Old},
			{model.EndpointNew, config.New},
		} {
			target := side.endpoints.ForScheme(item.Scheme)
			for range repeat {
				requests = append(requests, DryRequest{
					Side:     side.label,
					Scheme:   item.Scheme,
					Endpoint: target.String(),
					Method:   item.Method,
					Host:     item.Host,
					SNI:      item.SNI,
					Path:     item.Path,
				})
			}
		}
	}
	return requests
}

// FilterCases drops the cases a run must not plan: those whose family is not in
// families (when families is non-empty) and those whose request path matches an
// exclude-path glob. It returns the cases to run and how many were removed.
func FilterCases(cases []model.ProbeCase, families, excludePaths []string) ([]model.ProbeCase, int) {
	kept := make([]model.ProbeCase, 0, len(cases))
	for _, item := range cases {
		if len(families) != 0 && !slices.Contains(families, item.Family) {
			continue
		}
		if matchesAnyPath(excludePaths, item.Path) {
			continue
		}
		kept = append(kept, item)
	}
	return kept, len(cases) - len(kept)
}

func matchesAnyPath(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if ok, err := path.Match(pattern, value); err == nil && ok {
			return true
		}
	}
	return false
}
