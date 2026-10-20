package plan_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/ingress"
	"github.com/HarnageaGabriel/varco/internal/plan"
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
		"class-filter.yaml",
	}
}

func genericGoldenInputs() []string {
	return []string{
		"multi-regex-host.yaml",
		"rewrite-cors.yaml",
	}
}

// classGoldenInputs are the fixtures planned with a class filter: the plan
// reports every Ingress the filter left out with a VAR-PLAN-0011 warning.
//
// Sources: docs/plan.md (the --ingress-class flag matches spec.ingressClassName
// or the kubernetes.io/ingress.class annotation) and internal/plan/generate.go
// (the class filter drops the resources that do not match).
func classGoldenInputs() []string {
	return []string{
		"class-filter.yaml",
	}
}

func goldenPath(input string) string {
	return filepath.Join("..", "..", "testdata", "plan", strings.TrimSuffix(input, filepath.Ext(input))+".golden.json")
}

func genericGoldenPath(input string) string {
	return filepath.Join("..", "..", "testdata", "plan", strings.TrimSuffix(input, filepath.Ext(input))+".generic.golden.json")
}

func classGoldenPath(input string) string {
	return filepath.Join("..", "..", "testdata", "plan", strings.TrimSuffix(input, filepath.Ext(input))+".class.golden.json")
}

func renderPlanJSON(result plan.Plan) ([]byte, error) {
	document := struct {
		SchemaVersion int            `json:"schema_version"`
		Profile       string         `json:"profile"`
		Cases         []plan.Case    `json:"cases"`
		Unsupported   []plan.Finding `json:"unsupported"`
		Warnings      []plan.Warning `json:"warnings"`
	}{
		SchemaVersion: 1,
		Profile:       result.Profile,
		Cases:         result.Cases,
		Unsupported:   result.Unsupported,
		Warnings:      result.Warnings,
	}
	if document.Cases == nil {
		document.Cases = []plan.Case{}
	}
	if document.Unsupported == nil {
		document.Unsupported = []plan.Finding{}
	}
	if document.Warnings == nil {
		document.Warnings = []plan.Warning{}
	}
	var rendered bytes.Buffer
	encoder := json.NewEncoder(&rendered)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		return nil, err
	}
	return rendered.Bytes(), nil
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
			assertGoldenPlan(t, input, plan.Options{}, goldenPath(input))
		})
	}
}

func TestGenericGoldenPlans(t *testing.T) {
	for _, input := range genericGoldenInputs() {
		t.Run(input, func(t *testing.T) {
			assertGoldenPlan(t, input, plan.Options{Profile: plan.ProfileGeneric}, genericGoldenPath(input))
		})
	}
}

// TestClassFilterGoldenPlans proves the plan of a class-filtered corpus: every
// Ingress the filter left out is reported with a VAR-PLAN-0011 warning, and
// the warning names the Ingress, the class it declares and the class that was
// requested.
//
// Sources: docs/plan.md (Known limitations: the excluded resource is dropped
// and the regex scope is computed over the included resources only) and
// internal/plan/generate.go (the class filter and the exclusion warning).
func TestClassFilterGoldenPlans(t *testing.T) {
	for _, input := range classGoldenInputs() {
		t.Run(input, func(t *testing.T) {
			assertGoldenPlan(t, input, plan.Options{IngressClass: "nginx"}, classGoldenPath(input))
		})
	}
}

func assertGoldenPlan(t *testing.T, input string, options plan.Options, path string) {
	t.Helper()
	resources := parseFile(t, input)
	first, err := renderPlanJSON(plan.Build(resources, options))
	if err != nil {
		t.Fatal(err)
	}
	second, err := renderPlanJSON(plan.Build(resources, options))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("planning the same input twice produced different JSON")
	}
	if *update {
		if err := os.WriteFile(path, first, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run `go test ./internal/plan -update` to create it)", err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), first) {
		t.Fatalf("golden mismatch for %s; run `go test ./internal/plan -update` and review:\n%s", input, first)
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
