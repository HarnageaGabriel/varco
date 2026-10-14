// Package clock isolates wall-clock and ticker access for deterministic consumers.
package clock

import "time"

// Ticker supplies rate-limit events.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// Clock creates tickers.
type Clock interface{ NewTicker(time.Duration) Ticker }

// Real uses the system clock.
type Real struct{}

// NewTicker creates a system ticker.
func (Real) NewTicker(d time.Duration) Ticker { return realTicker{time.NewTicker(d)} }

type realTicker struct{ *time.Ticker }

func (t realTicker) C() <-chan time.Time { return t.Ticker.C }
