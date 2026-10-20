package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/HarnageaGabriel/varco/internal/clock"
	"github.com/HarnageaGabriel/varco/internal/compare"
	"github.com/HarnageaGabriel/varco/internal/endpoint"
	"github.com/HarnageaGabriel/varco/internal/ingress"
	"github.com/HarnageaGabriel/varco/internal/model"
	"github.com/HarnageaGabriel/varco/internal/plan"
	"github.com/HarnageaGabriel/varco/internal/probe"
)

// benchNow is the fixed clock reading the benchmark injects, so exception
// expiry never depends on the wall clock.
var benchNow = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

// benchIngresses builds count synthetic Ingress resources in memory. The
// resources are deterministic, so the planned cases are the same on every run.
func benchIngresses(count int) []ingress.Ingress {
	resources := make([]ingress.Ingress, 0, count)
	for index := 0; index < count; index++ {
		name := fmt.Sprintf("app-%04d", index)
		resources = append(resources, ingress.Ingress{
			APIVersion: ingress.APIVersion,
			Kind:       ingress.KindIngress,
			Namespace:  "bench",
			Name:       name,
			ClassName:  "nginx",
			Rules: []ingress.Rule{{
				Host:  name + ".example.com",
				Paths: []ingress.Path{{Path: "/" + name, PathType: "Prefix"}},
			}},
		})
	}
	return resources
}

// benchCases plans the synthetic resources and keeps the first caseCount
// cases of the plan, so the run executes exactly caseCount cases.
func benchCases(caseCount int) []model.ProbeCase {
	planned := plan.Build(benchIngresses(40), plan.Options{Profile: plan.ProfileNginx})
	if len(planned.Cases) < caseCount {
		return nil
	}
	return model.FromPlanCases(planned.Cases[:caseCount])
}

// benchTicker hands out rate-limit ticks from a buffered channel.
type benchTicker struct{ ticks chan time.Time }

func (t benchTicker) C() <-chan time.Time { return t.ticks }

func (benchTicker) Stop() {}

// benchClock is the clock the benchmark injects into the run. The run uses
// the highest supported rate, so the ticks are what keeps the measurement on
// the run itself instead of on the rate limiter.
type benchClock struct{ ticks chan time.Time }

func (c benchClock) NewTicker(time.Duration) clock.Ticker {
	return benchTicker(c)
}

// benchTicks returns a channel with count ready rate-limit ticks.
func benchTicks(count int) chan time.Time {
	ticks := make(chan time.Time, count)
	for range count {
		ticks <- time.Time{}
	}
	return ticks
}

// benchServer starts an in-process server that answers every request with the
// same body, so both sides of the comparison are equivalent.
func benchServer(b *testing.B) *httptest.Server {
	b.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("bench"))
	}))
	b.Cleanup(server.Close)
	return server
}

// benchEndpoint resolves the numeric endpoint of an in-process server.
func benchEndpoint(b *testing.B, server *httptest.Server) endpoint.Endpoint {
	b.Helper()
	parsed, err := endpoint.Parse(server.Listener.Addr().String(), 80)
	if err != nil {
		b.Fatal(err)
	}
	return parsed
}

// BenchmarkRunPlan measures a 200-case run against two in-process servers. It
// sends real requests, so it is skipped in short mode.
func BenchmarkRunPlan(b *testing.B) {
	if testing.Short() {
		b.Skip("the run benchmark sends real requests; skipped in short mode")
	}
	oldEndpoint := benchEndpoint(b, benchServer(b))
	newEndpoint := benchEndpoint(b, benchServer(b))
	cases := benchCases(200)
	if len(cases) != 200 {
		b.Fatalf("planned %d cases, want 200", len(cases))
	}
	config := RunConfig{
		Cases:       cases,
		Old:         Endpoints{HTTP: oldEndpoint, HTTPS: oldEndpoint},
		New:         Endpoints{HTTP: newEndpoint, HTTPS: newEndpoint},
		Timeout:     5 * time.Second,
		Rate:        MaxRate,
		Concurrency: 8,
		Repeat:      1,
		BodyPolicy:  compare.BodyPolicy2xx,
		Now:         benchNow,
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		runClock := benchClock{ticks: benchTicks(4*len(cases) + 16)}
		report, err := Run(context.Background(), config, runClock, probe.Do)
		if err != nil {
			b.Fatal(err)
		}
		if report.Coverage.Executed != len(cases) {
			b.Fatalf("executed %d of %d cases", report.Coverage.Executed, len(cases))
		}
	}
}
