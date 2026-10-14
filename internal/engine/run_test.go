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
	"github.com/HarnageaGabriel/varco/internal/compare"
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

// varyingHeaderServer answers 200 with a fixed body and an X-Frame-Options
// value that cycles through values, so repeated observations disagree.
func varyingHeaderServer(t *testing.T, values []string) *httptest.Server {
	t.Helper()
	var counter atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Frame-Options", values[int(counter.Add(1)-1)%len(values)])
		_, _ = w.Write([]byte("same"))
	}))
	t.Cleanup(server.Close)
	return server
}

// varyingStatusServer answers with a status that cycles through values.
func varyingStatusServer(t *testing.T, statuses []int) *httptest.Server {
	t.Helper()
	var counter atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(statuses[int(counter.Add(1)-1)%len(statuses)])
		_, _ = w.Write([]byte("same"))
	}))
	t.Cleanup(server.Close)
	return server
}

// Source: docs/run.md --repeat; a field that differs between repeated
// observations of one endpoint is accepted with the auto:unstable exception.
func TestRunUnstableFieldAccepted(t *testing.T) {
	oldServer := httpServer(t, 200, "same")
	newServer := varyingHeaderServer(t, []string{"SAMEORIGIN", "DENY", "SAMEORIGIN"})
	config := httpConfig(t, oldServer, newServer)
	config.Repeat = 3
	report, err := Run(context.Background(), config, fakeClock{ticksOf(64)}, probe.Do)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != model.OutcomeEquivalent || report.Repeat != 3 {
		t.Fatalf("report = %+v", report)
	}
	found := false
	for _, difference := range report.Cases[0].Differences {
		if difference.Field != "header.x-frame-options" {
			continue
		}
		found = true
		if !difference.Accepted || difference.ExceptionID != "auto:unstable" {
			t.Fatalf("difference = %+v", difference)
		}
		if !strings.Contains(difference.Reason, "of new") {
			t.Fatalf("reason = %q", difference.Reason)
		}
	}
	if !found {
		t.Fatalf("no unstable difference: %+v", report.Cases[0].Differences)
	}
	if len(report.Unstable) != 1 || report.Unstable[0].Host != "api.example.com" || report.Unstable[0].Field != "header.x-frame-options" {
		t.Fatalf("unstable = %+v", report.Unstable)
	}
}

// Source: docs/run.md --repeat; an unstable status makes the case INCONCLUSIVE.
func TestRunUnstableStatusInconclusive(t *testing.T) {
	config := httpConfig(t, httpServer(t, 200, "same"), varyingStatusServer(t, []int{200, 500, 200}))
	config.Repeat = 3
	report, err := Run(context.Background(), config, fakeClock{ticksOf(64)}, probe.Do)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != model.OutcomeInconclusive {
		t.Fatalf("verdict = %s", report.Verdict)
	}
	if report.Cases[0].Outcome != model.OutcomeInconclusive || report.Cases[0].Error != "unstable status" {
		t.Fatalf("evaluation = %+v", report.Cases[0])
	}
}

// Source: docs/run.md --max-requests; the case that would exceed the budget and
// every later case are skipped, never probed.
func TestRunBudgetSkipsRemaining(t *testing.T) {
	cases := []model.ProbeCase{
		{ID: "a", Host: "h", Scheme: "http", Method: "GET", Path: "/a", Family: "base"},
		{ID: "b", Host: "h", Scheme: "http", Method: "GET", Path: "/b", Family: "base"},
		{ID: "c", Host: "h", Scheme: "http", Method: "GET", Path: "/c", Family: "base"},
	}
	probes := atomic.Int64{}
	probeFn := func(_ context.Context, _ probe.Request) (result.Result, error) {
		probes.Add(1)
		return result.Result{StatusCode: 200}, nil
	}
	config := RunConfig{Cases: cases, Old: Endpoints{HTTP: mustEndpoint(t, "10.0.0.1", 80)}, New: Endpoints{HTTP: mustEndpoint(t, "10.0.0.2", 80)}, Rate: 10000, Concurrency: 1, Repeat: 1, MaxRequests: 2, Now: runNow}
	report, err := Run(context.Background(), config, fakeClock{ticksOf(64)}, probeFn)
	if err != nil {
		t.Fatal(err)
	}
	if probes.Load() != 2 {
		t.Fatalf("probes = %d, want 2 (one case)", probes.Load())
	}
	if report.Coverage.Executed != 1 || report.Coverage.Skipped != 2 {
		t.Fatalf("coverage = %+v", report.Coverage)
	}
	for _, evaluation := range report.Cases[1:] {
		if evaluation.Outcome != model.OutcomeInconclusive || evaluation.Error != "budget" {
			t.Fatalf("evaluation = %+v", evaluation)
		}
	}
}

// Source: docs/run.md --body; the 2xx default compares the body only when both
// statuses are equal and 2xx.
func TestRunBodyPolicyMatrix(t *testing.T) {
	probeFn := func(oldStatus, newStatus int, oldHash, newHash string) ProbeFunc {
		return func(_ context.Context, request probe.Request) (result.Result, error) {
			if request.Endpoint.Addr.String() == "10.0.0.2" {
				return result.Result{StatusCode: newStatus, BodySHA256: newHash, BodySize: 4}, nil
			}
			return result.Result{StatusCode: oldStatus, BodySHA256: oldHash, BodySize: 4}, nil
		}
	}
	tests := []struct {
		name           string
		oldStatus      int
		newStatus      int
		policy         compare.BodyPolicy
		wantBodyField  bool
		wantEquivalent bool
	}{
		{name: "2xx equal status compares", oldStatus: 200, newStatus: 200, policy: compare.BodyPolicy2xx, wantBodyField: true},
		{name: "2xx non-2xx skips", oldStatus: 404, newStatus: 404, policy: compare.BodyPolicy2xx, wantEquivalent: true},
		{name: "hash always compares", oldStatus: 404, newStatus: 404, policy: compare.BodyPolicyHash, wantBodyField: true},
		{name: "none never compares", oldStatus: 200, newStatus: 200, policy: compare.BodyPolicyNone, wantEquivalent: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := RunConfig{
				Cases:       []model.ProbeCase{{ID: "c1", Host: "api.example.com", Scheme: "http", Method: "GET", Path: "/", Family: "base"}},
				Old:         Endpoints{HTTP: mustEndpoint(t, "10.0.0.1", 80)},
				New:         Endpoints{HTTP: mustEndpoint(t, "10.0.0.2", 80)},
				Rate:        10000,
				Concurrency: 1,
				Repeat:      1,
				BodyPolicy:  test.policy,
				Now:         runNow,
			}
			report, err := Run(context.Background(), config, fakeClock{ticksOf(64)}, probeFn(test.oldStatus, test.newStatus, "aaaa", "bbbb"))
			if err != nil {
				t.Fatal(err)
			}
			body := false
			for _, difference := range report.Cases[0].Differences {
				if difference.Field == "body" {
					body = true
				}
			}
			if body != test.wantBodyField {
				t.Fatalf("body difference = %t, want %t: %+v", body, test.wantBodyField, report.Cases[0].Differences)
			}
			if test.wantEquivalent && report.Verdict != model.OutcomeEquivalent {
				t.Fatalf("verdict = %s", report.Verdict)
			}
			if report.BodyPolicy != string(test.policy) {
				t.Fatalf("body_policy = %q", report.BodyPolicy)
			}
		})
	}
}

// Source: docs/run.md --exclude-path and --family; filtering happens before the
// run and the removed cases are counted as excluded.
func TestFilterCases(t *testing.T) {
	cases := []model.ProbeCase{
		{ID: "a", Family: "base", Path: "/api"},
		{ID: "b", Family: "subpath", Path: "/api/varco-probe"},
		{ID: "c", Family: "base", Path: "/health"},
	}
	kept, excluded := FilterCases(cases, []string{"base"}, nil)
	if len(kept) != 2 || excluded != 1 || kept[0].ID != "a" || kept[1].ID != "c" {
		t.Fatalf("family filter kept=%#v excluded=%d", kept, excluded)
	}
	// path.Match's * does not cross "/", so two globs cover the route and its
	// subpaths; both are repeatable --exclude-path values.
	kept, excluded = FilterCases(cases, nil, []string{"/api", "/api/*"})
	if len(kept) != 1 || excluded != 2 || kept[0].ID != "c" {
		t.Fatalf("exclude-path kept=%#v excluded=%d", kept, excluded)
	}
}

// Source: docs/run.md --dry-run; DryRun lists the requests without probing and
// honors the budget.
func TestDryRunListsRequests(t *testing.T) {
	cases := []model.ProbeCase{
		{ID: "a", Host: "h", Scheme: "http", Method: "GET", Path: "/a", Family: "base"},
		{ID: "b", Host: "h", Scheme: "http", Method: "GET", Path: "/b", Family: "base"},
	}
	config := RunConfig{Cases: cases, Old: Endpoints{HTTP: mustEndpoint(t, "10.0.0.1", 80)}, New: Endpoints{HTTP: mustEndpoint(t, "10.0.0.2", 80)}, Repeat: 2, MaxRequests: 6}
	requests := DryRun(config)
	if len(requests) != 4 {
		t.Fatalf("requests = %d, want 4 (one case, two sides, repeat 2)", len(requests))
	}
	if got := requests[0].Line(); got != "old http 10.0.0.1:80 GET h - /a" {
		t.Fatalf("first line = %q", got)
	}
	if got := requests[2].Line(); got != "new http 10.0.0.2:80 GET h - /a" {
		t.Fatalf("third line = %q", got)
	}
}
