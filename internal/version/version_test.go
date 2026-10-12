package version

import "testing"

func TestCurrent(t *testing.T) {
	got := Current()
	if got.SchemaVersion != 1 || got.Version != Version || got.Commit != Commit || got.Date != Date {
		t.Fatalf("Current() = %#v", got)
	}
}
