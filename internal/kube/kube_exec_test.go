package kube

// The tests in this file exercise the real process-execution path of
// DefaultRunner (internal/kube/kube.go:98) without needing a kubectl
// installation, a container runtime or a compiler on the test machine.
//
// Helper-process pattern: the test binary copies itself into a temporary
// directory under the name kubectl (kubectl.exe on Windows), puts that
// directory on PATH and turns itself into the fake kubectl in TestMain when
// VARCO_FAKE_KUBECTL_MODE is set. DefaultRunner resolves kubectl through
// exec.CommandContext, so PATH resolution picks the fake.
//
// Sources cited per case:
//   - internal/kube/kube.go:87-118 (Run: verb check, flag order, bounded
//     stdout/stderr, error mapping)
//   - internal/kube/kube.go:129-147 (CommandError)
//   - internal/kube/kube.go:150-190 (FetchIngresses, FetchConfigMap)
//   - docs/adr/0006-redaction.md (secrets are redacted before they are shown)

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Environment variables that drive the fake kubectl process.
const (
	envFakeMode   = "VARCO_FAKE_KUBECTL_MODE"
	envFakeArgv   = "VARCO_FAKE_KUBECTL_ARGV"
	envFakeStdout = "VARCO_FAKE_KUBECTL_STDOUT"
	envFakeStderr = "VARCO_FAKE_KUBECTL_STDERR"
	envFakeExit   = "VARCO_FAKE_KUBECTL_EXIT"
)

// Modes understood by the fake kubectl.
const (
	fakeModeOK          = "ok"
	fakeModeFail        = "fail"
	fakeModeNotFound    = "notfound"
	fakeModeInvalidJSON = "invalid-json"
)

// fakeKubectlDir holds the copy of this binary that acts as kubectl.
var (
	fakeKubectlOnce sync.Once
	fakeKubectlDir  string
	fakeKubectlErr  error
)

// TestMain turns this binary into the fake kubectl when VARCO_FAKE_KUBECTL_MODE
// is set. Without that variable the binary behaves as a normal test binary.
func TestMain(m *testing.M) {
	if mode := os.Getenv(envFakeMode); mode != "" {
		os.Exit(runFakeKubectl(mode, os.Args[1:]))
	}
	code := m.Run()
	if fakeKubectlDir != "" {
		_ = os.RemoveAll(fakeKubectlDir)
	}
	os.Exit(code)
}

// runFakeKubectl is the fake kubectl. It records its argv and then answers
// according to the mode the test selected.
func runFakeKubectl(mode string, args []string) int {
	recordFakeArgv(args)
	switch mode {
	case fakeModeOK:
		stdout := os.Getenv(envFakeStdout)
		if stdout == "" {
			stdout = "{}"
		}
		_, _ = fmt.Fprint(os.Stdout, stdout)
		return 0
	case fakeModeFail:
		_, _ = fmt.Fprint(os.Stderr, os.Getenv(envFakeStderr))
		return fakeExitCode()
	case fakeModeNotFound:
		_, _ = fmt.Fprint(os.Stderr, `Error from server (NotFound): configmaps "missing" not found`)
		return 1
	case fakeModeInvalidJSON:
		_, _ = fmt.Fprint(os.Stdout, "this is not JSON")
		return 0
	default:
		_, _ = fmt.Fprintf(os.Stderr, "unknown fake kubectl mode %q", mode)
		return 2
	}
}

// recordFakeArgv writes the argv of the fake to the recording file so the
// test can assert on the flags that reached kubectl.
func recordFakeArgv(args []string) {
	path := os.Getenv(envFakeArgv)
	if path == "" {
		return
	}
	if err := os.WriteFile(path, []byte(strings.Join(args, "\n")), 0o600); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "fake kubectl: %v", err)
		os.Exit(3)
	}
}

// fakeExitCode is the exit code the fake uses in fail mode.
func fakeExitCode() int {
	raw := os.Getenv(envFakeExit)
	if raw == "" {
		return 1
	}
	code, err := strconv.Atoi(raw)
	if err != nil {
		return 1
	}
	return code
}

// installFakeKubectl puts a copy of this test binary named kubectl on PATH.
// The copy is made once per test binary run, every test keeps its own argv
// recording file.
func installFakeKubectl(t *testing.T) string {
	t.Helper()
	fakeKubectlOnce.Do(func() {
		self, err := os.Executable()
		if err != nil {
			fakeKubectlErr = err
			return
		}
		content, err := os.ReadFile(self)
		if err != nil {
			fakeKubectlErr = err
			return
		}
		dir, err := os.MkdirTemp("", "varco-fake-kubectl")
		if err != nil {
			fakeKubectlErr = err
			return
		}
		name := "kubectl"
		if runtime.GOOS == "windows" {
			name = "kubectl.exe"
		}
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o755); err != nil {
			fakeKubectlErr = err
			return
		}
		fakeKubectlDir = dir
	})
	if fakeKubectlErr != nil {
		t.Fatal(fakeKubectlErr)
	}
	t.Setenv("PATH", fakeKubectlDir)
	return fakeKubectlDir
}

// fakeArgvFile points the fake kubectl at a fresh recording file. The file
// only exists when the fake kubectl actually ran.
func fakeArgvFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "argv.txt")
	t.Setenv(envFakeArgv, path)
	return path
}

// readFakeArgv reads back the argv recorded by the fake kubectl.
func readFakeArgv(t *testing.T, path string) []string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fake kubectl argv: %v", err)
	}
	trimmed := strings.TrimRight(string(content), "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// sameArgs compares two argv slices.
func sameArgs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// Source: internal/kube/kube.go:88-90 - only the get verb is allowed and the
// check happens before any process is started.
func TestDefaultRunnerRejectsOtherVerbs(t *testing.T) {
	installFakeKubectl(t)
	tests := []struct {
		name string
		args []string
	}{
		{name: "nil args", args: nil},
		{name: "empty args", args: []string{}},
		{name: "apply verb", args: []string{"apply", "-f", "-"}},
		{name: "delete verb", args: []string{"delete", "ingress"}},
		{name: "uppercase verb", args: []string{"Get", "ingress"}},
		{name: "flag as verb", args: []string{"--kubeconfig=/etc/kubeconfig", "get"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(envFakeMode, fakeModeOK)
			argvFile := fakeArgvFile(t)
			_, err := DefaultRunner{}.Run(context.Background(), test.args)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), "only kubectl get is allowed") {
				t.Fatalf("error = %v", err)
			}
			if _, statErr := os.Stat(argvFile); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("kubectl was started for %v", test.args)
			}
		})
	}
}

// Source: internal/kube/kube.go:91-97 - the kubeconfig and the context flags
// are prepended to the user arguments when they are set and are absent when
// they are empty. The kubeconfig is prepended first and the context second,
// so the context flag ends up in front of the kubeconfig flag.
func TestDefaultRunnerPrependsKubeFlags(t *testing.T) {
	installFakeKubectl(t)
	tests := []struct {
		name        string
		kubeConfig  string
		contextName string
		want        []string
	}{
		{
			name: "no flags",
			want: []string{"get", "ingress", "-A", "-o", "json"},
		},
		{
			name:       "kubeconfig only",
			kubeConfig: "/home/user/.kube/config",
			want:       []string{"--kubeconfig", "/home/user/.kube/config", "get", "ingress", "-A", "-o", "json"},
		},
		{
			name:        "context only",
			contextName: "staging",
			want:        []string{"--context", "staging", "get", "ingress", "-A", "-o", "json"},
		},
		{
			name:        "kubeconfig and context",
			kubeConfig:  "/home/user/.kube/config",
			contextName: "staging",
			want: []string{
				"--context", "staging",
				"--kubeconfig", "/home/user/.kube/config",
				"get", "ingress", "-A", "-o", "json",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(envFakeMode, fakeModeOK)
			argvFile := fakeArgvFile(t)
			runner := DefaultRunner{KubeConfig: test.kubeConfig, ContextName: test.contextName}
			out, err := runner.Run(context.Background(), []string{"get", "ingress", "-A", "-o", "json"})
			if err != nil {
				t.Fatal(err)
			}
			if string(out) != "{}" {
				t.Fatalf("output = %q", out)
			}
			got := readFakeArgv(t, argvFile)
			if !sameArgs(got, test.want) {
				t.Fatalf("argv = %#v, want %#v", got, test.want)
			}
		})
	}
}

// Source: internal/kube/kube.go:99-111 and docs/adr/0006-redaction.md - a
// failing kubectl yields a *CommandError whose message redacts the stderr and
// whose stderr is bounded to maxStderrBytes.
func TestDefaultRunnerCommandError(t *testing.T) {
	installFakeKubectl(t)
	const secretURL = "https://user:s3cr3t-token@example.test/api?token=abcdef"
	t.Setenv(envFakeMode, fakeModeFail)
	t.Setenv(envFakeStderr, secretURL+"\n"+strings.Repeat("x", 4*maxStderrBytes))
	t.Setenv(envFakeExit, "1")

	_, err := DefaultRunner{}.Run(context.Background(), []string{"get", "ingress"})
	var commandError *CommandError
	if !errors.As(err, &commandError) {
		t.Fatalf("error = %v, want *CommandError", err)
	}
	if commandError.Exit != 1 {
		t.Fatalf("exit = %d, want 1", commandError.Exit)
	}
	if len(commandError.Stderr) != maxStderrBytes {
		t.Fatalf("stderr length = %d, want %d", len(commandError.Stderr), maxStderrBytes)
	}
	if !strings.HasPrefix(commandError.Stderr, secretURL) {
		t.Fatalf("stderr does not keep its beginning: %q", commandError.Stderr)
	}
	message := commandError.Error()
	if !strings.Contains(message, "kubectl get failed (exit 1)") {
		t.Fatalf("message = %q", message)
	}
	if strings.Contains(message, "s3cr3t-token") || strings.Contains(message, "abcdef") {
		t.Fatalf("message exposes the raw stderr: %q", message)
	}
	if !strings.Contains(message, "REDACTED") {
		t.Fatalf("message is not redacted: %q", message)
	}
}

// Source: internal/kube/kube.go:112-114 - a kubectl that cannot be resolved on
// PATH is reported with the install hint.
func TestDefaultRunnerMissingKubectl(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := DefaultRunner{}.Run(context.Background(), []string{"get", "ingress"})
	var commandError *CommandError
	if !errors.As(err, &commandError) {
		t.Fatalf("error = %v, want *CommandError", err)
	}
	if !commandError.Missing {
		t.Fatalf("error = %v, want Missing", err)
	}
	want := "kubectl not found: install kubectl or add it to PATH"
	if commandError.Error() != want {
		t.Fatalf("message = %q, want %q", commandError.Error(), want)
	}
}

// Source: internal/kube/kube.go:104-116 - an error that is neither an exit
// error nor a missing binary is reported as a CommandError without an exit
// code. A context that is already done stops the run before kubectl starts.
func TestDefaultRunnerDoneContext(t *testing.T) {
	installFakeKubectl(t)
	t.Setenv(envFakeMode, fakeModeOK)
	argvFile := fakeArgvFile(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := DefaultRunner{}.Run(ctx, []string{"get", "ingress"})
	var commandError *CommandError
	if !errors.As(err, &commandError) {
		t.Fatalf("error = %v, want *CommandError", err)
	}
	if commandError.Exit != 0 {
		t.Fatalf("exit = %d, want 0", commandError.Exit)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if _, statErr := os.Stat(argvFile); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("kubectl was started with a done context")
	}
}

// Source: internal/kube/kube.go:150-162 - an empty namespace selects every
// namespace, a valid namespace is passed with -n and an invalid namespace is
// rejected before kubectl runs.
func TestFetchIngressesThroughRunner(t *testing.T) {
	installFakeKubectl(t)

	t.Run("all namespaces", func(t *testing.T) {
		t.Setenv(envFakeMode, fakeModeOK)
		argvFile := fakeArgvFile(t)
		out, err := FetchIngresses(context.Background(), DefaultRunner{}, "")
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != "{}" {
			t.Fatalf("output = %q", out)
		}
		want := []string{"get", "ingress", "-A", "-o", "json"}
		if got := readFakeArgv(t, argvFile); !sameArgs(got, want) {
			t.Fatalf("argv = %#v, want %#v", got, want)
		}
	})

	t.Run("one namespace", func(t *testing.T) {
		t.Setenv(envFakeMode, fakeModeOK)
		argvFile := fakeArgvFile(t)
		if _, err := FetchIngresses(context.Background(), DefaultRunner{}, "apps"); err != nil {
			t.Fatal(err)
		}
		want := []string{"get", "ingress", "-n", "apps", "-o", "json"}
		if got := readFakeArgv(t, argvFile); !sameArgs(got, want) {
			t.Fatalf("argv = %#v, want %#v", got, want)
		}
	})

	invalid := []string{"--token=x", "Upper", "a/b", "a b", "-A", " apps"}
	for _, namespace := range invalid {
		t.Run("invalid namespace "+namespace, func(t *testing.T) {
			t.Setenv(envFakeMode, fakeModeOK)
			argvFile := fakeArgvFile(t)
			_, err := FetchIngresses(context.Background(), DefaultRunner{}, namespace)
			if err == nil {
				t.Fatalf("namespace %q accepted", namespace)
			}
			if !strings.Contains(err.Error(), "not a valid Kubernetes name") {
				t.Fatalf("error = %v", err)
			}
			if _, statErr := os.Stat(argvFile); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("kubectl was started for namespace %q", namespace)
			}
		})
	}
}

// Source: internal/kube/kube.go:166-190 - a missing ConfigMap is reported as
// (nil, nil), a valid document yields the data map and a broken document
// yields a decode error.
func TestFetchConfigMapThroughRunner(t *testing.T) {
	installFakeKubectl(t)

	t.Run("missing configmap", func(t *testing.T) {
		t.Setenv(envFakeMode, fakeModeNotFound)
		data, err := FetchConfigMap(context.Background(), DefaultRunner{}, "ingress-nginx", "missing")
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		if data != nil {
			t.Fatalf("data = %#v, want nil", data)
		}
	})

	t.Run("valid configmap", func(t *testing.T) {
		t.Setenv(envFakeMode, fakeModeOK)
		t.Setenv(envFakeStdout, string(fixture(t, "configmap.json")))
		argvFile := fakeArgvFile(t)
		data, err := FetchConfigMap(context.Background(), DefaultRunner{}, "ingress-nginx", "ingress-nginx-controller")
		if err != nil {
			t.Fatal(err)
		}
		if data["ssl-redirect"] != "true" || data["unknown-key"] != "ignored" {
			t.Fatalf("data = %#v", data)
		}
		want := []string{"get", "configmap", "ingress-nginx-controller", "-n", "ingress-nginx", "-o", "json"}
		if got := readFakeArgv(t, argvFile); !sameArgs(got, want) {
			t.Fatalf("argv = %#v, want %#v", got, want)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		t.Setenv(envFakeMode, fakeModeInvalidJSON)
		data, err := FetchConfigMap(context.Background(), DefaultRunner{}, "ingress-nginx", "ingress-nginx-controller")
		if err == nil {
			t.Fatal("expected a decode error")
		}
		if !strings.Contains(err.Error(), "decode ConfigMap JSON") {
			t.Fatalf("error = %v", err)
		}
		if data != nil {
			t.Fatalf("data = %#v, want nil", data)
		}
	})

	t.Run("failing kubectl", func(t *testing.T) {
		t.Setenv(envFakeMode, fakeModeFail)
		t.Setenv(envFakeStderr, "connection refused")
		data, err := FetchConfigMap(context.Background(), DefaultRunner{}, "ingress-nginx", "ingress-nginx-controller")
		if err == nil {
			t.Fatal("expected an error")
		}
		var commandError *CommandError
		if !errors.As(err, &commandError) {
			t.Fatalf("error = %v, want *CommandError", err)
		}
		if data != nil {
			t.Fatalf("data = %#v, want nil", data)
		}
	})

	invalid := []string{"--token=x", "Upper", "a/b", ""}
	for _, name := range invalid {
		t.Run("invalid name "+name, func(t *testing.T) {
			t.Setenv(envFakeMode, fakeModeOK)
			argvFile := fakeArgvFile(t)
			if _, err := FetchConfigMap(context.Background(), DefaultRunner{}, "ingress-nginx", name); err == nil {
				t.Fatalf("ConfigMap name %q accepted", name)
			}
			if _, err := FetchConfigMap(context.Background(), DefaultRunner{}, name, "cm"); err == nil {
				t.Fatalf("namespace %q accepted", name)
			}
			if _, statErr := os.Stat(argvFile); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("kubectl was started for %q", name)
			}
		})
	}
}

// Source: internal/kube/kube.go:129-147 - a CommandError without an exit code
// reports the wrapped error and a nil receiver never panics.
func TestCommandErrorMessages(t *testing.T) {
	var nilError *CommandError
	if nilError.Error() != "<nil>" {
		t.Fatalf("nil error = %q", nilError.Error())
	}
	failed := &CommandError{Err: errors.New("signal: killed")}
	if failed.Error() != "kubectl get failed: signal: killed" {
		t.Fatalf("message = %q", failed.Error())
	}
	if !errors.Is(failed, failed.Err) {
		t.Fatal("Unwrap does not expose the wrapped error")
	}
}

// Source: internal/kube/kube.go:192-197 - truncate keeps a short value and
// cuts a long one at the limit.
func TestTruncateKeepsShortValues(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Fatalf("truncate = %q, want %q", got, "short")
	}
	if got := truncate("abcdef", 3); got != "abc" {
		t.Fatalf("truncate = %q, want %q", got, "abc")
	}
}

// Source: internal/kube/kube.go:39-50 - NotFound carries the kubectl error.
func TestNotFoundError(t *testing.T) {
	var nilError *NotFound
	if nilError.Error() != "<nil>" {
		t.Fatalf("nil error = %q", nilError.Error())
	}
	inner := errors.New(`configmaps "missing" not found`)
	notFound := &NotFound{Err: inner}
	if notFound.Error() != inner.Error() {
		t.Fatalf("message = %q", notFound.Error())
	}
	if !errors.Is(notFound, inner) {
		t.Fatal("Unwrap does not expose the wrapped error")
	}
}
