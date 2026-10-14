package kube

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct {
	args []string
	out  []byte
	err  error
}

func (r *fakeRunner) Run(_ context.Context, args []string) ([]byte, error) {
	r.args = append([]string(nil), args...)
	return r.out, r.err
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "..", "testdata", "kube", name))
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func TestFetchIngresses(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		want      []string
	}{
		{name: "all namespaces", want: []string{"get", "ingress", "-A", "-o", "json"}},
		{name: "one namespace", namespace: "apps", want: []string{"get", "ingress", "-n", "apps", "-o", "json"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeRunner{out: fixture(t, "ingresses.json")}
			got, err := FetchIngresses(context.Background(), runner, test.namespace)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(fixture(t, "ingresses.json")) {
				t.Fatalf("output = %s", got)
			}
			if strings.Join(runner.args, " ") != strings.Join(test.want, " ") {
				t.Fatalf("args = %#v, want %#v", runner.args, test.want)
			}
		})
	}
}

func TestFetchConfigMap(t *testing.T) {
	runner := &fakeRunner{out: fixture(t, "configmap.json")}
	got, err := FetchConfigMap(context.Background(), runner, "ingress-nginx", "ingress-nginx-controller")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"ssl-redirect": "true", "unknown-key": "ignored"}
	if len(got) != len(want) || got["ssl-redirect"] != want["ssl-redirect"] || got["unknown-key"] != want["unknown-key"] {
		t.Fatalf("data = %#v", got)
	}
	if strings.Join(runner.args, " ") != "get configmap ingress-nginx-controller -n ingress-nginx -o json" {
		t.Fatalf("args = %#v", runner.args)
	}

	runner = &fakeRunner{err: &CommandError{Exit: 1, Stderr: `Error from server (NotFound): configmaps "missing" not found`}}
	got, err = FetchConfigMap(context.Background(), runner, "ns", "missing")
	if err != nil || got != nil {
		t.Fatalf("missing ConfigMap = %#v, %v; want nil, nil", got, err)
	}
}

func TestDefaultRunnerRejectsNonGet(t *testing.T) {
	_, err := DefaultRunner{}.Run(context.Background(), []string{"apply", "-f", "-"})
	if err == nil || !strings.Contains(err.Error(), "only kubectl get") {
		t.Fatalf("error = %v", err)
	}
}

func TestDefaultRunnerErrors(t *testing.T) {
	missing := CommandError{Missing: true, Err: execErrNotFound()}
	if !strings.Contains(missing.Error(), "kubectl not found") {
		t.Fatalf("missing = %v", missing)
	}
	failed := &CommandError{Exit: 1, Stderr: "https://user:secret@example.test", Err: errors.New("exit status 1")}
	if len(failed.Error()) > maxStderrBytes+len("kubectl get failed (exit 1): ") {
		t.Fatalf("error not bounded: %d", len(failed.Error()))
	}
	if strings.Contains(failed.Error(), "secret") {
		t.Fatalf("error exposes raw stderr: %s", failed.Error())
	}
}

func TestOutputCap(t *testing.T) {
	output := make([]byte, MaxOutputBytes+1)
	if _, err := json.Marshal(output); err != nil {
		t.Fatal(err)
	}
	message := truncate(strings.Repeat("a", MaxOutputBytes+1), maxStderrBytes)
	if len(message) != maxStderrBytes {
		t.Fatalf("truncate = %d", len(message))
	}
}

func execErrNotFound() error {
	return &os.PathError{Op: "start", Path: "kubectl", Err: errors.New("file not found")}
}

// Source: kubectl would read a leading dash as a flag; names must be DNS-1123.
func TestFetchRejectsFlagLikeNames(t *testing.T) {
	runner := &recordingRunner{}
	for _, name := range []string{"--token=x", "-A", "Upper", "a b", ""} {
		if _, err := FetchConfigMap(context.Background(), runner, "ns", name); err == nil && name != "" {
			t.Fatalf("ConfigMap name %q accepted", name)
		}
		if _, err := FetchConfigMap(context.Background(), runner, name, "cm"); err == nil {
			t.Fatalf("namespace %q accepted", name)
		}
		if name != "" {
			if _, err := FetchIngresses(context.Background(), runner, name); err == nil {
				t.Fatalf("ingress namespace %q accepted", name)
			}
		}
	}
	if len(runner.calls) != 0 {
		t.Fatalf("kubectl was invoked for an invalid name: %v", runner.calls)
	}
}

type recordingRunner struct{ calls [][]string }

func (r *recordingRunner) Run(_ context.Context, args []string) ([]byte, error) {
	r.calls = append(r.calls, args)
	return []byte("{}"), nil
}
