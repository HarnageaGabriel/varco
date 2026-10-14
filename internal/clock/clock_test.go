package clock

import (
	"testing"
	"time"
)

func TestRealTicker(t *testing.T) {
	var c Clock = Real{}
	ticker := c.NewTicker(time.Millisecond)
	defer ticker.Stop()
	select {
	case <-ticker.C():
	case <-time.After(5 * time.Second):
		t.Fatal("no tick")
	}
}
