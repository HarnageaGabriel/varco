package engine

import (
	"context"
	"errors"
	"net/netip"
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

func TestDiff(t *testing.T) {
	ticks := make(chan time.Time, 10)
	for i := 0; i < 10; i++ {
		ticks <- time.Time{}
	}
	var calls []string
	fn := func(_ context.Context, r probe.Request) (result.Result, error) {
		calls = append(calls, r.Endpoint.String()+r.Path)
		code := 200
		if r.Endpoint.Addr.String() == "10.0.0.2" && r.Path == "/b" {
			code = 404
		}
		return result.Result{StatusCode: code}, nil
	}
	diffs, err := Diff(context.Background(), config(), fakeClock{ticks}, fn)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 4 || len(diffs) != 1 || diffs[0].Path != "/b" {
		t.Fatalf("calls=%v diffs=%+v", calls, diffs)
	}
}

func TestDiffErrors(t *testing.T) {
	boom := errors.New("boom")
	ticks := make(chan time.Time, 10)
	for i := 0; i < 10; i++ {
		ticks <- time.Time{}
	}
	fail := func(context.Context, probe.Request) (result.Result, error) { return result.Result{}, boom }
	if _, err := Diff(context.Background(), config(), fakeClock{ticks}, fail); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ok := func(context.Context, probe.Request) (result.Result, error) { return result.Result{}, nil }
	if _, err := Diff(ctx, config(), fakeClock{make(chan time.Time)}, ok); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}
