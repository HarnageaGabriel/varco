# Changelog

All notable changes are documented here. Format: [Keep a Changelog](https://keepachangelog.com/).

## [Unreleased]

- Add `varco diff`: probe two numeric IP endpoints and report status, redirect, security/CORS header, TLS, certificate and body differences (table or JSON), with `VAR-CMP-0001`..`0006` diagnostics.

- Fix `varco diff` request handling: request targets are sent verbatim (no re-escaping of `%2F`, `?`, `//`), probes ignore proxies, do not decompress, accept TLS 1.0+, hash the whole body, and refuse CR/LF/control bytes, invalid header names/values, `Host`/`Content-Length`/`Transfer-Encoding`/`Trailer`/`Connection` headers and more credential headers (`X-Api-Key`, `X-Auth-Token`, `Private-Token`, `Api-Key`).
- `varco diff` now validates `--rate` (0.001..10000) and `--host`, sorts and de-duplicates paths, compares multi-valued headers element-wise (absent differs from empty) and `Content-Encoding`, keeps going after a failing path (exit 3 at the end), and exits 0 for `-h`.
- Harden `varco diff` response probing: cap header blocks at 64 KiB, skip up to eight informational responses, capture trailers, default non-positive probe timeouts to 10 seconds, and cap body hashing at a configurable 256 MiB default. SNI now defaults without the Host port and rejects port-bearing values; invalid paths are redacted in errors, and cancellation takes precedence over probe failures.

- Add the buildable `varco` CLI scaffold with version output and command stubs.
- Add the diagnostic registry and generated diagnostic reference pages.
- Add CI, dependency automation, security analysis, and release configuration.
- Document exit codes, probe safety, endpoint syntax, and diagnostic code policy.
