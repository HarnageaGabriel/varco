# Conventions

Mandatory for every contributor, human or AI agent. Decisions behind them are in `docs/adr/`.

## Safety (the tool sends traffic)
- Requests go only to the endpoints named by the user. Never follow redirects to other hosts.
- Default methods: GET, HEAD, OPTIONS. Anything else is opt-in per case and per run.
- Rate limit always on (configurable). No real credentials in generated cases; none in logs or reports.
- TLS verification is off for probes only (we inspect the certificate, not trust it); the captured certificate fields are the output, never a pass/fail on trust.

## Determinism
- Same input, same output. No map iteration in observable output; cases are sorted by a stable key.
- Time only through an injected clock. No AI in the main path. What the tool cannot handle is listed (`unsupported` finding), never guessed.

## Layering
- `cmd/` wires; `internal/cli` adapts; `internal/engine` orchestrates.
- Pure packages (`plan`, `compare`, `diag`, `report`) perform no I/O; the prober and cluster reader are the only I/O edges. Interfaces are defined by the consumer.

## Diagnostics
- Every finding has a stable `VAR-` code with a docs page under `docs/diagnostics/`.
- Wrap errors with `%w`; never compare error strings.

## Output
- stderr for logs, stdout for requested output only. JSON is stable and versioned (`schema_version`). Exit codes are documented.

## Tests
- Table-driven, `go-cmp`, goldens under `testdata/` (`-update`). Every case cites its source (URL or capture).
- Fuzz every parser. Coverage floor enforced in CI.

## Dependencies
- No new third-party module without a written reason in the PR.

## AI agent rules
- Never use `git stash`; use a WIP commit. Stay in task scope. Mark unverified claims `VERIFY:`.

## Review protocol
CI green, adversarial review, maintainer reads every change touching request sending, TLS handling or redaction. Semantic claims without a source are rejected.
