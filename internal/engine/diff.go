// Package engine orchestrates varco operations across pure and I/O layers.
package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/HarnageaGabriel/varco/internal/clock"
	"github.com/HarnageaGabriel/varco/internal/compare"
	"github.com/HarnageaGabriel/varco/internal/endpoint"
	"github.com/HarnageaGabriel/varco/internal/probe"
	"github.com/HarnageaGabriel/varco/internal/result"
)

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

// Diff probes both endpoints for every path and returns sorted differences.
func Diff(ctx context.Context, config DiffConfig, clk clock.Clock, probeFn ProbeFunc) ([]compare.Difference, error) {
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
	var differences []compare.Difference
	for _, path := range config.Paths {
		results := make([]result.Result, 2)
		for i, target := range []endpoint.Endpoint{config.Old, config.New} {
			if err := wait(); err != nil {
				return nil, fmt.Errorf("rate limit: %w", err)
			}
			request := probe.Request{Endpoint: target, Scheme: config.Scheme, Host: config.Host, SNI: config.SNI, Method: config.Method, Path: path, Headers: config.Headers, Timeout: config.Timeout, AllowUnsafeMethod: config.AllowUnsafeMethod}
			captured, err := probeFn(ctx, request)
			if err != nil {
				return nil, fmt.Errorf("probe %s for path %s: %w", target.String(), path, err)
			}
			results[i] = captured
		}
		differences = append(differences, compare.Results(path, results[0], results[1], config.IgnoreHeaders)...)
	}
	return differences, nil
}
