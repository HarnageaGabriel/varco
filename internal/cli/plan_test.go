package cli

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/ingress"
)

// planInputDir is the shared manifest corpus. Sources: the ingress-nginx
// retirement guidance
// https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate and
// docs/adr/0005-plan-case-families.md.
func planInputDir() string { return filepath.Join("..", "..", "testdata", "plan") }

func runPlanCLI(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(append([]string{"plan"}, args...), strings.NewReader(stdin), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// TestPlanJSONMatchesGolden ties the CLI to the plan package: the JSON for each
// golden input must equal the committed golden file.
func TestPlanJSONMatchesGolden(t *testing.T) {
	for _, name := range []string{
		"multi-regex-host.yaml",
		"rewrite-cors.yaml",
		"wildcard-unsupported.yaml",
		"app-root-redirect.yaml",
		"list-and-others.json",
		"hostless.yaml",
	} {
		t.Run(name, func(t *testing.T) {
			input := filepath.Join(planInputDir(), name)
			goldenPath := filepath.Join(planInputDir(), strings.TrimSuffix(name, filepath.Ext(name))+".golden.json")
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatal(err)
			}
			code, stdout, stderr := runPlanCLI(t, "", "-f", input, "--format", "json")
			if code != 0 {
				t.Fatalf("exit code = %d; stderr = %q", code, stderr)
			}
			if stdout != string(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))) {
				t.Fatalf("plan JSON for %s differs from %s:\n%s", name, goldenPath, stdout)
			}
		})
	}
}

func TestPlanTableOutput(t *testing.T) {
	input := filepath.Join(planInputDir(), "wildcard-unsupported.yaml")
	code, stdout, stderr := runPlanCLI(t, "", "-f", input, "--format", "table")
	if code != 0 {
		t.Fatalf("exit code = %d; stderr = %q", code, stderr)
	}
	for _, want := range []string{"CASES 6", "varco-probe.example.com", "unmatched-route", "default-backend", "VAR-PLAN-0002", "VAR-PLAN-0004", "VAR-PLAN-0005"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("table output %q does not contain %q", stdout, want)
		}
	}
}

func TestPlanReadsStdin(t *testing.T) {
	stdin := `
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  namespace: piped
  name: piped
spec:
  rules:
    - host: piped.example.com
      http:
        paths:
          - path: /piped
            pathType: Prefix
`
	code, stdout, stderr := runPlanCLI(t, stdin, "-f", "-", "--format", "json")
	if code != 0 {
		t.Fatalf("exit code = %d; stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "piped.example.com") || !strings.Contains(stdout, `"/piped"`) {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestPlanWalksDirectories(t *testing.T) {
	directory := filepath.Join(planInputDir(), "dir")
	code, stdout, stderr := runPlanCLI(t, "", "-f", directory, "--format", "table")
	if code != 0 {
		t.Fatalf("exit code = %d; stderr = %q", code, stderr)
	}
	for _, want := range []string{"alpha.example.com", "beta.example.com", "gamma.example.com"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("table output %q does not contain %q", stdout, want)
		}
	}
	if strings.Contains(stdout, "ignored.txt") || strings.Contains(stdout, "CASES 0") {
		t.Fatalf("directory walk planned the wrong files: %q", stdout)
	}
}

func TestPlanDeduplicatesRepeatedInputs(t *testing.T) {
	input := filepath.Join(planInputDir(), "rewrite-cors.yaml")
	_, once, _ := runPlanCLI(t, "", "-f", input, "--format", "json")
	_, twice, _ := runPlanCLI(t, "", "-f", input, "-f", input, "--format", "json")
	if once != twice {
		t.Fatal("listing the same manifest twice changed the plan")
	}
}

func TestPlanDefaultHostAndClass(t *testing.T) {
	input := filepath.Join(planInputDir(), "hostless.yaml")
	code, stdout, stderr := runPlanCLI(t, "", "-f", input, "--default-host", "internal.svc", "--ingress-class", "nginx", "--format", "table")
	if code != 0 {
		t.Fatalf("exit code = %d; stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "internal.svc") {
		t.Fatalf("stdout = %q", stdout)
	}
	code, stdout, _ = runPlanCLI(t, "", "-f", input, "--default-host", "internal.svc", "--ingress-class", "traefik", "--format", "json")
	if code != 0 || !strings.Contains(stdout, `"cases": []`) {
		t.Fatalf("exit code = %d, stdout = %q", code, stdout)
	}
}

func TestPlanUsageErrors(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{name: "no input", args: nil, wantStderr: "at least one -f"},
		{name: "bad format", args: []string{"-f", filepath.Join(planInputDir(), "rewrite-cors.yaml"), "--format", "yaml"}, wantStderr: "--format must be table or json"},
		{name: "positional argument", args: []string{"manifest.yaml"}, wantStderr: "no positional arguments"},
		{name: "missing file", args: []string{"-f", filepath.Join(planInputDir(), "absent.yaml")}, wantStderr: "cannot read"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, stdout, stderr := runPlanCLI(t, "", test.args...)
			if code != 2 {
				t.Fatalf("exit code = %d, want 2", code)
			}
			if !strings.Contains(stderr, test.wantStderr) {
				t.Fatalf("stderr = %q, want %q", stderr, test.wantStderr)
			}
			if stdout != "" {
				t.Fatalf("stdout = %q, want nothing", stdout)
			}
		})
	}
}

func TestPlanManifestErrors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "extensions v1beta1", input: "reject-v1beta1.yaml.invalid"},
		{name: "malformed yaml", input: "reject-malformed.yaml.invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// An explicit -f path is read whatever its extension, while a
			// directory walk only picks up manifest extensions.
			code, stdout, stderr := runPlanCLI(t, "", "-f", filepath.Join(planInputDir(), test.input))
			if code != 2 {
				t.Fatalf("exit code = %d, want 2; stdout = %q", code, stdout)
			}
			if !strings.Contains(stderr, "VAR-PLAN-0003") {
				t.Fatalf("stderr = %q, want a VAR-PLAN-0003 diagnostic", stderr)
			}
		})
	}
}

// TestWalkableManifest covers fix H's walk filter: only regular files with a
// manifest extension are read from a directory.
func TestWalkableManifest(t *testing.T) {
	tests := []struct {
		name string
		mode fs.FileMode
		want bool
	}{
		{name: "a.yaml", want: true},
		{name: "a.yml", want: true},
		{name: "a.json", want: true},
		{name: "a.txt", want: false},
		{name: "a.yaml", mode: fs.ModeDir, want: false},
		{name: "a.yaml", mode: fs.ModeSymlink, want: false},
		{name: "a.yaml", mode: fs.ModeNamedPipe, want: false},
	}
	for _, test := range tests {
		if got := walkableManifest(fakeDirEntry{name: test.name, mode: test.mode}); got != test.want {
			t.Fatalf("walkableManifest(%q, %v) = %v, want %v", test.name, test.mode, got, test.want)
		}
	}
}

type fakeDirEntry struct {
	name string
	mode fs.FileMode
}

func (f fakeDirEntry) Name() string               { return f.name }
func (f fakeDirEntry) IsDir() bool                { return f.mode.IsDir() }
func (f fakeDirEntry) Type() fs.FileMode          { return f.mode.Type() }
func (f fakeDirEntry) Info() (fs.FileInfo, error) { return nil, nil }

// TestPlanRejectsOversizedFile covers fix H: an input larger than the 8 MiB cap
// is a VAR-PLAN-0003 error, not a partial plan.
func TestPlanRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.yaml")
	content := "#" + strings.Repeat("a", ingress.MaxInputBytes)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runPlanCLI(t, "", "-f", path)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stdout = %q", code, stdout)
	}
	if !strings.Contains(stderr, "VAR-PLAN-0003") {
		t.Fatalf("stderr = %q, want a VAR-PLAN-0003 diagnostic", stderr)
	}
}

// TestPlanBoundsEndlessStdin covers the io.LimitReader cap: an endless stream is
// cut off and rejected instead of read forever.
func TestPlanBoundsEndlessStdin(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"plan", "-f", "-"}, endlessReader{}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "VAR-PLAN-0003") {
		t.Fatalf("stderr = %q, want a VAR-PLAN-0003 diagnostic", stderr.String())
	}
}

type endlessReader struct{}

func (endlessReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	return len(p), nil
}

func TestPlanHelp(t *testing.T) {
	code, _, stderr := runPlanCLI(t, "", "-h")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stderr, "-f") {
		t.Fatalf("stderr = %q", stderr)
	}
}
