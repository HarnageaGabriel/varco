# Development setup

Goal: get a checkout that builds, tests and lints the same way CI does, and
know exactly which command produces which artifact.

This guide serves the open issues
[#17](https://github.com/HarnageaGabriel/varco/issues/17)
(`ci: macOS job and arm64 cross-builds`),
[#13](https://github.com/HarnageaGabriel/varco/issues/13)
(`e2e: kind cluster with ingress-nginx and Envoy Gateway side by side`),
[#14](https://github.com/HarnageaGabriel/varco/issues/14)
(`e2e: Traefik as a second target controller`) and
[#20](https://github.com/HarnageaGabriel/varco/issues/20)
(`cli: shell completion`). The other guides are in the
[dev guide index](README.md).

Read before you start: [CONVENTIONS.md](../../CONVENTIONS.md) (mandatory for
every contributor) and [CONTRIBUTING.md](../../CONTRIBUTING.md).

## Clone and toolchain

```sh
git clone https://github.com/HarnageaGabriel/varco.git
cd varco
```

The Go version comes from [go.mod](../../go.mod), not from a README number:

```
module github.com/HarnageaGabriel/varco

go 1.26
```

So **Go 1.26 or newer** is required. CI reads the same file: the `test` and
`lint` jobs use `go-version-file: go.mod`
([.github/workflows/ci.yml](../../.github/workflows/ci.yml)), and the
`Supply-chain checks` job uses `go-version: stable`.

Check your toolchain:

```sh
go version
```

Beyond Go, the repository expects:

| Tool | Needed for | Version used by CI |
|---|---|---|
| `golangci-lint` | `make lint` | v2.14.0, pinned in [.github/workflows/ci.yml](../../.github/workflows/ci.yml) |
| `govulncheck` | `make vuln` | CI runs `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...`, so no local install is needed |
| `gofumpt` | `make fmt` | the formatter configured in [.golangci.yml](../../.golangci.yml) |
| GoReleaser v2 | `make release-check` | v7.3.0 in the release workflow |
| a POSIX shell | `make fuzz` (`sh scripts/fuzz.sh`) | the nightly workflow |

## Make targets

Every target is in the [Makefile](../../Makefile). This is the whole list, with
the command each one runs, so you can run them by hand when `make` is not
installed:

| Target | Command | Notes |
|---|---|---|
| `make build` | `go build -trimpath ./...` | no binary is written; `go build ./...` only type-checks. `make build` is what `README.md` mentions. |
| `make test` | `go test -race ./...` | needs cgo, see the Windows notes below |
| `make bench` | `go test -run ^$ -bench . -benchtime 100ms ./...` | benchmarks live in `internal/plan`, `internal/report`, `internal/compare` and `internal/engine` |
| `make fuzz` | `sh scripts/fuzz.sh` | needs a POSIX shell; the fuzz targets are `FuzzLoadPlan` (`internal/cli`), `FuzzParse` (`internal/endpoint`, `internal/ingress`), `FuzzDecodeConfigMap` and `FuzzFetchIngresses` (`internal/kube`), `FuzzLoad` (`internal/policy`), `FuzzReadResponse` (`internal/probe`), `FuzzURL` and `FuzzText` (`internal/redact`) |
| `make lint` | `golangci-lint run ./...` | |
| `make vuln` | `govulncheck ./...` | |
| `make cover` | `go test -race -covermode=atomic -coverprofile=coverage.out ./...` | writes `coverage.out` |
| `make tidy` | `go mod tidy` | |
| `make fmt` | `gofumpt -w cmd internal tools` | |
| `make docs` | `go run ./tools/docsgen -out docs/diagnostics` | regenerates the diagnostic pages, see [add-diagnostic.md](add-diagnostic.md) |
| `make docs-check` | `go run ./tools/docsgen -out docs/diagnostics -check` | the CI drift check; writes nothing |
| `make release-check` | `goreleaser check` and `goreleaser release --snapshot --clean` | fails when GoReleaser is not installed |

## Windows notes

Verified on a Windows 11 machine with Go 1.26.5.

**`make` is not installed by default.** On this machine `make --version` fails
with `make: The term 'make' is not recognized as a name of a cmdlet`. Run the
commands from the table above directly, or install GNU Make (for example
through `choco install make` or `winget install GnuWin32.Make`).

**`-race` needs a C compiler.** The race detector requires cgo, and cgo on
Windows needs gcc. With a MinGW-w64 gcc on `PATH` the race build works:

```sh
go test -race ./internal/diag
```

```
ok  	github.com/HarnageaGabriel/varco/internal/diag	1.061s
```

With no C compiler on `PATH`, Go turns cgo off and the race build stops:

```
go: -race requires cgo; enable cgo by setting CGO_ENABLED=1
```

So on Windows without gcc, run `go test ./...` locally and let CI run the
race build. CI runs the race detector on **both** `ubuntu-latest` and
`windows-latest` ([.github/workflows/ci.yml](../../.github/workflows/ci.yml)),
and the `windows-latest` image ships a C toolchain, so the race run is covered
even when your laptop has no gcc.

**`gofumpt` is not on `PATH` by default either.** `gofmt -l cmd internal tools`
prints nothing on a clean tree, so `gofmt` is enough to check formatting; use
`gofumpt -w cmd internal tools` to apply the stricter formatting that
`make fmt` uses.

**Paths.** The repository has no shell scripts outside `scripts/`, and the only
script (`scripts/fuzz.sh`) is POSIX, so it needs Git Bash or WSL on Windows.

## Run a single test

```sh
go test ./internal/plan -run TestGoldenPlans -v
go test ./internal/plan -run 'TestGoldenPlans/rewrite-cors.yaml' -v
go test ./internal/report -run TestRunJSONGolden -v
```

`go test ./...` runs everything. The full suite is fast; the whole tree passes
in a few seconds.

## Regenerate the goldens

The goldens live under `testdata/` and are rewritten by a `-update` flag that
each golden-owning test binary declares:

| Flag declared in | Writes |
|---|---|
| `internal/plan/golden_test.go` | `testdata/plan/*.golden.json` and `testdata/plan/*.generic.golden.json` |
| `internal/report/report_test.go` | `internal/report/testdata/*.golden` and `testdata/run/*.golden` |
| `internal/e2e/helpers_test.go` | `testdata/e2e/*` |

```sh
go test ./internal/plan -update
go test ./internal/report -update
go test ./internal/e2e -update
```

Always read the diff after `-update`. A golden is the expected output, so a
regenerated golden that changed for the wrong reason is a silent behaviour
change. The plan test even asserts determinism itself: `assertGoldenPlan` builds
the plan twice and fails with `planning the same input twice produced different
JSON` if the two builds differ.

After regenerating, verify without the flag:

```sh
go test ./internal/plan ./internal/report ./internal/e2e ./internal/schematest
```

## What CI runs

Four required checks ([docs/maintainers.md](../maintainers.md)):

| Job | Command |
|---|---|
| `Test (ubuntu-latest)` | `go test -race -covermode=atomic -coverprofile=coverage.out ./...` plus the coverage floors |
| `Test (windows-latest)` | `go test -race ./...` |
| `Lint` | `golangci-lint run ./...` (v2.14.0) |
| `Supply-chain checks` | `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...`, `make docs-check`, `go build ./... && go vet ./...` |

The coverage floors are enforced in the same job. The repository floor is
70 %, and each package has its own:

```
internal/plan 90   internal/compare 90   internal/ingress 90
internal/endpoint 90 internal/redact 90  internal/probe 85
internal/report 80  internal/model 90    internal/kube 90
internal/policy 90  internal/explain 90
```

A package below its floor fails CI, so a new branch needs a new test.

The [nightly workflow](../../.github/workflows/nightly.yml) adds a race run
with `-count=1` and the fuzz targets (`sh scripts/fuzz.sh 60s`). Fuzzing is not
part of the pull-request check, but a new parser is expected to have a fuzz
target ([CONVENTIONS.md](../../CONVENTIONS.md)).

## The DCO sign-off

Every commit carries a Developer Certificate of Origin sign-off
([CONTRIBUTING.md](../../CONTRIBUTING.md)):

```sh
git commit -s -m "feat(plan): add a case family"
```

`git commit -s` adds the `Signed-off-by:` trailer. Without it the PR cannot
merge. Commit messages in this repository follow the Conventional Commits
shape with the scope in brackets, for example `feat(plan): host-variant and
head case families (#34)` and `fix(cli): report close errors (#45)`.

Do not use `git stash`; use a work-in-progress commit
([CONVENTIONS.md](../../CONVENTIONS.md)).

## Checklist

- [ ] `go version` prints Go 1.26 or newer.
- [ ] `go build ./...` succeeds.
- [ ] `go test ./...` passes.
- [ ] `golangci-lint run ./...` passes (or `make lint` where `make` exists).
- [ ] `go run ./tools/docsgen -out docs/diagnostics -check` reports no drift.
- [ ] Every commit was made with `git commit -s`.
- [ ] You read [CONVENTIONS.md](../../CONVENTIONS.md) and the ADR your change
      touches.
