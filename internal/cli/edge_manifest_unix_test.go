//go:build linux || darwin

package cli

import (
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
)

// TestPlanInputSkipsNamedPipe covers a non-regular file in a walked
// directory: a named pipe is not a regular file, so the walk skips it
// instead of reading it. Source: internal/cli/plan.go walkableManifest (only
// regular files are read) and docs/plan.md (symlinks and other non-regular
// files are skipped).
func TestPlanInputSkipsNamedPipe(t *testing.T) {
	directory := t.TempDir()
	edgeWriteFile(t, filepath.Join(directory, "real.yaml"), edgeManifest("real", "real.example.com"))
	if err := syscall.Mkfifo(filepath.Join(directory, "pipe.yaml"), 0o600); err != nil {
		t.Skipf("cannot create a named pipe: %v", err)
	}
	document := edgePlan(t, "", "-f", directory, "--format", "json")
	want := []string{"real.example.com", edgeSyntheticHost}
	if got := edgeHosts(document); !reflect.DeepEqual(got, want) {
		t.Fatalf("hosts = %#v, want %#v", got, want)
	}
}
