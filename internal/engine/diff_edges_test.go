package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HarnageaGabriel/varco/internal/probe"
	"github.com/HarnageaGabriel/varco/internal/result"
)

// Source: internal/engine/diff.go:95-97; a context that is already cancelled
// stops the comparison before the first request.
func TestDiffCancelBeforeFirstRequest(t *testing.T) {
	ctx, cancel := edgesContext(t)
	cancel()
	var calls int
	_, err := Diff(ctx, config(), fakeClock{make(chan time.Time)}, func(context.Context, probe.Request) (result.Result, error) {
		calls++
		return result.Result{}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want a context cancellation", err)
	}
	if calls != 0 {
		t.Fatalf("calls = %d, want 0", calls)
	}
}

// Source: internal/engine/diff.go:97-100; a cancellation while the comparison
// waits for a rate-limit tick aborts the run and no later request is sent.
func TestDiffCancelWhileWaitingForRateTick(t *testing.T) {
	ctx, cancel := edgesContext(t)
	defer cancel()
	started := make(chan struct{})
	go func() {
		select {
		case <-started:
			cancel()
		case <-ctx.Done():
		}
	}()
	var calls int
	_, err := Diff(ctx, config(), fakeClock{make(chan time.Time)}, func(context.Context, probe.Request) (result.Result, error) {
		calls++
		close(started)
		return result.Result{StatusCode: 200}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want a context cancellation", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

// Source: internal/engine/diff.go:67-70; a rate that cannot be used as a
// request rate is refused before the first request.
func TestDiffRejectsInvalidRate(t *testing.T) {
	ctx, cancel := edgesContext(t)
	defer cancel()
	diffConfig := config()
	diffConfig.Rate = 0
	var calls int
	if _, err := Diff(ctx, diffConfig, fakeClock{ticksOf(1)}, func(context.Context, probe.Request) (result.Result, error) {
		calls++
		return result.Result{}, nil
	}); err == nil {
		t.Fatal("rate 0 accepted")
	}
	if calls != 0 {
		t.Fatalf("calls = %d, want 0", calls)
	}
}
