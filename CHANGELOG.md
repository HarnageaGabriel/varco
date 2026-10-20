# Changelog

All notable changes are documented here. Format: [Keep a Changelog](https://keepachangelog.com/).

## [Unreleased]

### Added

- Add the `host-variant` and `head` case families to `varco plan` (emitted by both the `nginx` and `generic` profiles, for every concrete declared host on its first planned path, SNI kept plain): `Host` header variants per RFC 3986 section 3.2.2 and RFC 9110 section 7.2 and one `HEAD` case per RFC 9110 section 9.3.2 (ADR 0005, docs/plan.md). The probe now accepts exactly one validated `Host` header override planned per request; `varco diff --header` still forbids `Host`.

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

[Unreleased]: https://github.com/HarnageaGabriel/varco/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/HarnageaGabriel/varco/releases/tag/v0.1.0
