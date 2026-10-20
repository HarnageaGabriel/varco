package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HarnageaGabriel/varco/internal/clock"
	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/model"
	"github.com/HarnageaGabriel/varco/internal/plan"
	"github.com/HarnageaGabriel/varco/internal/probe"
	"github.com/HarnageaGabriel/varco/internal/result"
)

// edgesContext returns a context bounded by a deadline, so a broken engine
// fails the test instead of hanging it. Source: CONVENTIONS.md determinism -
// a test never waits on an unbounded wait.
func edgesContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	timeout := 5 * time.Second
	if deadline, ok := t.Deadline(); ok {
		if remaining := time.Until(deadline) - time.Second; remaining < timeout {
			timeout = remaining
		}
	}
	if timeout <= 0 {
		timeout = time.Second
	}
	return context.WithTimeout(context.Background(), timeout)
}

// edgeConfig builds a run config for two numeric endpoints. Every probe in
// this file is a fake function, so no request ever leaves the process.
func edgeConfig(t *testing.T, cases []model.ProbeCase) RunConfig {
	t.Helper()
	return RunConfig{
		Cases: cases,
		Old: Endpoints{
			HTTP:  mustEndpoint(t, "10.0.0.1", 80),
			HTTPS: mustEndpoint(t, "10.0.0.1", 443),
		},
		New: Endpoints{
			HTTP:  mustEndpoint(t, "10.0.0.2", 80),
			HTTPS: mustEndpoint(t, "10.0.0.2", 443),
		},
		Rate:        MaxRate,
		Concurrency: 1,
		Repeat:      1,
		Now:         runNow,
	}
}

// edgeCase builds one plan case.
func edgeCase(id, host, path string) model.ProbeCase {
	return model.ProbeCase{ID: id, Host: host, Scheme: "http", Method: "GET", Path: path, Family: "base"}
}

// manualTicker is a rate-limit ticker the test drives by hand: a tick is only
// delivered when the test sends it, so the number of ticks a run waits for is
// observable and the run cannot proceed on its own.
type manualTicker struct {
	ticks    chan time.Time
	interval time.Duration
	sent     atomic.Int64
}

func (t *manualTicker) C() <-chan time.Time { return t.ticks }

func (t *manualTicker) Stop() {}

// manualClock injects the manual ticker and records the interval the engine
// asked for.
type manualClock struct{ ticker *manualTicker }

func (c manualClock) NewTicker(interval time.Duration) clock.Ticker {
	c.ticker.interval = interval
	return c.ticker
}

// tick delivers one rate-limit tick. It fails the test when the limiter is
// not waiting for a tick, which is how a run that skipped the rate limit is
// detected.
func (t *manualTicker) tick(tb testing.TB) {
	tb.Helper()
	select {
	case t.ticks <- time.Time{}:
		t.sent.Add(1)
	case <-time.After(2 * time.Second):
		tb.Fatal("the rate limiter did not wait for a tick")
	}
}

// settleGoroutines waits until the goroutine count stops changing, bounded by
// a deadline, and reports the settled count.
func settleGoroutines() int {
	previous := runtime.NumGoroutine()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			return runtime.NumGoroutine()
		default:
		}
		runtime.Gosched()
		current := runtime.NumGoroutine()
		if current == previous {
			return current
		}
		previous = current
	}
}

// cancelWhenProbed cancels the run as soon as the first probe starts and
// returns a channel that is closed when the helper goroutine is done, so a
// goroutine count never counts the helper itself.
func cancelWhenProbed(ctx context.Context, cancel context.CancelFunc, started <-chan struct{}) <-chan struct{} {
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		select {
		case <-started:
			cancel()
		case <-ctx.Done():
		}
	}()
	return finished
}

// Source: docs/run.md "Skipped cases (a cancelled run, or one dropped by
// --max-requests) are INCONCLUSIVE with the error cancelled or budget"; a run
// whose context is already cancelled probes nothing and skips every case.
func TestRunCancelledBeforeStartSkipsEveryCase(t *testing.T) {
	ctx, cancel := edgesContext(t)
	cancel()
	cases := []model.ProbeCase{edgeCase("a", "h", "/a"), edgeCase("b", "h", "/b"), edgeCase("c", "h", "/c")}
	config := edgeConfig(t, cases)
	config.Concurrency = 4
	var progressCalls atomic.Int32
	config.Progress = func(int, int, model.ProbeCase, model.Outcome) { progressCalls.Add(1) }
	report, err := Run(ctx, config, fakeClock{ticksOf(16)}, func(context.Context, probe.Request) (result.Result, error) {
		t.Error("no probe may run once the run was cancelled")
		return result.Result{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Coverage.Planned != 3 || report.Coverage.Executed != 0 || report.Coverage.Skipped != 3 {
		t.Fatalf("coverage = %+v", report.Coverage)
	}
	if report.Verdict != model.OutcomeInconclusive {
		t.Fatalf("verdict = %s", report.Verdict)
	}
	for _, item := range report.Cases {
		if item.Outcome != model.OutcomeInconclusive || item.Error != cancelledReason {
			t.Fatalf("case = %+v", item)
		}
	}
	if progressCalls.Load() != 0 {
		t.Fatalf("progress calls = %d, want 0", progressCalls.Load())
	}
}

// Source: docs/run.md "context cancellation stops scheduling and reports the
// remaining cases as skipped" (internal/engine/run.go:143-153). A run that is
// cancelled after two cases finished keeps those two cases and skips every
// later case; a skipped case is never probed.
func TestRunCancelAfterCompletedCasesStopsScheduling(t *testing.T) {
	ctx, cancel := edgesContext(t)
	defer cancel()
	cases := make([]model.ProbeCase, 5)
	for i := range cases {
		cases[i] = edgeCase(fmt.Sprintf("c%d", i), "h", fmt.Sprintf("/%d", i))
	}
	config := edgeConfig(t, cases)
	config.Concurrency = 1
	var probes atomic.Int32
	var mu sync.Mutex
	var progress []int
	config.Progress = func(done, total int, _ model.ProbeCase, _ model.Outcome) {
		mu.Lock()
		progress = append(progress, done)
		mu.Unlock()
		if total != len(cases) {
			t.Errorf("total = %d, want %d", total, len(cases))
		}
		if done == 2 {
			cancel()
		}
	}
	// Exactly three ticks: the two finished cases spend four requests, and the
	// first request never waits. The channel is empty afterwards, so a case
	// that is scheduled after the cancellation waits for a tick that never
	// comes and is skipped (internal/engine/run.go:231-233).
	report, err := Run(ctx, config, fakeClock{ticksOf(3)}, func(context.Context, probe.Request) (result.Result, error) {
		probes.Add(1)
		return result.Result{StatusCode: 200}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if probes.Load() != 4 {
		t.Fatalf("probes = %d, want 4 (two cases, two endpoints each)", probes.Load())
	}
	for i := 0; i < 2; i++ {
		if report.Cases[i].Outcome != model.OutcomeEquivalent {
			t.Fatalf("case %d = %+v", i, report.Cases[i])
		}
	}
	for i := 2; i < len(cases); i++ {
		if report.Cases[i].Outcome != model.OutcomeInconclusive || report.Cases[i].Error != cancelledReason {
			t.Fatalf("case %d = %+v", i, report.Cases[i])
		}
	}
	if report.Coverage.Planned != 5 || report.Coverage.Executed != 2 || report.Coverage.Skipped != 3 {
		t.Fatalf("coverage = %+v", report.Coverage)
	}
	if report.Verdict != model.OutcomeInconclusive {
		t.Fatalf("verdict = %s", report.Verdict)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(progress) != 2 || progress[0] != 1 || progress[1] != 2 {
		t.Fatalf("progress = %v, want [1 2]", progress)
	}
}

// Source: internal/engine/run.go:244-248; a probe that fails because the run
// was cancelled makes its case a skipped case, not a run error.
func TestRunCancelWhileProbeInFlightSkipsCase(t *testing.T) {
	ctx, cancel := edgesContext(t)
	defer cancel()
	cases := []model.ProbeCase{edgeCase("a", "h", "/a"), edgeCase("b", "h", "/b")}
	config := edgeConfig(t, cases)
	config.Concurrency = 1
	var progressCalls atomic.Int32
	config.Progress = func(int, int, model.ProbeCase, model.Outcome) { progressCalls.Add(1) }
	started := make(chan struct{})
	var once sync.Once
	finished := cancelWhenProbed(ctx, cancel, started)
	report, err := Run(ctx, config, fakeClock{ticksOf(0)}, func(probeCtx context.Context, _ probe.Request) (result.Result, error) {
		once.Do(func() { close(started) })
		<-probeCtx.Done()
		return result.Result{}, probeCtx.Err()
	})
	<-finished
	if err != nil {
		t.Fatal(err)
	}
	for i, item := range report.Cases {
		if item.Outcome != model.OutcomeInconclusive || item.Error != cancelledReason {
			t.Fatalf("case %d = %+v", i, item)
		}
	}
	if report.Coverage.Planned != 2 || report.Coverage.Executed != 0 || report.Coverage.Skipped != 2 {
		t.Fatalf("coverage = %+v", report.Coverage)
	}
	if progressCalls.Load() != 1 {
		t.Fatalf("progress calls = %d, want 1 (the cancelled case still completed)", progressCalls.Load())
	}
}

// Source: internal/engine/run.go:186-189; Run waits for every worker before
// it returns, so the progress callback can never run after Run returned.
func TestRunProgressCallbackNeverRunsAfterRunReturns(t *testing.T) {
	ctx, cancel := edgesContext(t)
	defer cancel()
	cases := make([]model.ProbeCase, 6)
	for i := range cases {
		cases[i] = edgeCase(fmt.Sprintf("c%d", i), "h", fmt.Sprintf("/%d", i))
	}
	config := edgeConfig(t, cases)
	config.Concurrency = 3
	var returned atomic.Bool
	var calls atomic.Int32
	config.Progress = func(done, total int, _ model.ProbeCase, _ model.Outcome) {
		if returned.Load() {
			t.Error("the progress callback ran after Run returned")
		}
		if total != len(cases) {
			t.Errorf("total = %d, want %d", total, len(cases))
		}
		if done < 1 || done > total {
			t.Errorf("done = %d, want 1..%d", done, total)
		}
		calls.Add(1)
	}
	report, err := Run(ctx, config, fakeClock{ticksOf(64)}, func(context.Context, probe.Request) (result.Result, error) {
		return result.Result{StatusCode: 200}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	returned.Store(true)
	if calls.Load() != int32(len(cases)) {
		t.Fatalf("progress calls = %d, want %d", calls.Load(), len(cases))
	}
	if report.Coverage.Executed != len(cases) {
		t.Fatalf("coverage = %+v", report.Coverage)
	}
}

// Source: docs/run.md; a cancelled run leaves no worker goroutine behind: the
// goroutine count settles back to the value it had before the run.
func TestRunCancelLeavesNoGoroutinesBehind(t *testing.T) {
	ctx, cancel := edgesContext(t)
	defer cancel()
	cases := make([]model.ProbeCase, 4)
	for i := range cases {
		cases[i] = edgeCase(fmt.Sprintf("c%d", i), "h", fmt.Sprintf("/%d", i))
	}
	config := edgeConfig(t, cases)
	config.Concurrency = 4
	started := make(chan struct{})
	var once sync.Once
	finished := cancelWhenProbed(ctx, cancel, started)
	// The garbage collector keeps its worker goroutines after the first run,
	// so the baseline is taken after one to keep the comparison stable.
	runtime.GC()
	before := settleGoroutines()
	report, err := Run(ctx, config, fakeClock{ticksOf(64)}, func(probeCtx context.Context, _ probe.Request) (result.Result, error) {
		once.Do(func() { close(started) })
		<-probeCtx.Done()
		return result.Result{}, probeCtx.Err()
	})
	<-finished
	if err != nil {
		t.Fatal(err)
	}
	after := settleGoroutines()
	if after > before {
		t.Fatalf("goroutines before = %d, after = %d", before, after)
	}
	if report.Coverage.Planned != 4 || report.Coverage.Executed != 0 || report.Coverage.Skipped != 4 {
		t.Fatalf("coverage = %+v", report.Coverage)
	}
}

// Source: docs/run.md --max-requests "stop after N requests in total; 0 (the
// default) means unlimited"; a case reserves two requests per repeat, one per
// endpoint (internal/engine/run.go:140).
func TestRunBudgetReservesTwoRequestsPerRepeat(t *testing.T) {
	for _, repeat := range []int{1, 2, 5} {
		t.Run(fmt.Sprintf("repeat %d", repeat), func(t *testing.T) {
			ctx, cancel := edgesContext(t)
			defer cancel()
			cases := make([]model.ProbeCase, 4)
			for i := range cases {
				cases[i] = edgeCase(fmt.Sprintf("c%d", i), "h", fmt.Sprintf("/%d", i))
			}
			config := edgeConfig(t, cases)
			config.Repeat = repeat
			// Three cases fit exactly: three times two requests per repeat.
			config.MaxRequests = 6 * repeat
			var probes atomic.Int32
			report, err := Run(ctx, config, fakeClock{ticksOf(8*repeat + 16)}, func(context.Context, probe.Request) (result.Result, error) {
				probes.Add(1)
				return result.Result{StatusCode: 200}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if probes.Load() != int32(6*repeat) {
				t.Fatalf("probes = %d, want %d", probes.Load(), 6*repeat)
			}
			if report.Coverage.Planned != 4 || report.Coverage.Executed != 3 || report.Coverage.Skipped != 1 {
				t.Fatalf("coverage = %+v", report.Coverage)
			}
			for i := 0; i < 3; i++ {
				if report.Cases[i].Outcome != model.OutcomeEquivalent {
					t.Fatalf("case %d = %+v", i, report.Cases[i])
				}
			}
			if report.Cases[3].Outcome != model.OutcomeInconclusive || report.Cases[3].Error != budgetReason {
				t.Fatalf("case = %+v", report.Cases[3])
			}
		})
	}
}

// Source: docs/run.md --max-requests; a budget that fits exactly one case
// runs that case and skips every later one.
func TestRunBudgetExactReservationRunsOneCase(t *testing.T) {
	for _, repeat := range []int{1, 2, 5} {
		t.Run(fmt.Sprintf("repeat %d", repeat), func(t *testing.T) {
			ctx, cancel := edgesContext(t)
			defer cancel()
			cases := []model.ProbeCase{edgeCase("a", "h", "/a"), edgeCase("b", "h", "/b")}
			config := edgeConfig(t, cases)
			config.Repeat = repeat
			config.MaxRequests = 2 * repeat
			var probes atomic.Int32
			report, err := Run(ctx, config, fakeClock{ticksOf(4*repeat + 8)}, func(context.Context, probe.Request) (result.Result, error) {
				probes.Add(1)
				return result.Result{StatusCode: 200}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if probes.Load() != int32(2*repeat) {
				t.Fatalf("probes = %d, want %d", probes.Load(), 2*repeat)
			}
			if report.Coverage.Executed != 1 || report.Coverage.Skipped != 1 {
				t.Fatalf("coverage = %+v", report.Coverage)
			}
			if report.Cases[1].Outcome != model.OutcomeInconclusive || report.Cases[1].Error != budgetReason {
				t.Fatalf("case = %+v", report.Cases[1])
			}
		})
	}
}

// Source: docs/run.md --max-requests; a budget of zero means unlimited.
func TestRunBudgetZeroMeansUnlimited(t *testing.T) {
	ctx, cancel := edgesContext(t)
	defer cancel()
	cases := []model.ProbeCase{edgeCase("a", "h", "/a"), edgeCase("b", "h", "/b"), edgeCase("c", "h", "/c")}
	config := edgeConfig(t, cases)
	config.MaxRequests = 0
	var probes atomic.Int32
	report, err := Run(ctx, config, fakeClock{ticksOf(16)}, func(context.Context, probe.Request) (result.Result, error) {
		probes.Add(1)
		return result.Result{StatusCode: 200}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if probes.Load() != 6 {
		t.Fatalf("probes = %d, want 6", probes.Load())
	}
	if report.Coverage.Executed != 3 || report.Coverage.Skipped != 0 {
		t.Fatalf("coverage = %+v", report.Coverage)
	}
}

// Source: docs/run.md --max-requests "that case and every later case are
// never probed"; the leftover budget of one request cannot cover the two
// requests the next case reserves, so the next case is skipped as well.
func TestRunBudgetSkipsLaterCaseThatDoesNotFit(t *testing.T) {
	ctx, cancel := edgesContext(t)
	defer cancel()
	cases := []model.ProbeCase{edgeCase("a", "h", "/a"), edgeCase("b", "h", "/b"), edgeCase("c", "h", "/c")}
	config := edgeConfig(t, cases)
	config.MaxRequests = 5
	var probes atomic.Int32
	report, err := Run(ctx, config, fakeClock{ticksOf(16)}, func(context.Context, probe.Request) (result.Result, error) {
		probes.Add(1)
		return result.Result{StatusCode: 200}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if probes.Load() != 4 {
		t.Fatalf("probes = %d, want 4", probes.Load())
	}
	if report.Coverage.Executed != 2 || report.Coverage.Skipped != 1 {
		t.Fatalf("coverage = %+v", report.Coverage)
	}
	if report.Cases[2].Outcome != model.OutcomeInconclusive || report.Cases[2].Error != budgetReason {
		t.Fatalf("case = %+v", report.Cases[2])
	}
}

// Source: docs/run.md --concurrency "cases probed in parallel. Default 4,
// allowed 1..64"; the number of probes in flight never exceeds the setting.
func TestRunNeverExceedsConcurrencyLimit(t *testing.T) {
	for _, concurrency := range []int{1, 4, 64} {
		t.Run(fmt.Sprintf("concurrency %d", concurrency), func(t *testing.T) {
			ctx, cancel := edgesContext(t)
			defer cancel()
			cases := make([]model.ProbeCase, concurrency)
			for i := range cases {
				cases[i] = edgeCase(fmt.Sprintf("c%d", i), "h", fmt.Sprintf("/%d", i))
			}
			config := edgeConfig(t, cases)
			config.Concurrency = concurrency
			var inFlight, peak atomic.Int32
			release := make(chan struct{})
			var once sync.Once
			report, err := Run(ctx, config, fakeClock{ticksOf(4*concurrency + 32)}, func(probeCtx context.Context, _ probe.Request) (result.Result, error) {
				current := inFlight.Add(1)
				for {
					observed := peak.Load()
					if current <= observed || peak.CompareAndSwap(observed, current) {
						break
					}
				}
				defer inFlight.Add(-1)
				if int(current) == concurrency {
					once.Do(func() { close(release) })
				}
				// The barrier makes the peak observable; the context keeps a
				// broken engine from hanging the test.
				select {
				case <-release:
				case <-probeCtx.Done():
					return result.Result{}, probeCtx.Err()
				}
				return result.Result{StatusCode: 200}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if peak.Load() > int32(concurrency) {
				t.Fatalf("peak in-flight probes = %d, want at most %d", peak.Load(), concurrency)
			}
			if peak.Load() != int32(concurrency) {
				t.Fatalf("peak in-flight probes = %d, want %d", peak.Load(), concurrency)
			}
			if report.Coverage.Executed != concurrency || report.Coverage.Skipped != 0 {
				t.Fatalf("coverage = %+v", report.Coverage)
			}
			for i, item := range report.Cases {
				if item.Outcome != model.OutcomeEquivalent {
					t.Fatalf("case %d = %+v", i, item)
				}
			}
		})
	}
}

// Source: internal/engine/run.go:85-89 and docs/run.md --concurrency "allowed
// 1..64"; an out-of-range concurrency is refused before any probe runs.
func TestRunRejectsInvalidConcurrency(t *testing.T) {
	for _, value := range []int{0, -1, 65} {
		want := fmt.Sprintf("concurrency %d is outside %d..%d", value, MinConcurrency, MaxConcurrency)
		if err := ValidateConcurrency(value); err == nil || err.Error() != want {
			t.Errorf("ValidateConcurrency(%d) = %v, want %q", value, err, want)
		}
	}
	ctx, cancel := edgesContext(t)
	defer cancel()
	for _, value := range []int{0, -1, 65} {
		config := edgeConfig(t, []model.ProbeCase{edgeCase("a", "h", "/a")})
		config.Concurrency = value
		if _, err := Run(ctx, config, fakeClock{ticksOf(4)}, func(context.Context, probe.Request) (result.Result, error) {
			t.Error("no probe may run with an invalid concurrency")
			return result.Result{}, nil
		}); err == nil {
			t.Fatalf("concurrency %d accepted", value)
		}
	}
}

// Source: internal/engine/run.go:93-98 and docs/run.md --repeat "probe each
// endpoint N times per case (1..5)"; an out-of-range repeat is refused, and a
// repeat of zero is normalized to one.
func TestRunRejectsInvalidRepeat(t *testing.T) {
	for _, value := range []int{0, -1, 6} {
		want := fmt.Sprintf("repeat %d is outside %d..%d", value, MinRepeat, MaxRepeat)
		if err := ValidateRepeat(value); err == nil || err.Error() != want {
			t.Errorf("ValidateRepeat(%d) = %v, want %q", value, err, want)
		}
	}
	ctx, cancel := edgesContext(t)
	defer cancel()
	config := edgeConfig(t, []model.ProbeCase{edgeCase("a", "h", "/a")})
	config.Repeat = 6
	if _, err := Run(ctx, config, fakeClock{ticksOf(4)}, func(context.Context, probe.Request) (result.Result, error) {
		t.Error("no probe may run with an invalid repeat")
		return result.Result{}, nil
	}); err == nil {
		t.Fatal("repeat 6 accepted")
	}
	floor := edgeConfig(t, []model.ProbeCase{edgeCase("a", "h", "/a")})
	floor.Repeat = 0
	var probes atomic.Int32
	report, err := Run(ctx, floor, fakeClock{ticksOf(8)}, func(context.Context, probe.Request) (result.Result, error) {
		probes.Add(1)
		return result.Result{StatusCode: 200}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Repeat != MinRepeat || probes.Load() != 2 {
		t.Fatalf("repeat = %d, probes = %d", report.Repeat, probes.Load())
	}
}

// Source: internal/engine/diff.go:28-33 and docs/run.md --rate; a rate that
// is not a usable request rate is refused before any probe runs.
func TestRunRejectsInvalidRate(t *testing.T) {
	for _, rate := range []float64{math.NaN(), 0, -1, MaxRate + 1, math.Inf(1)} {
		if err := ValidateRate(rate); err == nil {
			t.Errorf("rate %v accepted", rate)
		}
	}
	ctx, cancel := edgesContext(t)
	defer cancel()
	for _, rate := range []float64{math.NaN(), 0, MaxRate + 1} {
		config := edgeConfig(t, []model.ProbeCase{edgeCase("a", "h", "/a")})
		config.Rate = rate
		if _, err := Run(ctx, config, fakeClock{ticksOf(4)}, func(context.Context, probe.Request) (result.Result, error) {
			t.Error("no probe may run with an invalid rate")
			return result.Result{}, nil
		}); err == nil {
			t.Fatalf("rate %v accepted", rate)
		}
	}
}

// Source: docs/run.md --rate "maximum requests per second across both
// endpoints"; the first request is immediate and every later request waits
// for one tick of the limiter.
func TestRunRateLimiterWaitsOneTickPerRequestAfterTheFirst(t *testing.T) {
	ctx, cancel := edgesContext(t)
	defer cancel()
	ticker := &manualTicker{ticks: make(chan time.Time)}
	config := edgeConfig(t, []model.ProbeCase{edgeCase("a", "h", "/a")})
	config.Rate = 2
	probed := make(chan string, 4)
	results := make(chan model.RunReport, 1)
	failures := make(chan error, 1)
	go func() {
		report, err := Run(ctx, config, manualClock{ticker: ticker}, func(_ context.Context, request probe.Request) (result.Result, error) {
			probed <- request.Endpoint.Addr.String()
			return result.Result{StatusCode: 200}, nil
		})
		results <- report
		failures <- err
	}()
	if first := <-probed; first != "10.0.0.1" {
		t.Fatalf("first request went to %s, want the old endpoint", first)
	}
	// The second request may only run after the limiter released it.
	ticker.tick(t)
	if second := <-probed; second != "10.0.0.2" {
		t.Fatalf("second request went to %s, want the new endpoint", second)
	}
	report := <-results
	if err := <-failures; err != nil {
		t.Fatal(err)
	}
	if ticker.interval != 500*time.Millisecond {
		t.Fatalf("ticker interval = %v, want 500ms", ticker.interval)
	}
	if ticker.sent.Load() != 1 {
		t.Fatalf("ticks sent = %d, want 1", ticker.sent.Load())
	}
	if report.Coverage.Executed != 1 {
		t.Fatalf("coverage = %+v", report.Coverage)
	}
}

// Source: docs/run.md "one global rate limiter covers every request to either
// endpoint (repeats included)"; the limiter is shared, so the first request to
// the new endpoint waits for a tick too.
func TestRunRateLimiterIsGlobalAcrossBothEndpoints(t *testing.T) {
	ctx, cancel := edgesContext(t)
	defer cancel()
	ticker := &manualTicker{ticks: make(chan time.Time)}
	config := edgeConfig(t, []model.ProbeCase{edgeCase("a", "h", "/a"), edgeCase("b", "h", "/b")})
	config.Rate = 2
	probed := make(chan string, 8)
	results := make(chan model.RunReport, 1)
	failures := make(chan error, 1)
	go func() {
		report, err := Run(ctx, config, manualClock{ticker: ticker}, func(_ context.Context, request probe.Request) (result.Result, error) {
			probed <- request.Endpoint.Addr.String() + " " + request.Path
			return result.Result{StatusCode: 200}, nil
		})
		results <- report
		failures <- err
	}()
	want := []string{"10.0.0.1 /a", "10.0.0.2 /a", "10.0.0.1 /b", "10.0.0.2 /b"}
	for i := range want {
		got := <-probed
		if got != want[i] {
			t.Fatalf("request %d = %q, want %q", i, got, want[i])
		}
		if i < len(want)-1 {
			ticker.tick(t)
		}
	}
	report := <-results
	if err := <-failures; err != nil {
		t.Fatal(err)
	}
	if ticker.sent.Load() != 3 {
		t.Fatalf("ticks sent = %d, want 3 (one per request after the first)", ticker.sent.Load())
	}
	if report.Coverage.Executed != 2 {
		t.Fatalf("coverage = %+v", report.Coverage)
	}
}

// Source: internal/engine/run.go:231-233 and 432-435; a case that waits for a
// rate-limit tick when the run is cancelled is skipped, not an error.
func TestRunCancelWhileWaitingForRateTickSkipsCase(t *testing.T) {
	ctx, cancel := edgesContext(t)
	defer cancel()
	config := edgeConfig(t, []model.ProbeCase{edgeCase("a", "h", "/a")})
	started := make(chan struct{})
	var once sync.Once
	finished := cancelWhenProbed(ctx, cancel, started)
	report, err := Run(ctx, config, fakeClock{ticksOf(0)}, func(context.Context, probe.Request) (result.Result, error) {
		once.Do(func() { close(started) })
		return result.Result{StatusCode: 200}, nil
	})
	<-finished
	if err != nil {
		t.Fatal(err)
	}
	if report.Cases[0].Outcome != model.OutcomeInconclusive || report.Cases[0].Error != cancelledReason {
		t.Fatalf("case = %+v", report.Cases[0])
	}
	if report.Coverage.Planned != 1 || report.Coverage.Executed != 0 || report.Coverage.Skipped != 1 {
		t.Fatalf("coverage = %+v", report.Coverage)
	}
}

// Source: docs/run.md "A probe failure on either endpoint is INCONCLUSIVE
// with VAR-NET-0001"; the failure is confined to its case and the run
// continues with the next case.
func TestRunProbeErrorMakesOneCaseInconclusiveAndTheRunContinues(t *testing.T) {
	ctx, cancel := edgesContext(t)
	defer cancel()
	cases := []model.ProbeCase{edgeCase("a", "h", "/a"), edgeCase("b", "h", "/b"), edgeCase("c", "h", "/c")}
	config := edgeConfig(t, cases)
	config.Concurrency = 1
	report, err := Run(ctx, config, fakeClock{ticksOf(16)}, func(_ context.Context, request probe.Request) (result.Result, error) {
		if request.Path == "/b" && request.Endpoint.Addr.String() == "10.0.0.1" {
			return result.Result{}, errors.New("boom")
		}
		return result.Result{StatusCode: 200}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	failed := report.Cases[1]
	if failed.Outcome != model.OutcomeInconclusive {
		t.Fatalf("case = %+v", failed)
	}
	if !strings.Contains(failed.Error, string(diag.CodeEndpointUnreachable)) || !strings.Contains(failed.Error, "probe old") {
		t.Fatalf("error = %q", failed.Error)
	}
	if report.Cases[0].Outcome != model.OutcomeEquivalent || report.Cases[2].Outcome != model.OutcomeEquivalent {
		t.Fatalf("cases = %+v", report.Cases)
	}
	if report.Verdict != model.OutcomeInconclusive {
		t.Fatalf("verdict = %s", report.Verdict)
	}
}

// Source: internal/engine/run.go:249-252; a probe that answers with a
// diagnostic aborts the run and the report stays empty.
func TestRunDiagnosticProbeErrorAbortsTheRun(t *testing.T) {
	ctx, cancel := edgesContext(t)
	defer cancel()
	config := edgeConfig(t, []model.ProbeCase{edgeCase("a", "h", "/a"), edgeCase("b", "h", "/b")})
	var probes atomic.Int32
	report, err := Run(ctx, config, fakeClock{ticksOf(16)}, func(context.Context, probe.Request) (result.Result, error) {
		probes.Add(1)
		return result.Result{}, &diag.Error{Diagnostic: diag.Diagnostic{
			Code:     diag.CodeUnsupportedMethod,
			Severity: diag.SeverityError,
			Message:  "method POST requires --allow-unsafe-methods",
		}}
	})
	var diagnosticError *diag.Error
	if !errors.As(err, &diagnosticError) {
		t.Fatalf("error = %v, want a diagnostic error", err)
	}
	if probes.Load() == 0 {
		t.Fatal("no probe ran")
	}
	if report.Cases != nil || report.SchemaVersion != 0 || report.Verdict != "" {
		t.Fatalf("report = %+v, want the zero report", report)
	}
}

// Source: internal/engine/run.go:264; a probe that returns the zero result
// is compared like any other observation and never panics.
func TestRunZeroValueProbeResultIsHandled(t *testing.T) {
	ctx, cancel := edgesContext(t)
	defer cancel()
	config := edgeConfig(t, []model.ProbeCase{edgeCase("a", "h", "/a")})
	report, err := Run(ctx, config, fakeClock{ticksOf(8)}, func(context.Context, probe.Request) (result.Result, error) {
		return result.Result{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Cases[0].Outcome != model.OutcomeEquivalent {
		t.Fatalf("case = %+v", report.Cases[0])
	}
	if report.Coverage.Executed != 1 {
		t.Fatalf("coverage = %+v", report.Coverage)
	}
}

// Source: docs/run.md --dry-run "It honors --repeat, --family,
// --exclude-path and --max-requests"; the request list is deterministic and
// lists every case exactly as often as the repeat count asks for.
func TestDryRunIsDeterministicAndRepeatsEachCase(t *testing.T) {
	cases := []model.ProbeCase{edgeCase("a", "h", "/a"), edgeCase("b", "h", "/b")}
	config := edgeConfig(t, cases)
	config.Repeat = 3
	requests := DryRun(config)
	want := []string{
		"old http 10.0.0.1:80 GET h - /a",
		"old http 10.0.0.1:80 GET h - /a",
		"old http 10.0.0.1:80 GET h - /a",
		"new http 10.0.0.2:80 GET h - /a",
		"new http 10.0.0.2:80 GET h - /a",
		"new http 10.0.0.2:80 GET h - /a",
		"old http 10.0.0.1:80 GET h - /b",
		"old http 10.0.0.1:80 GET h - /b",
		"old http 10.0.0.1:80 GET h - /b",
		"new http 10.0.0.2:80 GET h - /b",
		"new http 10.0.0.2:80 GET h - /b",
		"new http 10.0.0.2:80 GET h - /b",
	}
	if len(requests) != len(want) {
		t.Fatalf("requests = %d, want %d", len(requests), len(want))
	}
	for i, request := range requests {
		if request.Line() != want[i] {
			t.Fatalf("request %d = %q, want %q", i, request.Line(), want[i])
		}
	}
	again := DryRun(config)
	if len(again) != len(requests) {
		t.Fatalf("second listing has %d requests, want %d", len(again), len(requests))
	}
	for i := range requests {
		if requests[i].Line() != again[i].Line() {
			t.Fatalf("request %d differs between listings: %q and %q", i, requests[i].Line(), again[i].Line())
		}
	}
	counts := map[string]int{}
	for _, request := range requests {
		counts[request.Side+" "+request.Path]++
	}
	if len(counts) != 4 {
		t.Fatalf("distinct case and side pairs = %d, want 4", len(counts))
	}
	for pair, count := range counts {
		if count != 3 {
			t.Fatalf("%s appears %d times, want 3", pair, count)
		}
	}
}

// Source: docs/run.md --dry-run; the listing stops before a case that would
// exceed the budget and a repeat of zero is raised to one.
func TestDryRunHonorsBudgetAndRepeatFloor(t *testing.T) {
	cases := []model.ProbeCase{edgeCase("a", "h", "/a"), edgeCase("b", "h", "/b"), edgeCase("c", "h", "/c")}
	config := edgeConfig(t, cases)
	config.MaxRequests = 5
	requests := DryRun(config)
	if len(requests) != 4 {
		t.Fatalf("requests = %d, want 4 (two cases, two endpoints each)", len(requests))
	}
	for _, request := range requests {
		if request.Path == "/c" {
			t.Fatalf("the budget-skipped case was listed: %+v", request)
		}
	}
	floor := edgeConfig(t, cases)
	floor.Repeat = 0
	requests = DryRun(floor)
	if len(requests) != 6 {
		t.Fatalf("requests = %d, want 6 (three cases, two endpoints each)", len(requests))
	}
}

// Source: docs/run.md --family "run only cases of the named families"; an
// empty family list keeps every case and a family that matches nothing keeps
// none.
func TestFilterCasesByFamily(t *testing.T) {
	cases := []model.ProbeCase{
		{ID: "a", Family: "base", Path: "/a"},
		{ID: "b", Family: "subpath", Path: "/b"},
		{ID: "c", Family: "base", Path: "/c"},
	}
	kept, excluded := FilterCases(cases, nil, nil)
	if len(kept) != 3 || excluded != 0 {
		t.Fatalf("kept = %d, excluded = %d, want 3 and 0", len(kept), excluded)
	}
	kept, excluded = FilterCases(cases, []string{"base"}, nil)
	if len(kept) != 2 || excluded != 1 || kept[0].ID != "a" || kept[1].ID != "c" {
		t.Fatalf("kept = %+v, excluded = %d", kept, excluded)
	}
	kept, excluded = FilterCases(cases, []string{"base", "subpath"}, nil)
	if len(kept) != 3 || excluded != 0 {
		t.Fatalf("kept = %d, excluded = %d, want 3 and 0", len(kept), excluded)
	}
	kept, excluded = FilterCases(cases, []string{"missing"}, nil)
	if len(kept) != 0 || excluded != 3 {
		t.Fatalf("kept = %d, excluded = %d, want 0 and 3", len(kept), excluded)
	}
}

// Source: docs/run.md --exclude-path "using path.Match semantics (* does not
// cross /, so use /api and /api/* to cover a route and its subpaths)"; a
// pattern that path.Match rejects matches nothing.
func TestFilterCasesByExcludePathGlob(t *testing.T) {
	cases := []model.ProbeCase{
		{ID: "a", Family: "base", Path: "/api"},
		{ID: "b", Family: "base", Path: "/api/v1"},
		{ID: "c", Family: "base", Path: "/health"},
	}
	kept, excluded := FilterCases(cases, nil, []string{"/api"})
	if len(kept) != 2 || excluded != 1 || kept[0].ID != "b" || kept[1].ID != "c" {
		t.Fatalf("kept = %+v, excluded = %d", kept, excluded)
	}
	kept, excluded = FilterCases(cases, nil, []string{"/api/*"})
	if len(kept) != 2 || excluded != 1 || kept[0].ID != "a" || kept[1].ID != "c" {
		t.Fatalf("kept = %+v, excluded = %d", kept, excluded)
	}
	// path.Match reports ErrBadPattern for "["; the engine ignores the error,
	// so the pattern excludes nothing. The command line refuses such a
	// pattern before the engine sees it (internal/cli/run.go:116-120).
	kept, excluded = FilterCases(cases, nil, []string{"["})
	if len(kept) != 3 || excluded != 0 {
		t.Fatalf("kept = %d, excluded = %d, want 3 and 0", len(kept), excluded)
	}
}

// Source: docs/run.md "Filtered-out cases are not planned into the run at
// all; the report counts them under excluded"; the family filter and the
// path filter both remove cases.
func TestFilterCasesAppliesFamilyAndPathTogether(t *testing.T) {
	cases := []model.ProbeCase{
		{ID: "a", Family: "base", Path: "/api"},
		{ID: "b", Family: "subpath", Path: "/api"},
		{ID: "c", Family: "base", Path: "/health"},
	}
	kept, excluded := FilterCases(cases, []string{"base"}, []string{"/health"})
	if len(kept) != 1 || excluded != 2 || kept[0].ID != "a" {
		t.Fatalf("kept = %+v, excluded = %d", kept, excluded)
	}
}

// Source: internal/engine/run.go:392-409 and docs/plan.md; cases are
// emitted in the plan order: host, path, scheme, method, family, id.
func TestRunOrdersCasesByMethodThenFamily(t *testing.T) {
	ctx, cancel := edgesContext(t)
	defer cancel()
	cases := []model.ProbeCase{
		{ID: "d", Host: "h", Scheme: "http", Method: "POST", Path: "/x", Family: "b"},
		{ID: "c", Host: "h", Scheme: "http", Method: "GET", Path: "/x", Family: "z"},
		{ID: "b", Host: "h", Scheme: "http", Method: "POST", Path: "/x", Family: "a"},
		{ID: "a", Host: "h", Scheme: "https", Method: "GET", Path: "/x", Family: "a"},
	}
	config := edgeConfig(t, cases)
	report, err := Run(ctx, config, fakeClock{ticksOf(32)}, func(context.Context, probe.Request) (result.Result, error) {
		return result.Result{StatusCode: 200}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"c", "b", "d", "a"}
	if len(report.Cases) != len(want) {
		t.Fatalf("cases = %d, want %d", len(report.Cases), len(want))
	}
	for i, id := range want {
		if report.Cases[i].CaseID != id {
			t.Fatalf("case %d = %s, want %s", i, report.Cases[i].CaseID, id)
		}
	}
}

// Source: internal/engine/run.go:371-379; the headers of a case are handed
// to the probe.
func TestRunSendsCaseHeadersToTheProbe(t *testing.T) {
	ctx, cancel := edgesContext(t)
	defer cancel()
	cases := []model.ProbeCase{edgeCase("a", "h", "/a")}
	cases[0].Headers = []plan.Header{{Name: "X-Probe", Values: []string{"1"}}}
	config := edgeConfig(t, cases)
	var mu sync.Mutex
	var seen []string
	report, err := Run(ctx, config, fakeClock{ticksOf(8)}, func(_ context.Context, request probe.Request) (result.Result, error) {
		mu.Lock()
		defer mu.Unlock()
		if len(request.Headers) == 0 {
			seen = append(seen, "(none)")
			return result.Result{StatusCode: 200}, nil
		}
		seen = append(seen, request.Headers[0].Name+"="+request.Headers[0].Values[0])
		return result.Result{StatusCode: 200}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || seen[0] != "X-Probe=1" || seen[1] != "X-Probe=1" {
		t.Fatalf("headers = %v", seen)
	}
	if report.Coverage.Executed != 1 {
		t.Fatalf("coverage = %+v", report.Coverage)
	}
}

// Source: docs/run.md "A field that takes different values across the N
// observations of the same endpoint is unstable"; the run reports the
// unstable (host, field) pairs de-duplicated and sorted.
func TestRunCollectsUnstableFieldsAcrossCases(t *testing.T) {
	ctx, cancel := edgesContext(t)
	defer cancel()
	specs := []struct {
		id, host, path, header string
	}{
		{"c1", "h1", "/alpha-1", "X-Frame-Options"},
		{"c2", "h1", "/beta-1", "Strict-Transport-Security"},
		{"c3", "h2", "/alpha-2", "X-Frame-Options"},
		{"c4", "h1", "/alpha-3", "X-Frame-Options"},
	}
	cases := make([]model.ProbeCase, len(specs))
	headers := map[string]string{}
	for i, spec := range specs {
		cases[i] = edgeCase(spec.id, spec.host, spec.path)
		headers[spec.path] = spec.header
	}
	config := edgeConfig(t, cases)
	config.Repeat = 2
	var mu sync.Mutex
	counts := map[string]int{}
	report, err := Run(ctx, config, fakeClock{ticksOf(256)}, func(_ context.Context, request probe.Request) (result.Result, error) {
		if request.Endpoint.Addr.String() != "10.0.0.1" {
			return result.Result{StatusCode: 200}, nil
		}
		mu.Lock()
		counts[request.Path]++
		count := counts[request.Path]
		mu.Unlock()
		name := headers[request.Path]
		return result.Result{StatusCode: 200, Headers: []result.Header{{Name: name, Values: []string{fmt.Sprintf("%s-%d", name, count)}}}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []model.UnstableField{
		{Host: "h1", Field: "header.strict-transport-security"},
		{Host: "h1", Field: "header.x-frame-options"},
		{Host: "h2", Field: "header.x-frame-options"},
	}
	if len(report.Unstable) != len(want) {
		t.Fatalf("unstable = %+v, want %+v", report.Unstable, want)
	}
	for i := range want {
		if report.Unstable[i] != want[i] {
			t.Fatalf("unstable[%d] = %+v, want %+v", i, report.Unstable[i], want[i])
		}
	}
	if report.Verdict != model.OutcomeEquivalent {
		t.Fatalf("verdict = %s", report.Verdict)
	}
}
