# Changelog

All notable changes are documented here. Format: [Keep a Changelog](https://keepachangelog.com/).

## [Unreleased]

## [0.1.2] - 2026-10-28

Hardening release. It makes plan generation independent of input order, closes the remaining ways remote or plan-supplied text could reach the terminal, and ships reproducible release binaries. Output change to note: the case ids of the `head` family and of some `host-variant` cases changed (see Fixed).

### Security

- Escape control characters in all table output built from remote or plan-supplied data: the `varco run` and `varco diff` tables, the `varco plan` table, the `--dry-run` request lines, the `VAR-RUN-0001` message and kubectl stderr. Control bytes (C0, DEL, C1) and invalid UTF-8 are shown as visible `\xNN` escapes, so a hostile backend, plan file or kubectl error cannot repaint or rewrite the operator's terminal. JSON output was already safe. See ADR 0011.
- Validate the `sni` and `method` of every case of a `--plan` document at load, with the case index and field named and the value never echoed (`VAR-CFG-0001`). See ADR 0010.
- Bound the certificate fields captured by the prober: subject and issuer at 1024 bytes, 64 SANs of at most 253 bytes each, truncated on a UTF-8 boundary with a `...` marker and a final `...+N more` entry. The SHA-256 fingerprint still covers the complete certificate.

### Fixed

- Make plan generation independent of the order of the input resources. The `head` and `host-variant` cases now use the lexicographically smallest planned path of the host, and `VAR-PLAN-0006` names the Ingress with the smallest namespace/name. Because the case note is part of the id, every `head` case id changed, and `host-variant` ids changed where the smallest path differs from the first; ids of all other families are unchanged.
- Report one manifest error layout: `VAR-PLAN-0003: document N: ...` for decode, parse and validation failures alike.
- Stop dropping Ingress resources silently: `--ingress-class` now reports every excluded Ingress with the new warning `VAR-PLAN-0011`.

### Added

- Add `make demo`: two local servers playing the old and the new ingress with documented differences, and a walkthrough in `docs/demo.md` that goes from `DIFFERENT` to `EQUIVALENT` with an exceptions file.
- Add reproducible release binaries: the version date is now the commit date, and `make verify-reproducible` and `scripts/verify-reproducible.sh` rebuild and compare hashes (`v0.1.0` and `v0.1.1` stamped the build time and cannot be reproduced bit for bit).
- Add `scripts/update-release-docs.sh`, which rewrites the version strings and the Krew hashes after a release.
- Add a generated command reference in `docs/reference/` (checked in CI), a Markdown link checker (`tools/linkcheck`, part of `make docs-check`), and documentation: architecture map, threat model, limitations, contributor how-to guides, ADRs 0009 to 0011, and the first-match rule of exceptions.
- Add tests: metamorphic and property tests for the comparator and the exceptions evaluator, determinism and input-order tests for the planner, hostile-server end-to-end scenarios, manifest edge cases, TLS edge cases, engine edge cases, rendering fuzz targets and source checks for explanations.

## [0.1.1] - 2026-10-20

Security and hardening release. It fixes a credential leak in error messages that affects `v0.1.0`; upgrading is recommended.

### Security

- Fix `redact.Text` leaking credentials: a URL that was quoted or prefixed inside a message (for example kubectl's `Get "https://user:password@host/...": dial tcp ...` for a kubeconfig whose server URL embeds userinfo) kept its password and query tokens in stderr, CI logs and report error strings. URLs are now redacted at any offset in a message (ADR 0006).
- Validate the TLS SNI of planned cases before it goes on the wire (`VAR-CFG-0001`, value never echoed); hosts with underscores keep working.
- Cap the `--plan` and `--exceptions` reads at 8 MiB like manifests (`VAR-PLAN-0003`, `VAR-CFG-0002`), validate every case of a plan document at load (host, path, headers, control bytes in text fields, including unsupported findings and warnings), and create the `--output` report file with mode `0600`.
- Document in `SECURITY.md` that `--from-cluster` runs the `kubectl` found on `PATH` with the user's privileges, `get` verb only.

### Fixed

- Serialize the `varco run` progress callback: workers called it concurrently, a data race for any writer that is not thread-safe.
- Report a failing `Close` of the `--output` and `--suggest-exceptions` files instead of claiming success (exit 4), and stop ignoring the error of the diagnostic registry in the CLI.

### Added

- Add the `host-variant` and `head` case families to `varco plan` (emitted by both the `nginx` and `generic` profiles, for every concrete declared host on its first planned path, SNI kept plain): `Host` header variants (upper-cased, trailing dot, explicit default port) and one `HEAD` request. The prober accepts exactly one validated `Host` override per planned request; `varco diff --header` still forbids `Host`.
- Publish JSON Schemas for `varco plan --format json` and `varco run --format json` in `docs/schema/`, validated against all JSON goldens in CI.
- Add guides: a cutover cookbook, a troubleshooting page keyed by `VAR-` codes, and a GitHub Actions example that downloads and verifies the release before use.
- Add an in-process end-to-end test suite for `varco run`, fuzz targets for the plan loader and the kubectl JSON decoders, benchmarks (`make bench`), and a coverage floor for `internal/kube`.

## [0.1.0] - 2026-10-20

The first pre-alpha release of varco; command output formats, JSON schemas and flags may still change between releases.

### Added

- Add `varco diff`: probe two numeric IP endpoints and report status, redirect, security/CORS header, TLS, certificate and body differences (table or JSON), with `VAR-CMP-0001`..`0006` diagnostics.
- Add `varco plan`: read Kubernetes Ingress manifests (files, directories, stdin, `kind: List`, multi-document YAML and JSON) and emit a deterministic JSON or table list of probe cases with stable IDs, plus the findings varco cannot test (`VAR-PLAN-0001`..`0008`). Adds `internal/ingress` (pure parser, `networking.k8s.io/v1` only, `extensions/v1beta1` rejected) and `internal/plan` (pure case generation: path families, host-wide regex scope, CORS preflight, redirects, TLS/SNI).
- Split `varco plan` into `nginx` and `generic` profiles selectable with `--profile` on `plan` and `run -f`: `nginx` remains the default and preserves ingress-nginx annotation, regex, rewrite, CORS, app-root, redirect, and trailing-slash behavior, while `generic` emits only portable HTTP/Kubernetes cases, ignores controller annotations with one `VAR-PLAN-0009` count warning per Ingress, and records the additive top-level JSON `profile` without changing `schema_version: 1` (ADR 0008).
- Add `varco run`: execute a plan from `varco plan --format json` or from manifests against two endpoints (`--old`/`--new` with per-scheme `--old-http`/`--old-https`/`--new-http`/`--new-https` overrides), with bounded concurrency, one global rate limiter, and a verdict per ADR 0007. A new pure `internal/model` package holds the observation/evaluation/report types and a new pure `internal/policy` package is the separate policy layer: an exceptions file (versioned YAML, `--exceptions`) marks differences accepted, keeps them visible with the reason and exception id, ignores and lists expired exceptions, and reports the built-in ignored fields so nothing is invisible. Add the `VAR-CFG-0002` diagnostic for an invalid exceptions file, table and JSON reports, progress on stderr, and a fuzz target for the parser. `varco diff` now shares the ADR 0007 precedence: exit 1 wins over exit 3.
- Make `varco run` trustworthy on real clusters: `--repeat N` (1..5) probes each endpoint N times per case and treats a field that disagrees between observations as unstable (`auto:unstable` exception, `INCONCLUSIVE` with `unstable status`), `--suggest-exceptions FILE` writes a version-1 exceptions file for unstable `(host, field)` pairs (never overwriting, exit 2), `--body (2xx|hash|none)` compares the body hash only when both endpoints answered the same 2xx status, `--max-requests N` caps the run and reports never-probed cases as skipped (`budget`), `--family` and `--exclude-path` filter cases before the run (`excluded`), and `--dry-run` prints the requests a run would send and contacts nothing. The table gains `HOST`, `METHOD`, `PATH` and `FAMILY` columns beside the case id and shortens a `body` value to `sha256=<first 12 hex> size=<n>`, and the JSON report gains additive `repeat`, `body_policy` and `excluded` fields (`schema_version` stays 1). `varco plan` now also adds `default-backend` and `unmatched-route` cases (ADR 0005, docs/plan.md).
- Add read-only live-cluster planning with `varco plan --from-cluster` and `varco run --from-cluster`: varco invokes only `kubectl get`, honors the user's kubeconfig/context, and can read ingress-nginx controller ConfigMap values that become `VAR-PLAN-0010` warnings.
- Add source-linked explanations to `varco run`: `DIFFERENT` cases matching known ingress-nginx behavior patterns gain stable explanations, suggested fixes and source URLs in indented table rows and the additive JSON `explanations` array, while equivalent and unrelated cases remain unchanged.
- Add the signed and attested release workflow, draft GoReleaser configuration, release verification and install documentation, and planned Homebrew and Krew manifests for `v0.1.0`.
- Add the buildable `varco` CLI scaffold with version output and command stubs, the diagnostic registry and generated diagnostic reference pages, and the documented exit codes, probe safety, endpoint syntax, and diagnostic code policy.
- Record the accepted verdict model separating per-case outcomes, aggregate verdicts, and exit codes; enforce uniform 8 MiB, 100,000-node, and depth-64 YAML limits; and strengthen pure-package coverage gates.
- Add CI, nightly race and fuzz checks, dependency automation and review, security analysis, third-party license notices, trademark and issue-contact policies, CODEOWNERS, maintainer governance documentation, and release configuration.

### Changed

- `varco diff` now validates `--rate` (0.001..10000) and `--host`, sorts and de-duplicates paths, compares multi-valued headers element-wise (absent differs from empty) and `Content-Encoding`, keeps going after a failing path (exit 3 at the end), and exits 0 for `-h`.

### Fixed

- Fix the `FuzzURL` oracle false positive: the leak detector now re-parses the redacted output and asserts its non-query parts, query keys, redacted fragment, and per-value `REDACTED` state instead of substring matching, so query values that also appear as the path or query separator no longer trip it.
- Fix `varco plan` per the M2 review: an `Exact` path on a host whose regex scope is active is also probed with `subpath` and `prefix-boundary` cases (blog behavior 2); the `trailing-slash` note states the direction probed instead of asserting a redirect; annotation booleans (`use-regex`, `ssl-redirect`, `force-ssl-redirect`, `enable-cors`) use `strconv.ParseBool` semantics and an unparsable value records `VAR-PLAN-0007`; `internal/ingress` checks `apiVersion`/`kind` before decoding and accepts unquoted scalar annotations (non-scalars are `VAR-PLAN-0003`); every emitted case is validated for control bytes; and an Ingress or rule with no plannable HTTP path reports `VAR-PLAN-0008`. `permanent-redirect-code`/`temporal-redirect-code` now appear in the case note, and `cors-allow-headers` without `enable-cors: true` is reported rather than dropped.
- Fix `varco plan` semantics against ingress-nginx: a `spec.tls` entry without hosts now covers every rule host, an explicit `ssl-redirect: "false"` or `force-ssl-redirect: "false"` on a TLS host no longer plans an HTTP redirect (a note is recorded instead), more regex path forms are sampled (`/api/(.*)`, `/api/.*`, `/api(/.*)`, `/api(.*)`), an omitted path is planned as `/`, and one manifest input is capped at 8 MiB (`VAR-PLAN-0003`) with directory walks skipping non-regular files.

### Security

- Redact `Location` query values, fragments, and userinfo in table and JSON diff reports by default; add the warning-bearing `--show-sensitive` opt-in and a top-level JSON `redacted` field. `Set-Cookie` now compares cookie names and security/scope attributes as `VAR-CMP-0007` without comparing or printing cookie values, and probe failures no longer print request queries or rejected response-header text.
- Harden `varco diff` request handling: request targets are sent verbatim (no re-escaping of `%2F`, `?`, `//`), probes ignore proxies, do not decompress, accept TLS 1.0+, hash the whole body, and refuse CR/LF/control bytes, invalid header names/values, `Host`/`Content-Length`/`Transfer-Encoding`/`Trailer`/`Connection` headers and more credential headers (`X-Api-Key`, `X-Auth-Token`, `Private-Token`, `Api-Key`).
- Harden `varco diff` response probing: cap header blocks at 64 KiB, skip up to eight informational responses, capture trailers, default non-positive probe timeouts to 10 seconds, and cap body hashing at a configurable 256 MiB default. SNI now defaults without the Host port and rejects port-bearing values; invalid paths are redacted in errors, and cancellation takes precedence over probe failures.

[Unreleased]: https://github.com/HarnageaGabriel/varco/compare/v0.1.2...HEAD
[0.1.2]: https://github.com/HarnageaGabriel/varco/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/HarnageaGabriel/varco/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/HarnageaGabriel/varco/releases/tag/v0.1.0
