package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/ingress"
	"github.com/HarnageaGabriel/varco/internal/plan"
)

// edgePlanDocument is the JSON envelope varco writes for a plan. Source:
// internal/cli/plan.go writePlanJSON and docs/plan.md (the envelope holds
// schema_version, profile, cases, unsupported and warnings).
type edgePlanDocument struct {
	SchemaVersion int            `json:"schema_version"`
	Profile       string         `json:"profile"`
	Cases         []plan.Case    `json:"cases"`
	Unsupported   []plan.Finding `json:"unsupported"`
	Warnings      []plan.Warning `json:"warnings"`
}

// edgePlan runs the plan command over the given inputs and decodes the JSON
// envelope.
func edgePlan(t *testing.T, stdin string, args ...string) edgePlanDocument {
	t.Helper()
	code, stdout, stderr := runPlanCLI(t, stdin, args...)
	if code != 0 {
		t.Fatalf("plan exit code = %d, want 0; stderr = %q", code, stderr)
	}
	var document edgePlanDocument
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatalf("plan JSON: %v", err)
	}
	return document
}

// edgeHosts returns the sorted, unique hosts of the planned cases.
func edgeHosts(document edgePlanDocument) []string {
	seen := make(map[string]bool, len(document.Cases))
	hosts := make([]string, 0, len(document.Cases))
	for _, item := range document.Cases {
		if seen[item.Host] {
			continue
		}
		seen[item.Host] = true
		hosts = append(hosts, item.Host)
	}
	sort.Strings(hosts)
	return hosts
}

// edgeManifest returns a minimal v1 Ingress manifest. Source: the Kubernetes
// Ingress API reference
// (https://kubernetes.io/docs/reference/kubernetes-api/networking/v1/).
func edgeManifest(name, host string) string {
	return "apiVersion: networking.k8s.io/v1\n" +
		"kind: Ingress\n" +
		"metadata:\n" +
		"  name: " + name + "\n" +
		"  namespace: edge\n" +
		"spec:\n" +
		"  rules:\n" +
		"    - host: " + host + "\n" +
		"      http:\n" +
		"        paths:\n" +
		"          - path: /" + name + "\n" +
		"            pathType: Prefix\n"
}

// edgeWriteFile writes a manifest fixture for a test.
func edgeWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// edgeSyntheticHost is the host of the synthetic default-backend case every
// plan carries. Source: docs/plan.md (a default-backend case is added for
// every scheme the plan uses, on the host varco-unknown.invalid).
const edgeSyntheticHost = "varco-unknown.invalid"

// TestPlanInputDirectoryTree covers a directory with nested directories: a
// manifest in a nested directory is read too. Source: internal/cli/plan.go
// expandInputs (a directory is walked recursively and only regular manifest
// files are collected) and docs/plan.md (a directory is walked recursively).
func TestPlanInputDirectoryTree(t *testing.T) {
	root := filepath.Join("testdata", "edge", "tree")
	document := edgePlan(t, "", "-f", root, "--format", "json")
	want := []string{"alpha.example.com", "beta.example.com", "gamma.example.com", edgeSyntheticHost}
	if got := edgeHosts(document); !reflect.DeepEqual(got, want) {
		t.Fatalf("hosts = %#v, want %#v", got, want)
	}
}

// TestPlanInputManifestExtensions covers the file extensions the directory
// walk collects. Source: internal/cli/plan.go manifestFile (the extension is
// compared case-insensitively against .yaml, .yml and .json) and docs/plan.md
// (only regular *.yaml, *.yml and *.json files are read).
func TestPlanInputManifestExtensions(t *testing.T) {
	root := filepath.Join("testdata", "edge", "mixed")
	document := edgePlan(t, "", "-f", root, "--format", "json")
	want := []string{"data.example.com", "lower.example.com", "mid.example.com", "upper.example.com", edgeSyntheticHost}
	if got := edgeHosts(document); !reflect.DeepEqual(got, want) {
		t.Fatalf("hosts = %#v, want %#v", got, want)
	}
}

// TestPlanInputSkipsSymlinks covers the symlinks in a walked directory: a
// symlink is not a regular file, so the walk skips it and never follows the
// directory one points at. Source: internal/cli/plan.go walkableManifest (only
// regular files are read) and docs/plan.md (symlinks and other non-regular
// files are skipped).
func TestPlanInputSkipsSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	edgeWriteFile(t, filepath.Join(outside, "target.yaml"), edgeManifest("target", "target.example.com"))
	if err := os.Mkdir(filepath.Join(outside, "inside"), 0o750); err != nil {
		t.Fatal(err)
	}
	edgeWriteFile(t, filepath.Join(outside, "inside", "inside.yaml"), edgeManifest("inside-dir", "inside-dir.example.com"))
	walked := filepath.Join(root, "walked")
	if err := os.Mkdir(walked, 0o750); err != nil {
		t.Fatal(err)
	}
	edgeWriteFile(t, filepath.Join(walked, "real.yaml"), edgeManifest("real", "real.example.com"))
	if err := os.Mkdir(filepath.Join(walked, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	edgeWriteFile(t, filepath.Join(walked, "nested", "inside.yaml"), edgeManifest("inside", "inside.example.com"))
	if err := os.Symlink(filepath.Join(outside, "target.yaml"), filepath.Join(walked, "link.yaml")); err != nil {
		t.Skipf("the platform does not permit symlinks: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "inside"), filepath.Join(walked, "linkdir")); err != nil {
		t.Skipf("the platform does not permit symlinks: %v", err)
	}
	document := edgePlan(t, "", "-f", walked, "--format", "json")
	want := []string{"inside.example.com", "real.example.com", edgeSyntheticHost}
	if got := edgeHosts(document); !reflect.DeepEqual(got, want) {
		t.Fatalf("hosts = %#v, want %#v", got, want)
	}
}

// TestPlanInputUnreadableFile covers a manifest the operating system refuses
// to open: it is a usage error, not a manifest diagnostic. Source:
// internal/cli/plan.go parseInputs (an unreadable file is a usage error) and
// docs/exit-codes.md (a usage error exits 2).
func TestPlanInputUnreadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not enforce the read permission bits")
	}
	path := filepath.Join(t.TempDir(), "locked.yaml")
	edgeWriteFile(t, path, edgeManifest("locked", "locked.example.com"))
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	if file, err := os.Open(path); err == nil {
		_ = file.Close()
		t.Skip("the test runs with permissions that ignore the read mode bits")
	}
	code, stdout, stderr := runPlanCLI(t, "", "-f", path)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stdout = %q", code, stdout)
	}
	if !strings.Contains(stderr, "cannot read") {
		t.Fatalf("stderr = %q, want the read diagnostic", stderr)
	}
	if strings.Contains(stderr, "VAR-PLAN-0003") {
		t.Fatalf("stderr = %q, want no manifest diagnostic", stderr)
	}
}

// TestPlanInputEmptyDirectory covers a directory with no manifest: the plan
// is empty and the command still succeeds. Source: internal/cli/plan.go
// expandInputs (a directory with no manifest file yields no input) and
// docs/plan.md (exit code 0 when a plan was produced).
func TestPlanInputEmptyDirectory(t *testing.T) {
	document := edgePlan(t, "", "-f", t.TempDir(), "--format", "json")
	if len(document.Cases) != 0 {
		t.Fatalf("cases = %#v, want none", document.Cases)
	}
	if len(document.Unsupported) != 0 {
		t.Fatalf("unsupported = %#v, want none", document.Unsupported)
	}
	if len(document.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", document.Warnings)
	}
}

// TestPlanInputSameFileTwice covers two inputs that name the same file: the
// plan is the same whether the file is named once, twice or under a second
// path that points at it. Source: internal/cli/plan.go expandInputs (the
// collected paths are de-duplicated) and docs/plan.md (identical cases are
// de-duplicated).
func TestPlanInputSameFileTwice(t *testing.T) {
	path := filepath.Join("testdata", "edge", "tree", "alpha.yaml")
	_, once, _ := runPlanCLI(t, "", "-f", path, "--format", "json")
	_, twice, _ := runPlanCLI(t, "", "-f", path, "-f", path, "--format", "json")
	if once != twice {
		t.Fatal("naming the same manifest twice changed the plan")
	}
	alias := "testdata/edge/tree/./alpha.yaml"
	_, indirect, _ := runPlanCLI(t, "", "-f", path, "-f", alias, "--format", "json")
	if once != indirect {
		t.Fatal("naming the same manifest under two paths changed the plan")
	}
}

// TestPlanInputStdinWithFiles covers stdin mixed with files: the stream is
// planned together with the files. Source: internal/cli/plan.go expandInputs
// (the - input is the stdin stream) and docs/plan.md (the -f flag is
// repeatable and reads a file, a directory or stdin).
func TestPlanInputStdinWithFiles(t *testing.T) {
	stdin := edgeManifest("piped", "stdin.example.com")
	path := filepath.Join("testdata", "edge", "tree", "alpha.yaml")
	document := edgePlan(t, stdin, "-f", "-", "-f", path, "--format", "json")
	want := []string{"alpha.example.com", "stdin.example.com", edgeSyntheticHost}
	if got := edgeHosts(document); !reflect.DeepEqual(got, want) {
		t.Fatalf("hosts = %#v, want %#v", got, want)
	}
}

// TestPlanInputOrderIndependence covers the order of the inputs: the plan is
// sorted, so the same inputs in another order produce the same plan. Source:
// docs/plan.md (cases are ordered by a stable key and the output does not
// depend on the order the inputs were given in) and internal/cli/plan.go
// expandInputs (the collected paths are sorted).
func TestPlanInputOrderIndependence(t *testing.T) {
	first := filepath.Join("testdata", "edge", "tree", "alpha.yaml")
	second := filepath.Join("testdata", "edge", "tree", "nested", "beta.yml")
	_, forward, _ := runPlanCLI(t, "", "-f", first, "-f", second, "--format", "json")
	_, backward, _ := runPlanCLI(t, "", "-f", second, "-f", first, "--format", "json")
	if forward != backward {
		t.Fatal("the order of the inputs changed the plan")
	}
}

// TestPlanInputEncodedFiles covers the encodings a manifest file can carry: a
// byte order mark and CRLF line breaks are read, a tab used for indentation
// is a manifest error. Sources: the YAML 1.2 specification (a stream may
// start with a byte order mark, may use CRLF line breaks and may not indent
// with a tab) and docs/plan.md (a manifest that cannot be parsed is
// VAR-PLAN-0003).
func TestPlanInputEncodedFiles(t *testing.T) {
	readable := t.TempDir()
	edgeWriteFile(t, filepath.Join(readable, "bom.yaml"), "\ufeff"+edgeManifest("bom", "bom.example.com"))
	edgeWriteFile(t, filepath.Join(readable, "crlf.yaml"), strings.ReplaceAll(edgeManifest("crlf", "crlf.example.com"), "\n", "\r\n"))
	document := edgePlan(t, "", "-f", readable, "--format", "json")
	want := []string{"bom.example.com", "crlf.example.com", edgeSyntheticHost}
	if got := edgeHosts(document); !reflect.DeepEqual(got, want) {
		t.Fatalf("hosts = %#v, want %#v", got, want)
	}

	broken := t.TempDir()
	tabbed := strings.ReplaceAll(edgeManifest("tab", "tab.example.com"), "\n  name: tab", "\n\tname: tab")
	edgeWriteFile(t, filepath.Join(broken, "tab.yaml"), tabbed)
	path := filepath.Join(broken, "tab.yaml")
	code, stdout, stderr := runPlanCLI(t, "", "-f", path)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stdout = %q", code, stdout)
	}
	wantStderr := "VAR-PLAN-0003: " + path + ": document 1: yaml: line 4: found character that cannot start any token\n"
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

// TestPlanInputSizeLimit covers the input cap at the CLI boundary: a file of
// exactly the cap is read, one byte more is a manifest error. Source:
// internal/cli/plan.go parseInput (the cap is applied to every input) and
// docs/plan.md (an input larger than the 8 MiB cap is VAR-PLAN-0003).
func TestPlanInputSizeLimit(t *testing.T) {
	directory := t.TempDir()
	atLimit := filepath.Join(directory, "at-limit.yaml")
	edgeWriteFile(t, atLimit, "#"+strings.Repeat("a", ingress.MaxInputBytes-1))
	document := edgePlan(t, "", "-f", atLimit, "--format", "json")
	if len(document.Cases) != 0 {
		t.Fatalf("cases = %#v, want none", document.Cases)
	}

	overLimit := filepath.Join(directory, "over-limit.yaml")
	edgeWriteFile(t, overLimit, "#"+strings.Repeat("a", ingress.MaxInputBytes))
	code, stdout, stderr := runPlanCLI(t, "", "-f", overLimit)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stdout = %q", code, stdout)
	}
	wantStderr := fmt.Sprintf("VAR-PLAN-0003: %s: manifest is larger than the %d-byte limit varco reads\n", overLimit, ingress.MaxInputBytes)
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}

// TestPlanInputClassFilter covers the two ways an Ingress declares its
// class: spec.ingressClassName and the legacy kubernetes.io/ingress.class
// annotation. The --ingress-class flag matches either one. Source:
// docs/plan.md (the --ingress-class flag matches spec.ingressClassName or
// the kubernetes.io/ingress.class annotation) and internal/plan/generate.go
// matchesClass.
func TestPlanInputClassFilter(t *testing.T) {
	root := filepath.Join("testdata", "edge", "class")
	document := edgePlan(t, "", "-f", root, "--ingress-class", "nginx", "--format", "json")
	want := []string{"class-annotation.example.com", "class-spec.example.com", edgeSyntheticHost}
	if got := edgeHosts(document); !reflect.DeepEqual(got, want) {
		t.Fatalf("hosts = %#v, want %#v", got, want)
	}
	other := edgePlan(t, "", "-f", root, "--ingress-class", "traefik", "--format", "json")
	if len(other.Cases) != 0 {
		t.Fatalf("cases = %#v, want none for an unmatched class", other.Cases)
	}
}

// TestPlanInputScalarEdges covers the hosts and paths the planner cannot turn
// into a request: a wildcard host is planned under a concrete label, and a
// path with a space is dropped instead of becoming a malformed request.
// Sources: internal/plan/generate.go resolveHosts (a wildcard host is planned
// as varco-probe.<zone>) and validateCase (a path with a space is dropped),
// docs/plan.md (the VAR-PLAN-0004 warning and the findings array).
func TestPlanInputScalarEdges(t *testing.T) {
	root := filepath.Join("testdata", "edge", "scalars")
	document := edgePlan(t, "", "-f", root, "--format", "json")
	wantHosts := []string{"spaced.example.com", "varco-probe.example.com", edgeSyntheticHost}
	if got := edgeHosts(document); !reflect.DeepEqual(got, wantHosts) {
		t.Fatalf("hosts = %#v, want %#v", got, wantHosts)
	}
	wantWarning := plan.Warning{
		Code:    diag.CodePlanWildcardHost,
		Ingress: "edge/wildcard",
		Message: `wildcard host "*.example.com" is planned as "varco-probe.example.com"`,
	}
	if !reflect.DeepEqual(document.Warnings, []plan.Warning{wantWarning}) {
		t.Fatalf("warnings = %#v, want %#v", document.Warnings, []plan.Warning{wantWarning})
	}
	wantFinding := plan.Finding{
		Code:    diag.CodePlanManifest,
		Ingress: "edge/spaced",
		Path:    "/a b",
		Message: `path "/a b" contains a control character or space; varco drops this case`,
	}
	found := false
	for _, finding := range document.Unsupported {
		if finding == wantFinding {
			found = true
		}
	}
	if !found {
		t.Fatalf("unsupported = %#v, want the finding %#v", document.Unsupported, wantFinding)
	}
	for _, item := range document.Cases {
		if strings.ContainsRune(item.Path, ' ') {
			t.Fatalf("case path = %q, want no case with a space", item.Path)
		}
	}
}

// TestWalkableManifestRejectsDeviceFiles covers the walk filter for the
// character device of the platform: it is not a regular file, so the walk
// skips it. Source: internal/cli/plan.go walkableManifest (only regular
// files are read) and docs/plan.md (other non-regular files are skipped).
func TestWalkableManifestRejectsDeviceFiles(t *testing.T) {
	info, err := os.Stat(os.DevNull)
	if err != nil {
		t.Skipf("the platform has no null device: %v", err)
	}
	if info.Mode().IsRegular() {
		t.Fatalf("%s is a regular file, want a device", os.DevNull)
	}
	if walkableManifest(fakeDirEntry{name: "null", mode: info.Mode().Type()}) {
		t.Fatalf("walkableManifest() accepted %s, want it skipped", os.DevNull)
	}
}
