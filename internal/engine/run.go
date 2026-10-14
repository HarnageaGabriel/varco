// Package engine orchestrates varco operations across pure and I/O layers.
package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/HarnageaGabriel/varco/internal/clock"
	"github.com/HarnageaGabriel/varco/internal/compare"
	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/endpoint"
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
)

// cancelledReason is the outcome error recorded for a skipped case.
const cancelledReason = "cancelled"

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
	for i, item := range cases {
		if ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
		case slots <- struct{}{}:
			scheduled[i] = true
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
				mu.Unlock()
				if err == nil && config.Progress != nil {
					config.Progress(completed, len(cases), item, evaluation.Outcome)
				}
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
		if !scheduled[i] {
			evaluations[i] = cancelledEvaluation(cases[i])
		}
		if evaluations[i].Error == cancelledReason {
			skipped++
		}
	}
	report := model.RunReport{
		SchemaVersion:     1,
		Coverage:          model.Summarize(evaluations, len(cases), skipped),
		UnsupportedCount:  len(config.Unsupported),
		Unsupported:       config.Unsupported,
		ExpiredExceptions: config.Exceptions.ExpiredIDs(config.Now),
		IgnoredFields:     policy.IgnoredFields(),
		Cases:             evaluations,
	}
	report.Verdict = model.Verdict(report)
	return report, nil
}

// runCase probes the old and new endpoints back to back for one case. A probe
// error makes the case INCONCLUSIVE; a cancellation of the run context makes it
// a skipped case.
func runCase(ctx context.Context, item model.ProbeCase, config RunConfig, limiter *rateLimiter, probeFn ProbeFunc) (model.Evaluation, error) {
	targets := []endpoint.Endpoint{config.Old.ForScheme(item.Scheme), config.New.ForScheme(item.Scheme)}
	labels := []string{model.EndpointOld, model.EndpointNew}
	observations := make([]model.Observation, len(targets))
	for i, target := range targets {
		if err := limiter.wait(ctx); err != nil {
			return cancelledEvaluation(item), nil
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
				return cancelledEvaluation(item), nil
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
			return model.Evaluation{
				CaseID:      item.ID,
				Outcome:     model.OutcomeInconclusive,
				Differences: []model.Difference{},
				Error:       fmt.Sprintf("%s: %s", diagnostic.Code, diagnostic.Message),
			}, nil
		}
		observations[i] = model.Observation{Endpoint: labels[i], Result: captured}
	}
	differences := compare.Results(item.Path, observations[0].Result, observations[1].Result, nil)
	return policy.Evaluate(item, differences, config.Exceptions, config.Now), nil
}

func cancelledEvaluation(item model.ProbeCase) model.Evaluation {
	return model.Evaluation{
		CaseID:      item.ID,
		Outcome:     model.OutcomeInconclusive,
		Differences: []model.Difference{},
		Error:       cancelledReason,
	}
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
