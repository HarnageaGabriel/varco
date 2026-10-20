# Contributing

By contributing you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).

## Developer Certificate of Origin

Sign every commit with `git commit -s` ([DCO 1.1](https://developercertificate.org/)).

## Development

Go 1.26+, `golangci-lint`, `govulncheck`.

```sh
make fmt test lint vuln
```

Rules for code, tests, diagnostics and review are in [CONVENTIONS.md](CONVENTIONS.md). Architecture decisions live in `docs/adr/`.

## Contributor guides

Task-focused guides for the changes varco accepts from outside, each verified
against the code:

- [docs/dev/README.md](docs/dev/README.md) - the index, with the open `good first issue` and `help wanted` issues
- [docs/dev/setup.md](docs/dev/setup.md) - clone, Go version, make targets, the `-update` flag for the goldens, Windows notes and the DCO sign-off
- [docs/dev/add-case-family.md](docs/dev/add-case-family.md) - add a plan case family, its goldens, the `docs/plan.md` row and the README row
- [docs/dev/add-diagnostic.md](docs/dev/add-diagnostic.md) - add a stable `VAR-` code and regenerate its documentation page
- [docs/dev/add-explanation-rule.md](docs/dev/add-explanation-rule.md) - add a source-linked explanation rule
- [docs/dev/add-report-format.md](docs/dev/add-report-format.md) - add a new run report format, for example JUnit XML or a Markdown summary

Governance responsibilities are summarized in [docs/maintainers.md](docs/maintainers.md).
