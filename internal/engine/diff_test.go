package engine

import (
	"context"
	"errors"
	"github.com/HarnageaGabriel/varco/internal/diag"
	"math"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/HarnageaGabriel/varco/internal/clock"
	"github.com/HarnageaGabriel/varco/internal/endpoint"
	"github.com/HarnageaGabriel/varco/internal/probe"
	"github.com/HarnageaGabriel/varco/internal/result"
)

type fakeTicker struct{ c chan time.Time }

func (f fakeTicker) C() <-chan time.Time { return f.c }
func (fakeTicker) Stop()                 {}

type fakeClock struct{ c chan time.Time }

func (f fakeClock) NewTicker(time.Duration) clock.Ticker { return fakeTicker{f.c} }

func config() DiffConfig {
	return DiffConfig{
		Old:    endpoint.Endpoint{Addr: netip.MustParseAddr("10.0.0.1"), Port: 443},
		New:    endpoint.Endpoint{Addr: netip.MustParseAddr("10.0.0.2"), Port: 443},
		Scheme: "https", Host: "h", SNI: "h", Method: "GET", Paths: []string{"/a", "/b"}, Rate: 10,
	}
}

func ticksOf(n int) chan time.Time {
	ticks := make(chan time.Time, n)
	for i := 0; i < n; i++ {
		ticks <- time.Time{}
	}
	return ticks
}

// Source: internal design (CONVENTIONS.md determinism): output must not depend on CLI path order.
func TestDiffSortsAndDedupesPaths(t *testing.T) {
	var calls []string
	fn := func(_ context.Context, r probe.Request) (result.Result, error) {
		calls = append(calls, r.Path)
		code := 200
		if r.Endpoint.Addr.String() == "10.0.0.2" {
			code = 404
		}
		return result.Result{StatusCode: code}, nil
	}
	cfg := config()
	cfg.Paths = []string{"/z", "/a", "/z", "/m", "/a"}
	outcome, err := Diff(context.Background(), cfg, fakeClock{ticksOf(20)}, fn)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, d := range outcome.Differences {
		paths = append(paths, d.Path)
	}
	if !slices.Equal(paths, []string{"/a", "/m", "/z"}) || len(calls) != 6 {
		t.Fatalf("paths=%v calls=%v", paths, calls)
	}
	if !slices.Equal(cfg.Paths, []string{"/z", "/a", "/z", "/m", "/a"}) {
		t.Fatal("input paths were mutated")
	}
}

// Source: review finding 4; time.NewTicker panics for non-positive intervals.
func TestDiffRejectsExtremeRates(t *testing.T) {
	for _, rate := range []float64{1e10, 1e-12, 0, -1, math.NaN(), math.Inf(1)} {
		cfg := config()
		cfg.Rate = rate
		if _, err := Diff(context.Background(), cfg, fakeClock{ticksOf(1)}, nil); err == nil {
			t.Errorf("rate %v accepted", rate)
		}
	}
	if err := ValidateRate(5); err != nil {
		t.Fatal(err)
	}
}

// Source: review minor finding: a failing probe is recorded per path and the run continues.
func TestDiffContinuesAfterProbeError(t *testing.T) {
	boom := errors.New("boom")
	var calls []string
	fn := func(_ context.Context, r probe.Request) (result.Result, error) {
		calls = append(calls, r.Endpoint.String()+r.Path)
		if r.Path == "/a" {
			return result.Result{}, boom
		}
		return result.Result{StatusCode: 200}, nil
	}
	outcome, err := Diff(context.Background(), config(), fakeClock{ticksOf(10)}, fn)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.Failures) != 1 || outcome.Failures[0].Path != "/a" || !errors.Is(outcome.Failures[0].Err, boom) || len(outcome.Differences) != 0 {
		t.Fatalf("outcome=%+v", outcome)
	}
	if len(calls) != 3 { // /a old fails (new skipped), /b old and new
		t.Fatalf("calls=%v", calls)
	}
}

func TestDiffAbortsOnRefusal(t *testing.T) {
	refuse := func(context.Context, probe.Request) (result.Result, error) {
		return result.Result{}, &diag.Error{Diagnostic: diag.Diagnostic{Code: diag.CodeUnsupportedMethod}}
	}
	if _, err := Diff(context.Background(), config(), fakeClock{ticksOf(10)}, refuse); err == nil {
		t.Fatal("expected refusal to abort")
	}
}

func TestDiffContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls int
	ok := func(context.Context, probe.Request) (result.Result, error) { calls++; return result.Result{}, nil }
	if _, err := Diff(ctx, config(), fakeClock{make(chan time.Time)}, ok); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

// Source: internal design; cancellation between probes stops the run.
func TestDiffCancelBetweenProbes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls int
	fn := func(context.Context, probe.Request) (result.Result, error) {
		calls++
		cancel()
		return result.Result{}, nil
	}
	if _, err := Diff(ctx, config(), fakeClock{ticksOf(10)}, fn); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestDiffCancellationWinsOverProbeFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	probeErr := errors.New("connection closed")
	fn := func(context.Context, probe.Request) (result.Result, error) {
		cancel()
		return result.Result{}, probeErr
	}
	outcome, err := Diff(ctx, config(), fakeClock{ticksOf(10)}, fn)
	if !errors.Is(err, context.Canceled) || len(outcome.Failures) != 0 || len(outcome.Differences) != 0 {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
}
