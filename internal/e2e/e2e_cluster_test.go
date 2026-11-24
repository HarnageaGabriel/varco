package e2e

// These tests exercise cli.Run through kube.DefaultRunner and the operating
// system's PATH lookup. The test binary copies itself as kubectl (kubectl.exe
// on Windows); TestMain turns that copy into a fixture-backed fake.
//
// Sources:
//   - docs/usage.md: --from-cluster flags, ConfigMap warnings, and get-only access
//   - docs/troubleshooting.md: missing kubectl message and exit status 2
//   - docs/adr/0006-redaction.md: credentials are redacted before serialization
//   - docs/adr/0011-terminal-output-safety.md: kubectl stderr is escaped

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	clusterFakeModeEnv      = "VARCO_E2E_KUBECTL_MODE"
	clusterFakeCallsEnv     = "VARCO_E2E_KUBECTL_CALLS"
	clusterFakeIngressesEnv = "VARCO_E2E_KUBECTL_INGRESSES"
	clusterFakeConfigMapEnv = "VARCO_E2E_KUBECTL_CONFIGMAP"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv(clusterFakeModeEnv); mode != "" {
		os.Exit(runClusterFakeKubectl(mode, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func runClusterFakeKubectl(mode string, args []string) int {
	if err := recordClusterFakeCall(args); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 3
	}
	if mode == "fail" {
		_, _ = fmt.Fprint(os.Stderr, "Get \"https://admin:cluster-secret@example.test/api?token=query-secret\": denied\x1b[31m\n")
		return 7
	}
	if mode != "serve" {
		_, _ = fmt.Fprintf(os.Stderr, "unknown fake kubectl mode %q", mode)
		return 3
	}
	fixture := clusterFakeIngressesEnv
	if containsArg(args, "configmap") {
		fixture = clusterFakeConfigMapEnv
	}
	content, err := os.ReadFile(os.Getenv(fixture))
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 3
	}
	_, _ = os.Stdout.Write(content)
	return 0
}

func recordClusterFakeCall(args []string) error {
	file, err := os.OpenFile(os.Getenv(clusterFakeCallsEnv), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	encodeErr := json.NewEncoder(file).Encode(args)
	return errorsJoin(encodeErr, file.Close())
}

// errorsJoin keeps the helper compatible with the same minimum Go version as
// the repository without hiding a close failure behind an earlier nil error.
func errorsJoin(first, second error) error {
	if first != nil {
		return first
	}
	return second
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func installClusterFakeKubectl(t *testing.T, mode string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	name := "kubectl"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), content, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(t.TempDir(), "calls.jsonl")
	ingresses, err := filepath.Abs(testDataPath("cluster-ingresses.json"))
	if err != nil {
		t.Fatal(err)
	}
	configMap, err := filepath.Abs(testDataPath("cluster-configmap.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv(clusterFakeModeEnv, mode)
	t.Setenv(clusterFakeCallsEnv, calls)
	t.Setenv(clusterFakeIngressesEnv, ingresses)
	t.Setenv(clusterFakeConfigMapEnv, configMap)
	return calls
}

func readClusterFakeCalls(t *testing.T, path string) [][]string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	var calls [][]string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var call []string
		if err := json.Unmarshal(scanner.Bytes(), &call); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, call)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return calls
}

func assertClusterGetCalls(t *testing.T, got [][]string, want [][]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("kubectl calls = %#v, want %#v", got, want)
	}
	for index := range want {
		if strings.Join(got[index], "\x00") != strings.Join(want[index], "\x00") {
			t.Fatalf("kubectl call %d = %#v, want %#v", index, got[index], want[index])
		}
		getSeen := false
		for _, arg := range got[index] {
			if arg == "get" {
				getSeen = true
				break
			}
		}
		if !getSeen {
			t.Fatalf("kubectl call %d is not a get: %#v", index, got[index])
		}
	}
}

// Source: docs/usage.md documents the cluster selectors, ConfigMap warning,
// and read-only kubectl get contract. docs/run.md documents dry-run output.
func TestClusterCommandsThroughKubectlOnPath(t *testing.T) {
	callsFile := installClusterFakeKubectl(t, "serve")
	kubeconfig := filepath.Join(t.TempDir(), "config")
	selectors := []string{
		"--from-cluster",
		"--namespace", "apps",
		"--kube-context", "staging",
		"--kubeconfig", kubeconfig,
	}

	planArgs := make([]string, 0, 1+len(selectors)+2)
	planArgs = append(planArgs, "plan")
	planArgs = append(planArgs, selectors...)
	planArgs = append(planArgs, "--format", "json")
	planResult := runCLI(t, planArgs)
	if planResult.exit != 0 || planResult.stderr != "" {
		t.Fatalf("plan exit=%d stdout=%q stderr=%q", planResult.exit, planResult.stdout, planResult.stderr)
	}
	var document struct {
		Cases []struct {
			Host string `json:"host"`
			Path string `json:"path"`
		} `json:"cases"`
		Warnings []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(planResult.stdout), &document); err != nil {
		t.Fatal(err)
	}
	caseSeen := false
	for _, item := range document.Cases {
		if item.Host == "web.example.test" && item.Path == "/" {
			caseSeen = true
		}
	}
	if !caseSeen {
		t.Fatalf("plan cases = %#v", document.Cases)
	}
	warningSeen := false
	for _, warning := range document.Warnings {
		if warning.Code == "VAR-PLAN-0010" && strings.Contains(warning.Message, "ssl-redirect=true") {
			warningSeen = true
		}
	}
	if !warningSeen {
		t.Fatalf("plan warnings = %#v", document.Warnings)
	}

	runArgs := make([]string, 0, 1+len(selectors)+5)
	runArgs = append(runArgs, "run")
	runArgs = append(runArgs, selectors...)
	runArgs = append(runArgs, "--old", "127.0.0.1", "--new", "127.0.0.2", "--dry-run")
	runResult := runCLI(t, runArgs)
	if runResult.exit != 0 || runResult.stderr != "" {
		t.Fatalf("run exit=%d stdout=%q stderr=%q", runResult.exit, runResult.stdout, runResult.stderr)
	}
	if !strings.Contains(runResult.stdout, "web.example.test") || !strings.Contains(runResult.stdout, "TOTAL ") {
		t.Fatalf("dry-run output = %q", runResult.stdout)
	}

	prefix := []string{"--context", "staging", "--kubeconfig", kubeconfig}
	ingressCall := append(append([]string{}, prefix...), "get", "ingress", "-n", "apps", "-o", "json")
	configMapCall := append(append([]string{}, prefix...), "get", "configmap", "ingress-nginx-controller", "-n", "ingress-nginx", "-o", "json")
	assertClusterGetCalls(t, readClusterFakeCalls(t, callsFile), [][]string{
		ingressCall, configMapCall,
		ingressCall, configMapCall,
	})
}

// Source: docs/troubleshooting.md documents this exact message and exit 2.
func TestClusterMissingKubectl(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	result := runCLI(t, []string{"plan", "--from-cluster"})
	if result.exit != 2 || result.stdout != "" || result.stderr != "kube: kubectl not found: install kubectl or add it to PATH\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", result.exit, result.stdout, result.stderr)
	}
}

// Sources: ADR 0006 requires URL credential redaction; ADR 0011 requires
// control bytes from kubectl stderr to be rendered as visible escapes.
func TestClusterKubectlFailureIsRedactedAndEscaped(t *testing.T) {
	callsFile := installClusterFakeKubectl(t, "fail")
	result := runCLI(t, []string{"plan", "--from-cluster"})
	if result.exit != 2 || result.stdout != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", result.exit, result.stdout, result.stderr)
	}
	if strings.Contains(result.stderr, "cluster-secret") || strings.Contains(result.stderr, "query-secret") || strings.ContainsRune(result.stderr, '\x1b') {
		t.Fatalf("unsafe kubectl error = %q", result.stderr)
	}
	for _, want := range []string{"kubectl get failed (exit 7)", "REDACTED", `\x1b`, `\x0a`} {
		if !strings.Contains(result.stderr, want) {
			t.Fatalf("kubectl error %q lacks %q", result.stderr, want)
		}
	}
	calls := readClusterFakeCalls(t, callsFile)
	if len(calls) != 1 || !containsArg(calls[0], "get") {
		t.Fatalf("kubectl calls = %#v", calls)
	}
}
