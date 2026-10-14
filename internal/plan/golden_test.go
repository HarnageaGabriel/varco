package plan_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/ingress"
	"github.com/HarnageaGabriel/varco/internal/plan"
	"github.com/HarnageaGabriel/varco/internal/report"
)

var update = flag.Bool("update", false, "update golden files")

// goldenInputs are the realistic anonymized manifests under testdata/plan. Each
// one has a <name>.golden.json plan next to it.
//
// Sources: the ingress-nginx retirement guidance
// https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate and
// docs/adr/0005-plan-case-families.md.
func goldenInputs() []string {
	return []string{
		"multi-regex-host.yaml",
		"rewrite-cors.yaml",
		"wildcard-unsupported.yaml",
		"app-root-redirect.yaml",
		"list-and-others.json",
		"hostless.yaml",
	}
}

func goldenPath(input string) string {
	return filepath.Join("..", "..", "testdata", "plan", strings.TrimSuffix(input, filepath.Ext(input))+".golden.json")
}

func parseFile(t *testing.T, input string) []ingress.Ingress {
	t.Helper()
	file, err := os.Open(filepath.Join("..", "..", "testdata", "plan", input))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	resources, err := ingress.Parse(file)
	if err != nil {
		t.Fatalf("parse %s: %v", input, err)
	}
	return resources
}

func TestGoldenPlans(t *testing.T) {
	for _, input := range goldenInputs() {
		t.Run(input, func(t *testing.T) {
			resources := parseFile(t, input)
			first := plan.Build(resources, plan.Options{})
			second := plan.Build(resources, plan.Options{})
			var rendered bytes.Buffer
			if err := report.PlanJSON(&rendered, first); err != nil {
				t.Fatal(err)
			}
			var repeat bytes.Buffer
			if err := report.PlanJSON(&repeat, second); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(rendered.Bytes(), repeat.Bytes()) {
				t.Fatal("planning the same input twice produced different JSON")
			}
			path := goldenPath(input)
			if *update {
				if err := os.WriteFile(path, rendered.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run `go test ./internal/plan -update` to create it)", err)
			}
			if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), rendered.Bytes()) {
				t.Fatalf("golden mismatch for %s; run `go test ./internal/plan -update` and review:\n%s", input, rendered.String())
			}
		})
	}
}

// TestGoldenPlansAreNotEmpty guards against a golden that silently plans nothing.
func TestGoldenPlansAreNotEmpty(t *testing.T) {
	for _, input := range goldenInputs() {
		if input == "hostless.yaml" {
			continue
		}
		result := plan.Build(parseFile(t, input), plan.Options{})
		if len(result.Cases) == 0 {
			t.Fatalf("%s produced no cases", input)
		}
	}
}

// TestGoldenPlansCiteTheirSource keeps the study source visible in each golden:
// every input file names the blog post and the case families it covers.
func TestGoldenPlansCiteTheirSource(t *testing.T) {
	for _, input := range goldenInputs() {
		content, err := os.ReadFile(filepath.Join("..", "..", "testdata", "plan", input))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(content, []byte("https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate")) {
			t.Fatalf("%s does not cite the ingress-nginx behavior source", input)
		}
	}
}
