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

Governance responsibilities are summarized in [docs/maintainers.md](docs/maintainers.md).
