# ADR 0006: Redact sensitive response metadata at serialization

## Status

Accepted

## Context

Redirect targets routinely carry credentials. OAuth 2.0 authorization-code
responses put the code in the redirect URI query ([RFC 6749 section
4.1.2](https://www.rfc-editor.org/rfc/rfc6749#section-4.1.2)), and presigned
object URLs carry credentials and signatures in query parameters. `Location`
was previously reported verbatim, so normal comparison output could become a
credential disclosure channel.

`Set-Cookie` is also sensitive. Cookie values are application credentials or
identifiers, but cookie names and attributes such as `Secure`, `HttpOnly`,
`SameSite`, `Path`, and `Domain` are useful migration signals.

## Decision

Comparison operates on the raw captured `Location` values so two different
secrets still produce a difference. Immediately before table or JSON
serialization, the report layer replaces every query value, the fragment, and
userinfo with the deterministic literal `REDACTED`. Scheme, host, path, query
keys, and query-key order remain visible. An unparseable URL is replaced in
full. If two raw locations differ but their redacted forms are equal, the
report keeps the difference and explains that the hidden values differ.

`varco diff --show-sensitive` is the explicit escape hatch for raw Location
values. It prints a warning to stderr. JSON reports state the choice in a
top-level `redacted` boolean without changing `schema_version` 1.

`Set-Cookie` is parsed before comparison. The comparison uses the set of cookie
names and only these attributes: `Secure`, `HttpOnly`, `SameSite`, `Path`,
`Domain`, presence of `Max-Age`, presence of `Expires`, and `Partitioned`.
Cookie values are neither compared nor rendered. Request paths in probe-failure
diagnostics are represented only by length, and HTTP response parse errors do
not include rejected header text.

## Consequences

- Default output is safe to share with respect to Location query values,
  fragments, userinfo, and cookie values.
- Operators can still see that only hidden redirect values changed.
- `--show-sensitive` output must be handled as credential-bearing material.
- Cookie value rotation no longer causes noise, while weaker or changed cookie
  attributes produce `VAR-CMP-0007`.

## Known limits

- Secrets carried in path segments (for example `/reset/<token>`) are not redacted: the path is what varco compares and reports.
- Query keys are kept as written, so a secret used as a bare key (`/cb?<token>`) is printed. Use `--show-sensitive` only for local debugging.
