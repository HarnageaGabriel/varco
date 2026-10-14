// Package engine orchestrates varco operations across pure and I/O layers.
package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/HarnageaGabriel/varco/internal/clock"
	"github.com/HarnageaGabriel/varco/internal/compare"
	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/endpoint"
	"github.com/HarnageaGabriel/varco/internal/probe"
	"github.com/HarnageaGabriel/varco/internal/result"
)

// Supported request rates in requests per second. The bounds keep the ticker
// interval between 100us and 1000s.
const (
	MinRate = 0.001
	MaxRate = 10000
)

// ValidateRate reports whether rate is usable as a request rate.
func ValidateRate(rate float64) error {
	if !(rate >= MinRate && rate <= MaxRate) {
		return fmt.Errorf("rate %v is outside %v..%v requests per second", rate, MinRate, float64(MaxRate))
	}
	return nil
}

// ProbeFunc sends one probe.
type ProbeFunc func(context.Context, probe.Request) (result.Result, error)

// DiffConfig configures a comparison run.
type DiffConfig struct {
	Old, New                  endpoint.Endpoint
	Scheme, Host, SNI, Method string
	Headers                   []result.Header
	Paths                     []string
	Timeout                   time.Duration
	Rate                      float64
	IgnoreHeaders             []string
	AllowUnsafeMethod         bool
}

// Failure records a path that could not be compared because a probe failed.
type Failure struct {
	Path     string
	Endpoint string
	Err      error
}

// Outcome is the result of a comparison run.
type Outcome struct {
	Differences []compare.Difference
	Failures    []Failure
}

// Diff probes both endpoints for every path (sorted and de-duplicated) and
// returns sorted differences. A failing probe is recorded as a Failure and the
// run continues with the next path. Refusals (unsafe method, invalid request)
// and context cancellation abort the run with an error.
func Diff(ctx context.Context, config DiffConfig, clk clock.Clock, probeFn ProbeFunc) (Outcome, error) {
	if err := ValidateRate(config.Rate); err != nil {
		return Outcome{}, err
	}
	interval := time.Duration(float64(time.Second) / config.Rate)
	ticker := clk.NewTicker(interval)
	defer ticker.Stop()
	first := true
	wait := func() error {
		if first {
			first = false
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C():
			return nil
		}
	}
	paths := slices.Clone(config.Paths)
	sort.Strings(paths)
	paths = slices.Compact(paths)
	var outcome Outcome
	for _, path := range paths {
		results := make([]result.Result, 2)
		failed := false
		for i, target := range []endpoint.Endpoint{config.Old, config.New} {
			if err := ctx.Err(); err != nil {
				return Outcome{}, fmt.Errorf("diff: %w", err)
			}
			if err := wait(); err != nil {
				return Outcome{}, fmt.Errorf("rate limit: %w", err)
			}
			request := probe.Request{Endpoint: target, Scheme: config.Scheme, Host: config.Host, SNI: config.SNI, Method: config.Method, Path: path, Headers: config.Headers, Timeout: config.Timeout, AllowUnsafeMethod: config.AllowUnsafeMethod}
			captured, err := probeFn(ctx, request)
			if err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return Outcome{}, ctxErr
				}
				var diagnosticError *diag.Error
				if errors.As(err, &diagnosticError) {
					return Outcome{}, err
				}
				outcome.Failures = append(outcome.Failures, Failure{Path: path, Endpoint: target.String(), Err: err})
				failed = true
				break
			}
			results[i] = captured
		}
		if !failed {
			outcome.Differences = append(outcome.Differences, compare.Results(path, results[0], results[1], config.IgnoreHeaders)...)
		}
	}
	return outcome, nil
}
