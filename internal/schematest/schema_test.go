package schematest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const (
	// planSchemaPath is the published schema of `varco plan --format json`.
	planSchemaPath = "../../docs/schema/plan.schema.json"
	// runSchemaPath is the published schema of `varco run --format json`.
	runSchemaPath = "../../docs/schema/run-report.schema.json"
	// runGoldenPath is the golden of a run report.
	runGoldenPath = "../../testdata/run/json.golden"
	// planGoldenPath is the golden of a plan.
	planGoldenPath = "../../testdata/plan/rewrite-cors.golden.json"
	// minimumGoldens is the number of goldens the sources below must yield at
	// least, so a lost golden fails the test.
	minimumGoldens = 25
)

// goldenSources lists the JSON goldens of the two machine-readable outputs
// and the schema each one must match.
var goldenSources = []struct {
	pattern    string
	schemaPath string
}{
	{pattern: "../../testdata/plan/*.golden.json", schemaPath: planSchemaPath},
	{pattern: "../../testdata/e2e/plan-*.json", schemaPath: planSchemaPath},
	{pattern: "../../internal/report/testdata/plan_json.golden", schemaPath: planSchemaPath},
	{pattern: "../../internal/report/testdata/plan_json_empty.golden", schemaPath: planSchemaPath},
	{pattern: "../../testdata/run/json.golden", schemaPath: runSchemaPath},
	{pattern: "../../testdata/e2e/*.json.golden", schemaPath: runSchemaPath},
}

// golden is one published output and the schema it must match.
type golden struct {
	name       string
	path       string
	schema     *schema
	schemaPath string
}

// TestGoldenOutputsMatchPublishedSchemas checks every JSON golden of the plan
// and run output against the schema published for it.
func TestGoldenOutputsMatchPublishedSchemas(t *testing.T) {
	schemas := loadSchemas(t)
	outputs := goldenOutputs(t, schemas)
	if len(outputs) < minimumGoldens {
		t.Fatalf("found %d goldens, want at least %d", len(outputs), minimumGoldens)
	}
	for _, output := range outputs {
		t.Run(output.name, func(t *testing.T) {
			document := decodeGolden(t, output.path)
			problems := output.schema.validate(document)
			if len(problems) != 0 {
				t.Fatalf("%s does not match %s:\n%s", output.path, output.schemaPath, formatProblems(problems))
			}
		})
	}
}

// TestSchemaRejectsInvalidDocuments proves that the published schemas reject
// a document with a missing required field, a wrong type and an unknown enum
// value.
func TestSchemaRejectsInvalidDocuments(t *testing.T) {
	schemas := loadSchemas(t)
	for _, test := range []struct {
		name       string
		schemaPath string
		goldenPath string
		mutate     func(t *testing.T, document map[string]any)
		want       string
	}{
		{
			name:       "missing required field",
			schemaPath: runSchemaPath,
			goldenPath: runGoldenPath,
			mutate: func(_ *testing.T, document map[string]any) {
				delete(document, "verdict")
			},
			want: "$.verdict: required property is missing",
		},
		{
			name:       "wrong type",
			schemaPath: runSchemaPath,
			goldenPath: runGoldenPath,
			mutate: func(_ *testing.T, document map[string]any) {
				document["repeat"] = "3"
			},
			want: "$.repeat: expected type integer, got string",
		},
		{
			name:       "unknown enum value",
			schemaPath: runSchemaPath,
			goldenPath: runGoldenPath,
			mutate: func(_ *testing.T, document map[string]any) {
				document["verdict"] = "MAYBE"
			},
			want: "$.verdict: MAYBE is not one of",
		},
		{
			name:       "unknown plan family",
			schemaPath: planSchemaPath,
			goldenPath: planGoldenPath,
			mutate: func(t *testing.T, document map[string]any) {
				firstCase(t, document)["family"] = "unknown-family"
			},
			want: "$.cases[0].family: unknown-family is not one of",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := decodeObject(t, test.goldenPath)
			test.mutate(t, document)
			problems := schemas[test.schemaPath].validate(document)
			if len(problems) == 0 {
				t.Fatalf("the schema accepted a document with %s", test.name)
			}
			if !hasProblem(problems, test.want) {
				t.Fatalf("want a problem containing %q, got:\n%s", test.want, formatProblems(problems))
			}
		})
	}
}

// TestSchemaAcceptsAdditiveTopLevelFields proves the compatibility promise:
// an unknown top-level field is an additive change, so the schema accepts it.
func TestSchemaAcceptsAdditiveTopLevelFields(t *testing.T) {
	schemas := loadSchemas(t)
	document := decodeObject(t, runGoldenPath)
	document["future_field"] = "added in a later release"
	problems := schemas[runSchemaPath].validate(document)
	if len(problems) != 0 {
		t.Fatalf("the schema rejected an unknown top-level field:\n%s", formatProblems(problems))
	}
}

// TestSchemaAcceptsDocumentsWithoutTheAdditiveFields proves the additive
// fields are optional: a document written before they existed still matches.
func TestSchemaAcceptsDocumentsWithoutTheAdditiveFields(t *testing.T) {
	schemas := loadSchemas(t)
	run := decodeObject(t, runGoldenPath)
	for _, field := range []string{"repeat", "body_policy", "excluded", "redacted"} {
		delete(run, field)
	}
	delete(firstCase(t, run), "explanations")
	problems := schemas[runSchemaPath].validate(run)
	if len(problems) != 0 {
		t.Fatalf("the schema rejected a report without the additive fields:\n%s", formatProblems(problems))
	}
	plan := decodeObject(t, planGoldenPath)
	delete(plan, "profile")
	problems = schemas[planSchemaPath].validate(plan)
	if len(problems) != 0 {
		t.Fatalf("the schema rejected a plan without the profile field:\n%s", formatProblems(problems))
	}
}

// TestPublishedSchemasDeclareTheirIdentity keeps the published documents
// findable and pinned to one draft.
func TestPublishedSchemasDeclareTheirIdentity(t *testing.T) {
	for _, test := range []struct {
		path string
		id   string
	}{
		{
			path: planSchemaPath,
			id:   "https://github.com/HarnageaGabriel/varco/tree/main/docs/schema/plan.schema.json",
		},
		{
			path: runSchemaPath,
			id:   "https://github.com/HarnageaGabriel/varco/tree/main/docs/schema/run-report.schema.json",
		},
	} {
		t.Run(test.path, func(t *testing.T) {
			root := decodeObject(t, test.path)
			if got := root["$schema"]; got != "https://json-schema.org/draft/2020-12/schema" {
				t.Fatalf("$schema = %v, want the draft 2020-12 dialect", got)
			}
			if got := root["$id"]; got != test.id {
				t.Fatalf("$id = %v, want %v", got, test.id)
			}
			if got, ok := root["title"].(string); !ok || got == "" {
				t.Fatalf("title = %v, want a non-empty string", root["title"])
			}
			if got, ok := root["description"].(string); !ok || got == "" {
				t.Fatalf("description = %v, want a non-empty string", root["description"])
			}
		})
	}
}

// loadSchemas loads every published schema the tests use.
func loadSchemas(t *testing.T) map[string]*schema {
	t.Helper()
	paths := []string{planSchemaPath, runSchemaPath}
	loaded := make(map[string]*schema, len(paths))
	for _, path := range paths {
		loaded[path] = loadSchemaOrFail(t, path)
	}
	return loaded
}

// loadSchemaOrFail loads one schema and fails the test when it cannot be read.
func loadSchemaOrFail(t *testing.T, path string) *schema {
	t.Helper()
	loaded, err := loadSchema(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return loaded
}

// goldenOutputs resolves the golden sources into one table.
func goldenOutputs(t *testing.T, schemas map[string]*schema) []golden {
	t.Helper()
	var outputs []golden
	for _, source := range goldenSources {
		matches, err := filepath.Glob(source.pattern)
		if err != nil {
			t.Fatalf("glob %s: %v", source.pattern, err)
		}
		if len(matches) == 0 {
			t.Fatalf("no golden matched %s", source.pattern)
		}
		for _, path := range matches {
			outputs = append(outputs, golden{
				name:       goldenName(path),
				path:       path,
				schema:     schemas[source.schemaPath],
				schemaPath: source.schemaPath,
			})
		}
	}
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].name < outputs[j].name })
	return outputs
}

// goldenName names a golden by its path inside the repository.
func goldenName(path string) string {
	return strings.TrimPrefix(filepath.ToSlash(path), "../../")
}

// decodeGolden reads a JSON file and decodes it.
func decodeGolden(t *testing.T, path string) any {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(content, &document); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return document
}

// decodeObject reads a JSON file and decodes it as an object.
func decodeObject(t *testing.T, path string) map[string]any {
	t.Helper()
	document, ok := decodeGolden(t, path).(map[string]any)
	if !ok {
		t.Fatalf("%s is not a JSON object", path)
	}
	return document
}

// firstCase returns the first case of a decoded plan document.
func firstCase(t *testing.T, document map[string]any) map[string]any {
	t.Helper()
	cases, ok := document["cases"].([]any)
	if !ok || len(cases) == 0 {
		t.Fatal("the document has no cases")
	}
	item, ok := cases[0].(map[string]any)
	if !ok {
		t.Fatal("the first case is not an object")
	}
	return item
}

// hasProblem reports whether one of the violations contains the text.
func hasProblem(problems []error, want string) bool {
	for _, problem := range problems {
		if strings.Contains(problem.Error(), want) {
			return true
		}
	}
	return false
}

// formatProblems renders the violations of a document for a failure message.
func formatProblems(problems []error) string {
	messages := make([]string, 0, len(problems))
	for _, problem := range problems {
		messages = append(messages, problem.Error())
	}
	return strings.Join(messages, "\n")
}
