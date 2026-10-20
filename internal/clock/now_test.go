package clock

import (
	"testing"
	"time"
)

// Sources:
//   - CONVENTIONS.md "Determinism": "Time only through an injected clock."
//   - internal/clock/clock.go: Real.Now is the single wall-clock edge of the
//     tool, so it must report a real, current, non-decreasing instant.
//
// The test never calls time.Now itself (forbidigo forbids it outside the
// production wall-clock edge), so it asserts only properties of the value
// Real.Now returns: it is not the zero time, it is not before a fixed past
// reference, and successive readings never move backwards.
func TestRealNow(t *testing.T) {
	// A fixed reference instant from 2000, long before any plausible wall
	// clock, used instead of a live reading to keep the assertion stable.
	reference := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		readings      int
		assertOrdered bool
	}{
		{name: "single reading", readings: 1},
		{name: "two successive readings", readings: 2, assertOrdered: true},
		{name: "repeated readings", readings: 5, assertOrdered: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			system := Real{}
			var previous time.Time
			for reading := 1; reading <= test.readings; reading++ {
				current := system.Now()
				if current.IsZero() {
					t.Fatalf("Now() returned the zero time on reading %d", reading)
				}
				if current.Before(reference) {
					t.Fatalf("Now() = %v, want a time at or after %v", current, reference)
				}
				if test.assertOrdered && reading > 1 && current.Before(previous) {
					t.Fatalf("Now() = %v decreased after %v", current, previous)
				}
				previous = current
			}
		})
	}
}
