package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HarnageaGabriel/varco/internal/clock"
	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/endpoint"
	"github.com/HarnageaGabriel/varco/internal/model"
	"github.com/HarnageaGabriel/varco/internal/policy"
	"github.com/HarnageaGabriel/varco/internal/probe"
	"github.com/HarnageaGabriel/varco/internal/result"
)

var runNow = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

func mustEndpoint(t *testing.T, value string, port uint16) endpoint.Endpoint {
	t.Helper()
	parsed, err := endpoint.Parse(value, port)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func httpServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

// caseFor builds a single-case run config against two live HTTP endpoints.
func httpConfig(t *testing.T, oldServer, newServer *httptest.Server) RunConfig {
	t.Helper()
	return RunConfig{
		Cases:   []model.ProbeCase{{ID: "c1", Host: "api.example.com", Scheme: "http", Method: "GET", Path: "/", Family: "base"}},
		Old:     Endpoints{HTTP: mustEndpoint(t, oldServer.Listener.Addr().String(), 80)},
		New:     Endpoints{HTTP: mustEndpoint(t, newServer.Listener.Addr().String(), 80)},
		Timeout: 5 * time.Second, Rate: 10000, Concurrency: 1, Now: runNow,
	}
}

// Source: docs/adr/0007-verdict-model.md; two identical endpoints are EQUIVALENT.
func TestRunEquivalentHTTP(t *testing.T) {
	oldServer, newServer := httpServer(t, 200, "same"), httpServer(t, 200, "same")
	report, err := Run(context.Background(), httpConfig(t, oldServer, newServer), fakeClock{ticksOf(64)}, probe.Do)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != model.OutcomeEquivalent || report.Coverage.Equivalent != 1 || report.Coverage.Planned != 1 {
		t.Fatalf("report = %+v", report)
	}
	if report.SchemaVersion != 1 || len(report.Cases) != 1 || report.Cases[0].Outcome != model.OutcomeEquivalent {
		t.Fatalf("cases = %+v", report.Cases)
	}
	if len(report.IgnoredFields) == 0 {
		t.Fatal("ignored fields not reported")
	}
}

// Source: docs/adr/0007-verdict-model.md; a status difference is DIFFERENT.
func TestRunDifferentHTTP(t *testing.T) {
	oldServer, newServer := httpServer(t, 200, "same"), httpServer(t, 404, "same")
	report, err := Run(context.Background(), httpConfig(t, oldServer, newServer), fakeClock{ticksOf(64)}, probe.Do)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != model.OutcomeDifferent || report.Coverage.Different != 1 {
		t.Fatalf("verdict=%s coverage=%+v", report.Verdict, report.Coverage)
	}
	differences := report.Cases[0].Differences
	if len(differences) == 0 || differences[0].Code != diag.CodeCompareStatus || differences[0].Accepted {
		t.Fatalf("differences = %+v", differences)
	}
}

// Source: docs/adr/0007-verdict-model.md; policy accepts a difference and keeps it visible.
func TestRunAcceptedByException(t *testing.T) {
	oldServer, newServer := httpServer(t, 200, "same"), httpServer(t, 404, "same")
	file, err := policy.Load([]byte("version: 1\nexceptions:\n  - id: status-ok\n    reason: \"known 404\"\n    match:\n      field: status\n"))
	if err != nil {
		t.Fatal(err)
	}
	config := httpConfig(t, oldServer, newServer)
	config.Exceptions = file
	report, err := Run(context.Background(), config, fakeClock{ticksOf(64)}, probe.Do)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != model.OutcomeEquivalent || report.Coverage.Equivalent != 1 {
		t.Fatalf("report = %+v", report)
	}
	difference := report.Cases[0].Differences[0]
	if !difference.Accepted || difference.ExceptionID != "status-ok" || difference.Reason != "known 404" {
		t.Fatalf("difference = %+v", difference)
	}
}

// Source: docs/run.md; an expired exception is ignored and listed.
func TestRunExpiredException(t *testing.T) {
	oldServer, newServer := httpServer(t, 200, "same"), httpServer(t, 404, "same")
	file, err := policy.Load([]byte("version: 1\nexceptions:\n  - id: status-ok\n    reason: \"known 404\"\n    expires: 2026-01-01\n    match:\n      field: status\n"))
	if err != nil {
		t.Fatal(err)
	}
	config := httpConfig(t, oldServer, newServer)
	config.Exceptions = file
	report, err := Run(context.Background(), config, fakeClock{ticksOf(64)}, probe.Do)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != model.OutcomeDifferent {
		t.Fatalf("verdict = %s", report.Verdict)
	}
	if len(report.ExpiredExceptions) != 1 || report.ExpiredExceptions[0] != "status-ok" {
		t.Fatalf("expired = %v", report.ExpiredExceptions)
	}
	if report.Cases[0].Differences[0].Accepted {
		t.Fatal("expired exception accepted a difference")
	}
}

// Source: docs/adr/0007-verdict-model.md; a probe failure is INCONCLUSIVE with VAR-NET-0001.
func TestRunProbeFailureInconclusive(t *testing.T) {
	oldServer := httpServer(t, 200, "ok")
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := closed.Listener.Addr().String()
	closed.Close()

	config := httpConfig(t, oldServer, oldServer)
	config.New = Endpoints{HTTP: mustEndpoint(t, address, 80)}
	report, err := Run(context.Background(), config, fakeClock{ticksOf(64)}, probe.Do)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != model.OutcomeInconclusive || report.Coverage.Inconclusive != 1 || report.Coverage.Executed != 1 {
		t.Fatalf("report = %+v", report)
	}
	evaluation := report.Cases[0]
	if evaluation.Outcome != model.OutcomeInconclusive || !strings.Contains(evaluation.Error, string(diag.CodeEndpointUnreachable)) {
		t.Fatalf("evaluation = %+v", evaluation)
	}
}

// Source: docs/adr/0007-verdict-model.md; skipped cases are reported, never hidden.
func TestRunCancellationSkipsRemaining(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cases := make([]model.ProbeCase, 3)
	for i := range cases {
		cases[i] = model.ProbeCase{ID: string(rune('a' + i)), Host: "h", Scheme: "http", Method: "GET", Path: "/"}
	}
	config := RunConfig{Cases: cases, Old: Endpoints{HTTP: mustEndpoint(t, "127.0.0.1:1", 80)}, New: Endpoints{HTTP: mustEndpoint(t, "127.0.0.1:2", 80)}, Rate: 10000, Concurrency: 4, Now: runNow}
	report, err := Run(ctx, config, fakeClock{ticksOf(64)}, func(context.Context, probe.Request) (result.Result, error) {
		t.Fatal("no probe should run after cancellation")
		return result.Result{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Coverage.Skipped != 3 || report.Coverage.Executed != 0 || report.Verdict != model.OutcomeInconclusive {
		t.Fatalf("coverage = %+v", report.Coverage)
	}
	for _, evaluation := range report.Cases {
		if evaluation.Error != "cancelled" || evaluation.Outcome != model.OutcomeInconclusive {
			t.Fatalf("evaluation = %+v", evaluation)
		}
	}
}

// Source: CONVENTIONS.md determinism; plan order and JSON must not depend on
// completion order, even at concurrency 8.
func TestRunDeterministicUnderConcurrency(t *testing.T) {
	cases := []model.ProbeCase{
		{ID: "c", Host: "b.example", Path: "/z", Scheme: "http", Method: "GET", Family: "base"},
		{ID: "a", Host: "a.example", Path: "/x", Scheme: "http", Method: "GET", Family: "base"},
		{ID: "b", Host: "a.example", Path: "/x", Scheme: "http", Method: "HEAD", Family: "base"},
	}
	probeFn := func(_ context.Context, request probe.Request) (result.Result, error) {
		status := 200
		if request.Endpoint.Addr.String() == "10.0.0.2" {
			status = 404
		}
		return result.Result{StatusCode: status, Headers: []result.Header{{Name: "Content-Type", Values: []string{"text/plain"}}}}, nil
	}
	config := RunConfig{Cases: cases, Old: Endpoints{HTTP: mustEndpoint(t, "10.0.0.1", 80)}, New: Endpoints{HTTP: mustEndpoint(t, "10.0.0.2", 80)}, Rate: 10000, Concurrency: 8, Now: runNow}
	var first string
	for run := 0; run < 5; run++ {
		report, err := Run(context.Background(), config, fakeClock{ticksOf(256)}, probeFn)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		if run == 0 {
			first = string(encoded)
			continue
		}
		if string(encoded) != first {
			t.Fatalf("run %d differs:\n%s\n%s", run, first, encoded)
		}
	}
	report, err := Run(context.Background(), config, fakeClock{ticksOf(256)}, probeFn)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{report.Cases[0].CaseID, report.Cases[1].CaseID, report.Cases[2].CaseID}
	if strings.Join(ids, ",") != "a,b,c" {
		t.Fatalf("cases not in plan order: %v", ids)
	}
	if report.Verdict != model.OutcomeDifferent {
		t.Fatalf("verdict = %s", report.Verdict)
	}
}

type countingTicker struct {
	count *atomic.Int64
	ticks chan time.Time
}

func (t *countingTicker) C() <-chan time.Time { t.count.Add(1); return t.ticks }
func (t *countingTicker) Stop()               {}

type countingClock struct {
	count *atomic.Int64
	ticks chan time.Time
}

func (c countingClock) NewTicker(time.Duration) clock.Ticker {
	return &countingTicker{count: c.count, ticks: c.ticks}
}

// Source: docs/run.md; one global limiter counts every request to either endpoint.
func TestRunRateLimiterCountsBothEndpoints(t *testing.T) {
	var count atomic.Int64
	ticks := make(chan time.Time, 64)
	for i := 0; i < 64; i++ {
		ticks <- time.Time{}
	}
	var mu sync.Mutex
	hits := map[string]int{}
	probeFn := func(_ context.Context, request probe.Request) (result.Result, error) {
		mu.Lock()
		hits[request.Endpoint.Addr.String()]++
		mu.Unlock()
		return result.Result{StatusCode: 200}, nil
	}
	cases := []model.ProbeCase{
		{ID: "1", Host: "h", Scheme: "http", Method: "GET", Path: "/a"},
		{ID: "2", Host: "h", Scheme: "http", Method: "GET", Path: "/b"},
	}
	config := RunConfig{Cases: cases, Old: Endpoints{HTTP: mustEndpoint(t, "10.0.0.1", 80)}, New: Endpoints{HTTP: mustEndpoint(t, "10.0.0.2", 80)}, Rate: 5, Concurrency: 1, Now: runNow}
	if _, err := Run(context.Background(), config, countingClock{count: &count, ticks: ticks}, probeFn); err != nil {
		t.Fatal(err)
	}
	// Four requests (two cases times two endpoints), the first of which is immediate.
	if got := count.Load(); got != 3 {
		t.Fatalf("rate limiter waited %d times, want 3", got)
	}
	if hits["10.0.0.1"] != 2 || hits["10.0.0.2"] != 2 {
		t.Fatalf("hits = %v", hits)
	}
}

// Source: docs/adr/0003/0004; https cases use the TLS endpoint and SNI.
func TestRunEquivalentTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("tls")) }))
	t.Cleanup(server.Close)
	address := server.Listener.Addr().String()
	config := RunConfig{
		Cases:   []model.ProbeCase{{ID: "c1", Host: "api.example.com", SNI: "api.example.com", Scheme: "https", Method: "GET", Path: "/"}},
		Old:     Endpoints{HTTPS: mustEndpoint(t, address, 443)},
		New:     Endpoints{HTTPS: mustEndpoint(t, address, 443)},
		Timeout: 5 * time.Second, Rate: 10000, Concurrency: 1, Now: runNow,
	}
	report, err := Run(context.Background(), config, fakeClock{ticksOf(64)}, probe.Do)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != model.OutcomeEquivalent || report.Coverage.Equivalent != 1 {
		t.Fatalf("report = %+v", report)
	}
}

// Source: docs/run.md; progress reports each completed case in order.
func TestRunProgress(t *testing.T) {
	oldServer, newServer := httpServer(t, 200, "a"), httpServer(t, 200, "a")
	config := httpConfig(t, oldServer, newServer)
	var mu sync.Mutex
	var done []int
	config.Progress = func(completed, total int, _ model.ProbeCase, _ model.Outcome) {
		mu.Lock()
		done = append(done, completed)
		mu.Unlock()
		if total != 1 {
			t.Errorf("total = %d", total)
		}
	}
	if _, err := Run(context.Background(), config, fakeClock{ticksOf(64)}, probe.Do); err != nil {
		t.Fatal(err)
	}
	if len(done) != 1 || done[0] != 1 {
		t.Fatalf("progress = %v", done)
	}
}

func TestValidateConcurrency(t *testing.T) {
	for _, value := range []int{0, -1, 65} {
		if err := ValidateConcurrency(value); err == nil {
			t.Fatalf("concurrency %d accepted", value)
		}
	}
	for _, value := range []int{1, 4, 64} {
		if err := ValidateConcurrency(value); err != nil {
			t.Fatalf("concurrency %d rejected: %v", value, err)
		}
	}
}
