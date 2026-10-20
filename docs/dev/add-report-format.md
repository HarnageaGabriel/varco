# Add a run report format

Goal: add a new output format to `varco run` (for example JUnit XML or a
Markdown summary) that is deterministic, redacted like the existing formats and
covered by a golden.

The rules live in [ADR 0006: redaction](../adr/0006-redaction.md) and
[ADR 0007: verdict model](../adr/0007-verdict-model.md). Read both, and
[CONVENTIONS.md](../../CONVENTIONS.md) (mandatory for every contributor), before
you start.

This guide serves the open issues
[#10](https://github.com/HarnageaGabriel/varco/issues/10)
(`run: JUnit XML report`) and
[#11](https://github.com/HarnageaGabriel/varco/issues/11)
(`run: Markdown summary for GitHub Actions step summary`). The other guides are
in the [dev guide index](README.md).

## Where the renderer lives

`internal/report` is the package that renders output. It is **pure**: no I/O
beyond the writer it is handed. The lint configuration
([.golangci.yml](../../.golangci.yml)) forbids `net/http`, `os` and
`os/exec` in `internal/report`, and `forbidigo` forbids `time.Now`, so a
renderer cannot read a file, open a socket or read the clock. Time only
arrives through the injected clock in the engine.

The exported renderers today:

| Function | File | Used by |
|---|---|---|
| `Table(w io.Writer, differences []compare.Difference, options Options) error` | [internal/report/report.go](../../internal/report/report.go) | `varco diff` |
| `JSON(w io.Writer, differences []compare.Difference, options Options) error` | [internal/report/report.go](../../internal/report/report.go) | `varco diff` |
| `PlanTable(w io.Writer, result plan.Plan) error` | [internal/report/plan.go](../../internal/report/plan.go) | `varco plan` |
| `PlanJSON(w io.Writer, result plan.Plan) error` | [internal/report/plan.go](../../internal/report/plan.go) | the plan JSON path |
| `RunTable(w io.Writer, report model.RunReport, options Options) error` | [internal/report/run.go](../../internal/report/run.go) | `varco run` |
| `RunJSON(w io.Writer, report model.RunReport, options Options) error` | [internal/report/run.go](../../internal/report/run.go) | `varco run` |

A new run renderer follows the same shape:

```go
// RunJUnit writes the run report as a JUnit XML testsuite.
func RunJUnit(w io.Writer, report model.RunReport, options Options) error
```

The report type is `model.RunReport` in
[internal/model/model.go](../../internal/model/model.go), not a type of
`internal/report`:

```go
type RunReport struct {
	SchemaVersion     int            `json:"schema_version"`
	Verdict           Outcome        `json:"verdict"`
	Coverage          Coverage       `json:"coverage"`
	Repeat            int            `json:"repeat"`
	BodyPolicy        string         `json:"body_policy"`
	Excluded          int            `json:"excluded"`
	UnsupportedCount  int            `json:"unsupported_count"`
	Unsupported       []plan.Finding `json:"unsupported"`
	ExpiredExceptions []string       `json:"expired_exceptions"`
	IgnoredFields     []string       `json:"ignored_fields"`
	Cases             []Evaluation   `json:"cases"`
	Redacted          bool           `json:"redacted"`

	// Unstable lists the (host, field) pairs observed unstable, in stable order.
	// It drives --suggest-exceptions and is not part of the JSON contract.
	Unstable []UnstableField `json:"-"`
}
```

## How `--format` is validated

There is no `Format` type and no format registry. The format is a plain
string, compared with `==` in the command handlers.

The flag is declared in three places, once per subcommand, with the same
default and help text:

| Command | Declaration |
|---|---|
| `varco run` | `format := flags.String("format", "table", "table or json")` in [internal/cli/run.go](../../internal/cli/run.go) |
| `varco plan` | the same line in [internal/cli/plan.go](../../internal/cli/plan.go) |
| `varco diff` | the same line in [internal/cli/cli.go](../../internal/cli/cli.go) |

The validation is also duplicated in the three handlers, byte for byte:

```go
if *format != "table" && *format != "json" {
	return configFailure(stderr, "--format must be table or json")
}
```

`configFailure` in [internal/cli/cli.go](../../internal/cli/cli.go) prints the
diagnostic and returns exit code 2. Verified:

```sh
go run ./cmd/varco run --plan - --format junit --old 127.0.0.1:1 --new 127.0.0.1:1
```

```
VAR-CFG-0001: --format must be table or json
exit status 2
```

So a new format means: extend the three checks, extend the three help
strings, and update the message. The message is asserted as an exact string
in [internal/cli/plan_test.go](../../internal/cli/plan_test.go):

```go
{name: "bad format", args: []string{"-f", filepath.Join(planInputDir(), "rewrite-cors.yaml"), "--format", "yaml"}, wantStderr: "--format must be table or json"},
```

A cleaner change is to introduce one shared validator in `internal/cli` and
call it from the three handlers, but that is a refactor of shared code: keep it
in a separate pull request if the issue asks for one format.

## Where the renderer is dispatched

For `varco run` the dispatch is `writeRunReportTo` in
[internal/cli/run.go](../../internal/cli/run.go):

```go
// writeRunReportTo renders the run report in the requested format.
func writeRunReportTo(format string, runReport model.RunReport, options report.Options, writer io.Writer) error {
	if format == "json" {
		return report.RunJSON(writer, runReport, options)
	}
	return report.RunTable(writer, runReport, options)
}
```

A new format is one more case here. The output file handling sits above it in
`writeRunReport`, which opens `--output` with mode `0o600` and joins the write
and the close error, so a report that only fails at close is never reported as
written.

For `varco plan` the dispatch is inline in `runPlan`
([internal/cli/plan.go](../../internal/cli/plan.go)), and for `varco diff` it is
inline in `runDiff` ([internal/cli/cli.go](../../internal/cli/cli.go)).

## The redaction obligations

ADR 0006 decides that redaction happens **immediately before serialization**.
The report layer owns that, and a new renderer inherits it.

The single choke point for a difference value is `displayValue` in
[internal/report/report.go](../../internal/report/report.go):

```go
// displayValue redacts a difference's two display values when they are
// sensitive and the caller did not opt in. Per ADR 0006 it only redacts the
// Location header; every other field is printed as observed.
func displayValue(field, oldValue, newValue string, options Options) (string, string) {
	if options.ShowSensitive || field != "header.location" {
		return oldValue, newValue
	}
	redactedOld := redact.URL(oldValue)
	redactedNew := redact.URL(newValue)
	if oldValue != newValue && redactedOld == redactedNew {
		redactedNew += redactedDifferenceNote(oldValue, newValue)
	}
	return redactedOld, redactedNew
}
```

`redact.URL` and `redact.Text` live in
[internal/redact](../../internal/redact). `redact.URL` removes the userinfo,
every query value and the fragment, replacing each with the literal
`REDACTED`; scheme, host, path, query keys and the query key order stay
visible.

A new renderer has four obligations:

1. **Route every difference value through `displayValue`.** The table calls it
   per difference; the JSON renderer calls it in `displayReport`. A renderer
   that prints `difference.Old` and `difference.New` directly leaks the
   secret.
2. **Keep the "the hidden values differ" note.** When two raw locations differ
   but their redacted forms are equal, the difference must stay visible with
   the note (`(query values differ)`, `(fragments differ)`,
   `(userinfo differs)` or `(sensitive values differ)`). Dropping the note
   hides the fact that something changed.
3. **Redact free text.** A case error goes through `redact.Text`, as
   `runError` does in [internal/report/run.go](../../internal/report/run.go).
4. **State the redaction choice.** The JSON report records it in the top-level
   `redacted` boolean, set by `displayReport`:

```go
func displayReport(report model.RunReport, options Options) model.RunReport {
	display := report
	display.Redacted = !options.ShowSensitive
	...
}
```

A format that has no such field needs an equivalent statement, otherwise a
reader cannot tell whether the values are raw.

`--show-sensitive` is the escape hatch and its output is credential-bearing.
The CLI prints the warning, not the report layer:

```go
if *showSensitive {
	_, _ = fmt.Fprintln(stderr, "warning: --show-sensitive exposes raw sensitive values")
}
```

Two more ADR 0006 rules that a new format inherits: cookie **values** are
never compared and never rendered (only cookie names and the attributes
`Secure`, `HttpOnly`, `SameSite`, `Path`, `Domain`, the presence of `Max-Age`
and `Expires`, and `Partitioned`), and a probe failure reports a request path
by length only.

## The JSON schema

The published schemas live in [docs/schema](../schema). There is one per
machine-readable output:

| Output | Schema |
|---|---|
| `varco plan --format json` | [plan.schema.json](../schema/plan.schema.json) |
| `varco run --format json` | [run-report.schema.json](../schema/run-report.schema.json) |

[docs/schema/README.md](../schema/README.md) states the compatibility promise:

- `schema_version` stays `1` for an additive change, and varco bumps it only
  for a change a consumer cannot absorb: a removed or renamed field, a changed
  type or a changed meaning.
- The envelope allows additional properties, so a new top-level field is an
  additive change.
- The inner objects (case, source, header, finding, warning, coverage block,
  evaluation, difference, explanation) also allow additional properties.

So:

- A **new JSON format** (a third JSON document) needs a **new schema file**,
  a `$id`, a `title` and a `description`, an entry in the `goldenSources`
  table in [internal/schematest/schema_test.go](../../internal/schematest/schema_test.go),
  and a row in the table in [docs/schema/README.md](../schema/README.md).
- A **non-JSON format** (JUnit XML, Markdown) needs **no** schema, but the
  sentence in [docs/schema/README.md](../schema/README.md) that says varco
  publishes *one JSON Schema for each of its two machine-readable outputs* has
  to be reworded, and the `## Not covered` section has to say that the new
  output has no schema.

The in-repository proof is [internal/schematest](../../internal/schematest),
which validates every JSON golden of the plan and run output against the
schema published for it:

```go
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
```

A JSON golden in a new place that is not listed here is not schema-checked.

## The goldens

The run report goldens live at the repository root, in
[testdata/run](../../testdata/run):

| Golden | Written by | Test |
|---|---|---|
| `testdata/run/table.golden` | `goldenRun` in [internal/report/run_test.go](../../internal/report/run_test.go) | `TestRunTableGolden` |
| `testdata/run/json.golden` | the same helper | `TestRunJSONGolden` |

The helper writes to `../../testdata/run/<name>` and only rewrites the file
when the `-update` flag is set:

```go
var update = flag.Bool("update", false, "update golden files")
```

The flag is declared once per test binary:

| Flag declared in | Writes |
|---|---|
| `internal/report/report_test.go` | `internal/report/testdata/*.golden` and `testdata/run/*.golden` |
| `internal/plan/golden_test.go` | `testdata/plan/*.golden.json` |
| `internal/e2e/helpers_test.go` | `testdata/e2e/*` |

So a new format gets its own golden, for example
`testdata/run/junit.golden`, written by a new `TestRunJUnitGolden` in
[internal/report/run_test.go](../../internal/report/run_test.go).

## The other tests a new format touches

| Test | File | Why it matters |
|---|---|---|
| `TestRunRedaction` | [internal/report/run_test.go](../../internal/report/run_test.go) | loops over `[]string{"table", "json"}`; the new format must be added |
| `TestRenderersReportWriterErrorsMidRow` | [internal/report/write_errors_test.go](../../internal/report/write_errors_test.go) | a table of renderers that must report a writer error; the new renderer belongs there |
| `TestRunCommand` (`bad format` row) | [internal/cli/run_test.go](../../internal/cli/run_test.go) | asserts `--format xml` is rejected with exit 2 |
| `TestRunDiff` (`bad format` row) | [internal/cli/diff_test.go](../../internal/cli/diff_test.go) | same, for `varco diff` |
| `TestPlanUsageErrors` (`bad format` row) | [internal/cli/plan_test.go](../../internal/cli/plan_test.go) | asserts the exact error message |
| `TestRunScenario` | [internal/e2e/e2e_test.go](../../internal/e2e/e2e_test.go) | loops over `[]string{"table", "json"}` and writes one golden per format |
| `TestPlanFailsWhenReportCannotBeWritten` | [internal/cli/plan_edges_test.go](../../internal/cli/plan_edges_test.go) | loops over the plan formats |

## Files to touch

| File | What changes |
|---|---|
| `internal/report/<format>.go` (new) | the renderer, in package `report` |
| [internal/cli/run.go](../../internal/cli/run.go) | the flag help text, the validation, and the `writeRunReportTo` dispatch |
| [internal/cli/plan.go](../../internal/cli/plan.go), [internal/cli/cli.go](../../internal/cli/cli.go) | the same, if the format is offered by `plan` or `diff` too |
| [internal/report/run_test.go](../../internal/report/run_test.go) | the golden test and the redaction loop |
| [internal/report/write_errors_test.go](../../internal/report/write_errors_test.go) | the writer-error table |
| `testdata/run/<format>.golden` (new) | the golden |
| [internal/e2e/e2e_test.go](../../internal/e2e/e2e_test.go) | the e2e golden loop, when the format is covered end to end |
| [internal/schematest/schema_test.go](../../internal/schematest/schema_test.go) | only when the format emits JSON |
| [docs/run.md](../run.md) | the `--format` flag row and the `## Report` section |
| [docs/usage.md](../usage.md) | the flag lists of `plan`, `run` and `diff` |
| [docs/cookbook.md](../cookbook.md) | the flag table |
| [docs/ci.md](../ci.md) | the example pipeline, when the format belongs there |
| [docs/schema/README.md](../schema/README.md) | the number of machine-readable outputs and the `## Not covered` section |
| [README.md](../../README.md) | the roadmap checkbox, when the format closes one of the open items |

## Worked example: `--format junit`

Issue [#10](https://github.com/HarnageaGabriel/varco/issues/10) asks for
`--format junit`. The shape of the change is the same for any format.

**1. The renderer** in a new file `internal/report/junit.go`:

```go
package report

import (
	"encoding/xml"
	"io"

	"github.com/HarnageaGabriel/varco/internal/model"
)

// RunJUnit writes the run report as a JUnit XML testsuite, one testsuite per
// host and one testcase per case.
//
// Source: the Jenkins/Ant JUnit XML schema, the flavour GitLab CI, Jenkins
// and Azure Pipelines all read.
func RunJUnit(w io.Writer, report model.RunReport, options Options) error {
	...
}
```

`encoding/xml` is in the standard library, so no new module is needed and no
dependency rule is triggered. The renderer must:

- group the cases by `evaluation.Host` into one `<testsuite>` per host;
- name a case `METHOD path [family]` and use the host as `classname`;
- turn a `DIFFERENT` case into a `<failure>` that lists the differences (code,
  field, old, new);
- turn an `INCONCLUSIVE` case into an `<error>` with the reason;
- put an accepted difference in `<system-out>` with its exception id and
  reason;
- print no timestamp unless the caller asked for one, so the output is
  deterministic.

**2. The dispatch** in [internal/cli/run.go](../../internal/cli/run.go):

```go
func writeRunReportTo(format string, runReport model.RunReport, options report.Options, writer io.Writer) error {
	switch format {
	case "json":
		return report.RunJSON(writer, runReport, options)
	case "junit":
		return report.RunJUnit(writer, runReport, options)
	default:
		return report.RunTable(writer, runReport, options)
	}
}
```

**3. The validation** in the three handlers, and the help text:

```go
if *format != "table" && *format != "json" && *format != "junit" {
	return configFailure(stderr, "--format must be table, json or junit")
}
```

`flags.String("format", "table", "table or json")` becomes
`flags.String("format", "table", "table, json or junit")` in all three
commands.

**4. The redaction.** The renderer copies the values through `displayValue`
before it writes them, exactly like `RunTable` does:

```go
for _, difference := range evaluation.Differences {
	oldValue, newValue := displayValue(difference.Field, difference.Old, difference.New, options)
	...
}
```

and the case error through `redact.Text`.

**5. The golden** `testdata/run/junit.golden`, written by a new
`TestRunJUnitGolden` next to `TestRunTableGolden` in
[internal/report/run_test.go](../../internal/report/run_test.go).

**6. The docs.** `--format (table|json)` becomes
`--format (table|json|junit)` in [docs/run.md](../run.md),
[docs/usage.md](../usage.md) and [docs/cookbook.md](../cookbook.md), and the
`## Report` section of [docs/run.md](../run.md) describes the new output.

The Markdown format of issue
[#11](https://github.com/HarnageaGabriel/varco/issues/11) follows the same
path, with a verdict line, a coverage table and a capped table of the
non-equivalent cases, and a `$GITHUB_STEP_SUMMARY` example in
[docs/run.md](../run.md).

## Tests and commands

| What | Command |
|---|---|
| regenerate the run goldens | `go test ./internal/report -update` |
| verify the run goldens | `go test ./internal/report` |
| the e2e goldens | `go test ./internal/e2e -update` then `go test ./internal/e2e` |
| the schema check | `go test ./internal/schematest` |
| the CLI tests | `go test ./internal/cli` |
| the whole tree | `go test ./...` |
| the coverage floor | `go test -cover ./internal/report` (floor: 80 %) |
| the lint gate | `golangci-lint run ./...` |
| the CLI itself | `go run ./cmd/varco run --help` |

## Checklist

- [ ] You read [ADR 0006](../adr/0006-redaction.md) and
      [ADR 0007](../adr/0007-verdict-model.md).
- [ ] The renderer lives in `internal/report`, is pure (no `net/http`, no
      `os`, no `time.Now`) and returns writer errors.
- [ ] Every difference value goes through `displayValue`, and every free-text
      error through `redact.Text`.
- [ ] The "the hidden values differ" note survives.
- [ ] The format states the redaction choice, like the `redacted` boolean.
- [ ] `--format` is accepted by the CLI, and the error message and the help
      text were updated in all three commands.
- [ ] The dispatch in `writeRunReportTo` was extended.
- [ ] A golden exists under `testdata/run/` and was regenerated with
      `go test ./internal/report -update`; the diff was read.
- [ ] `TestRunRedaction` and `TestRenderersReportWriterErrorsMidRow` cover the
      new renderer.
- [ ] The docs (`run.md`, `usage.md`, `cookbook.md`, and `schema/README.md`
      when the number of machine-readable outputs changed) were updated.
- [ ] `go test ./...` passes and `internal/report` is at or above 80 %
      coverage.
- [ ] `golangci-lint run ./...` is clean.
- [ ] The commit is signed off (`git commit -s`).
