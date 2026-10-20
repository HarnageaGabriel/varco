# JSON Schemas

varco publishes one JSON Schema for each of its two machine-readable outputs.
Both documents are plain [JSON Schema draft 2020-12](https://json-schema.org/specification-links#2020-12)
files, and both carry `schema_version: 1`.

| Output | Schema | `$id` |
|---|---|---|
| `varco plan --format json` | [plan.schema.json](plan.schema.json) | `https://github.com/HarnageaGabriel/varco/tree/main/docs/schema/plan.schema.json` |
| `varco run --format json` | [run-report.schema.json](run-report.schema.json) | `https://github.com/HarnageaGabriel/varco/tree/main/docs/schema/run-report.schema.json` |

The schemas describe the envelope only: the field names, their types, the
closed lists of outcomes, verdicts, case families, methods and body policies,
and which fields are required. They say nothing about the values inside a
field.

## Validating an output

Any standard draft 2020-12 validator can check a document against a schema.
Two verified examples:

```sh
# Python, check-jsonschema
pip install check-jsonschema
check-jsonschema --schemafile docs/schema/plan.schema.json plan.json
check-jsonschema --schemafile docs/schema/run-report.schema.json report.json
```

```sh
# Node, ajv-cli
npx ajv-cli@5 validate -s docs/schema/plan.schema.json -d plan.json --spec=draft2020
npx ajv-cli@5 validate -s docs/schema/run-report.schema.json -d report.json --spec=draft2020
```

`ajv-cli` reads the data file by its extension, so the file it validates has
to end in `.json`. `check-jsonschema` accepts any extension and globs.

## Using the schemas in CI

A CI job that keeps the published schemas honest needs no new dependency of
its own: the repository test suite already checks every golden against the
schema published for it (see below), and a job that validates a real
`varco plan --format json` or `varco run --format json` output only needs a
standard validator.

```yaml
- name: Validate the published schemas
  run: |
    pip install check-jsonschema
    check-jsonschema --schemafile docs/schema/plan.schema.json 'testdata/plan/*.golden.json' 'testdata/e2e/plan-*.json'
    check-jsonschema --schemafile docs/schema/run-report.schema.json 'testdata/e2e/*.json.golden' 'testdata/run/json.golden'
```

The in-repository proof lives in `internal/schematest`. It validates every
JSON golden of the plan and run output against its schema with a small
standard-library validator, and it proves that the schemas reject a missing
required field, a wrong type and an unknown enum value. The validator
implements only the keywords these schemas use (`type`, `const`, `enum`,
`required`, `properties`, `additionalProperties`, `items` and a local `$ref`
into `#/$defs`), so it is not a general JSON Schema implementation; use a
standard validator for anything else.

## Compatibility promise

- `schema_version` stays `1` for an additive change. varco bumps it only for a
  change a consumer cannot absorb: a removed or renamed field, a changed
  type, or a changed meaning.
- An additive change at the envelope level - a new top-level field such as
  `profile` - keeps `schema_version` at `1`. The envelope allows additional
  properties, so a consumer that ignores the fields it does not know keeps
  working.
- The fields varco added over time are optional in the schema: `profile` in
  the plan, and `repeat`, `body_policy`, `excluded`, `redacted` and the
  per-case `explanations` in the run report. A document written before one of
  them existed still matches.
- The objects inside the envelope - a case, a source, a header, a finding, a
  warning, a coverage block, an evaluation, a difference and an explanation -
  list their known properties and the required ones, and also allow additional
  properties. A new optional field in one of them is therefore an additive
  change that keeps `schema_version` at `1` and does not break a consumer
  validating with an older schema. Typos in a field name are not reported by
  the schema; the required fields and the closed enums still are.
- The schema files live in this directory and evolve with the tool. A
  consumer should use the schema file that ships with the varco release it
  uses and pin it.

## Not covered

The table output of `varco plan` and `varco run` is not machine-readable and
has no schema. The JSON envelope of `varco diff` has no schema yet either.
