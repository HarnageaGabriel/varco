# ADR 0009: Planned Host header override

## Status

Accepted (2026-10-20)

## Context

The `host-variant` family sends the same request target as the `base` case
with a modified `Host` header: the host upper-cased, the host with a trailing
dot, and the host with the explicit default port for the scheme
([ADR 0005](0005-plan-case-families.md); [plan.md](../plan.md)). The family
exists because the host is case-insensitive (RFC 3986 section 3.2.2) and the
`Host` header may carry a port (RFC 9110 section 7.2), so a cutover can break
host matching without breaking the path.

The prober owns the `Host` line of every request it writes: `buildRequest`
emits exactly one `Host: <host>` line
([`internal/probe/probe.go`](../../internal/probe/probe.go), `buildRequest`), and
`ForbiddenHeader` lists `Host` among the headers a caller may not set
(same file, `ForbiddenHeader`). A caller that could set `Host` freely would
put a second `Host` line on the wire, which is a request-smuggling shape
(RFC 9112 section 2.2), and it would let an unvalidated value reach the
request framing.

The plan is the only caller that needs to vary the `Host` line, so the
override has to travel from the plan to the prober. Whatever carries it must
be validated like a host: an override of `evil\r\nX-Injected: 1` must never
reach the socket.

The TLS server name is a separate thing. Over HTTPS the SNI is the TLS
extension value (`tls.Config.ServerName`,
[`internal/probe/probe.go`](../../internal/probe/probe.go), `Do`), so a case can
carry a plain SNI and a different `Host` line, and only the `Host` line
differs on the wire.

## Decision

The plan carries the override as a request header: one header named `Host`
with exactly one value, in the case's `headers` list
([`internal/plan/generate.go`](../../internal/plan/generate.go),
`addHostVariantAndHeadCases`, and the `hostHeaderName` constant). The case
keeps its declared `host` and its plain `sni`; only the header varies, so the
plan states which line differs and the report can attribute a difference to
the `host-variant` family.

The prober separates the override from the other headers before it validates
them (`hostOverride`,
[`internal/probe/probe.go`](../../internal/probe/probe.go)):

- at most one `Host` header per request; a second one is refused;
- the override carries exactly one value;
- the value is not empty;
- the value is validated with `ValidHost`, the same validator a case host
  uses (same file, `ValidHost`): no whitespace, no control byte, no DEL, and
  none of `/ ? # @ \`.

A violation is a `VAR-CFG-0001` diagnostic and no request is sent. The
override then replaces the probe's host, so exactly one `Host` line reaches
the wire, and the remaining headers are written after it.

The plan loader applies the same rule to a plan file it did not write
(`planHeadersProblem`,
[`internal/cli/run.go`](../../internal/cli/run.go)): one `Host` header, one
value, a valid host. A plan that breaks the rule is refused at load
([ADR 0010](0010-input-limits-and-plan-validation.md)).

`varco diff --header` still forbids `Host`: the flag is for headers a caller
owns, and the `Host` line stays reserved for the planned cases. The CLI
refuses it with `VAR-CFG-0001` and the message "request header Host is not
allowed; use --host"
([`internal/cli/cli.go`](../../internal/cli/cli.go), `parseHeaders`).

The SNI that goes on the wire is checked by `validSNIOnWire`
([`internal/probe/probe.go`](../../internal/probe/probe.go)), deliberately more
permissive than `ValidSNI` (same file), which guards the `--sni` flag. The
wire check accepts letters, digits, hyphen, underscore and dot labels and an
IP literal, and rejects control bytes, whitespace, a colon, a slash, an at
sign and names longer than 253 bytes. The difference is a host with an
underscore in a label: such a host is not a valid DNS name, but a manifest or
a plan can legitimately declare it, and it must still be probeable. The
rejected value is never echoed; the diagnostic names only its length.

## Alternatives considered

- **A dedicated field on the plan case** (`host_override` next to `host`).
  Rejected: the headers list already exists, is already part of the plan
  schema, and is already validated by the plan loader and by the prober. A
  second way to express the same request would need its own validation, its
  own schema entry, and its own rule in the case id - the hash of the
  canonical case already covers the headers
  ([`internal/plan/plan.go`](../../internal/plan/plan.go), `caseID`).
- **A new request kind** (a `host-variant` request that is not a case).
  Rejected: a case is one request target for one host, and everything around
  it - endpoint resolution, the rate limit, the request budget, the family
  and path filters, the coverage counting, the report row - is defined per
  case. A second kind would have to be threaded through all of it to express
  one different header value.
- **The override as an ordinary request header.** Rejected: the prober
  forbids `Host` as an ordinary header, and the request would carry two
  `Host` lines. Letting the override be an ordinary header would also let
  any other caller set it, which is exactly what this ADR refuses.

## Consequences

- The plan can express a request whose `Host` line differs from the declared
  host, and the value that reaches the wire is constrained by the same
  validator as any other host.
- A host-variant case is a normal case: it is filtered, budgeted, counted
  and reported like every other, and its difference is attributable to the
  family.
- The `Host` override is part of the case id, so a plan that varies the
  override produces different cases with different ids.
- `varco diff --header` cannot set `Host`, so a hand-written `Host` header
  stays impossible; the only way to vary the `Host` line is a planned case.
- The dry-run request line prints the case host, not the override
  ([`internal/engine/run.go`](../../internal/engine/run.go), `DryRequest.Line`),
  so a dry run does not show which `Host` line a host-variant case sends.
- The SNI of a planned case is checked on the wire but not as strictly as the
  `--sni` flag, so a host with an underscore in a label can be probed. The
  name is never echoed in the diagnostic.

## Sources

- PR #34, `feat(plan): host-variant and head case families` - the family, the
  planned `Host` override and the single `Host` line the prober writes.
- PR #42, `fix: serialize run progress callbacks and validate the TLS SNI of
  planned cases` - the check of the SNI on the wire, and the reason it is
  more permissive than the `--sni` flag.
