# Contributor guides

Task-focused guides for the changes varco accepts from outside. Every claim in
them was checked against the code in this repository; where a guide names a
function, a flag or a file, that name exists today.

Read [CONVENTIONS.md](../../CONVENTIONS.md) (mandatory for every contributor,
human or AI agent), [CONTRIBUTING.md](../../CONTRIBUTING.md) and the ADRs in
[docs/adr](../adr) before you open a pull request.

## The guides

| Guide | Goal | Open issues it serves |
|---|---|---|
| [setup.md](setup.md) | Build, test and lint the repository the way CI does: the Go version from `go.mod`, every make target, the `-update` flag for the goldens, the Windows notes and the DCO sign-off. | [#17](https://github.com/HarnageaGabriel/varco/issues/17), [#13](https://github.com/HarnageaGabriel/varco/issues/13), [#14](https://github.com/HarnageaGabriel/varco/issues/14), [#20](https://github.com/HarnageaGabriel/varco/issues/20) |
| [add-case-family.md](add-case-family.md) | Add a request shape to the planner: the family constant, where the case is emitted, the profile rules, the goldens, the `docs/plan.md` row, the README row and the `--family` flag. | [#12](https://github.com/HarnageaGabriel/varco/issues/12), [#19](https://github.com/HarnageaGabriel/varco/issues/19) |
| [add-diagnostic.md](add-diagnostic.md) | Give a condition a stable `VAR-` code in the registry, generate its documentation page with `tools/docsgen` and keep `make docs-check` green. | [#15](https://github.com/HarnageaGabriel/varco/issues/15), [#16](https://github.com/HarnageaGabriel/varco/issues/16) |
| [add-explanation-rule.md](add-explanation-rule.md) | Explain a known difference with a stable rule id, a suggested fix and a citable source, and keep the package at 100 % coverage. | [#15](https://github.com/HarnageaGabriel/varco/issues/15), [#16](https://github.com/HarnageaGabriel/varco/issues/16) |
| [add-report-format.md](add-report-format.md) | Add a new `varco run` output format: where the renderer lives, how `--format` is validated, the redaction obligations of ADR 0006 and the JSON schema in `docs/schema`. | [#10](https://github.com/HarnageaGabriel/varco/issues/10), [#11](https://github.com/HarnageaGabriel/varco/issues/11) |

## The open issues these guides serve

The issues below are the open `good first issue` and `help wanted` issues in
this repository, with the guide that helps you start. Get the live list with:

```sh
gh issue list --state open --limit 50
```

| Issue | Title | Labels | Guide |
|---|---|---|---|
| [#10](https://github.com/HarnageaGabriel/varco/issues/10) | run: JUnit XML report | `help wanted`, `area/run`, `size/M` | [add-report-format.md](add-report-format.md) |
| [#11](https://github.com/HarnageaGabriel/varco/issues/11) | run: Markdown summary for GitHub Actions step summary | `good first issue`, `area/run`, `size/S` | [add-report-format.md](add-report-format.md) |
| [#12](https://github.com/HarnageaGabriel/varco/issues/12) | plan: derive samples for more regex path forms | `help wanted`, `area/plan`, `size/M` | [add-case-family.md](add-case-family.md) |
| [#13](https://github.com/HarnageaGabriel/varco/issues/13) | e2e: kind cluster with ingress-nginx and Envoy Gateway side by side | `help wanted`, `area/e2e`, `size/L` | [setup.md](setup.md) |
| [#14](https://github.com/HarnageaGabriel/varco/issues/14) | e2e: Traefik as a second target controller | `help wanted`, `area/e2e`, `size/M` | [setup.md](setup.md) |
| [#15](https://github.com/HarnageaGabriel/varco/issues/15) | probe: report the TLS versions an endpoint accepts | `help wanted`, `area/probe`, `size/M` | [add-diagnostic.md](add-diagnostic.md), [add-explanation-rule.md](add-explanation-rule.md) |
| [#16](https://github.com/HarnageaGabriel/varco/issues/16) | probe: detect HTTP/2 (ALPN) differences | `help wanted`, `area/probe`, `size/M` | [add-diagnostic.md](add-diagnostic.md), [add-explanation-rule.md](add-explanation-rule.md) |
| [#17](https://github.com/HarnageaGabriel/varco/issues/17) | ci: macOS job and arm64 cross-builds | `good first issue`, `area/ci`, `size/S` | [setup.md](setup.md) |
| [#18](https://github.com/HarnageaGabriel/varco/issues/18) | docs: JSON Schema for the exceptions file | `good first issue`, `area/run`, `area/docs`, `size/S` | [docs/schema](../schema/README.md) and [docs/run.md](../run.md) |
| [#19](https://github.com/HarnageaGabriel/varco/issues/19) | fixtures: real-world Ingress manifests (anonymized) | `help wanted`, `good first issue`, `area/plan`, `size/S` | [add-case-family.md](add-case-family.md) |
| [#20](https://github.com/HarnageaGabriel/varco/issues/20) | cli: shell completion | `good first issue`, `area/run`, `size/S` | [setup.md](setup.md) |

Two labels mark where to look for work, and both are live queries:

- [`good first issue`](https://github.com/HarnageaGabriel/varco/issues?q=is%3Aopen+label%3A%22good+first+issue%22)
- [`help wanted`](https://github.com/HarnageaGabriel/varco/issues?q=is%3Aopen+label%3A%22help+wanted%22)

The labels `area/plan`, `area/run`, `area/probe`, `area/e2e`, `area/ci` and
`area/docs` say which part of the tool a change touches; each guide names the
package it works in.

## Where the rules live

| Document | What it decides |
|---|---|
| [CONVENTIONS.md](../../CONVENTIONS.md) | safety, determinism, layering, diagnostics, output, tests and the review protocol. Mandatory for every contributor. |
| [CONTRIBUTING.md](../../CONTRIBUTING.md) | the DCO sign-off and the development workflow. |
| [docs/adr/](../adr) | the architecture decisions. Each guide names the ADR to read. |
| [docs/plan.md](../plan.md) | the case families and what each one probes. |
| [docs/run.md](../run.md) | the run report, the verdict model and the explanation rules. |
| [docs/schema/README.md](../schema/README.md) | the published JSON schemas and the compatibility promise. |
| [docs/maintainers.md](../maintainers.md) | the required checks and the maintainer workflow. |

## Before you open a pull request

```sh
go build ./...
go test ./...
golangci-lint run ./...
go run ./tools/docsgen -out docs/diagnostics -check
```

Then sign the commit off with `git commit -s`, comment on the issue before you
start so nobody duplicates the work, and explain every semantic claim with a
source: a reviewer rejects a claim without one
([CONVENTIONS.md](../../CONVENTIONS.md)).
