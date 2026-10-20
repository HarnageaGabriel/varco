# ADR 0005: Plan case families

## Status

Accepted

## Context

`varco plan` must translate Ingress resources into probe cases. The interesting
behaviors are the ones where ingress-nginx and a Gateway API implementation can
disagree, and they are easy to get wrong silently: matching is case-insensitive
once regex is on, `rewrite-target` turns regex on for a whole host, a missing
trailing slash redirects, and URL normalization happens before matching. The
source of truth for these behaviors is the Kubernetes blog post
[Before you migrate](https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate)
(behaviors 1 to 5), backed by `.notes/research/01-study.md`.

A probe that returns a difference is only useful if the difference can be
attributed. A single request per path cannot say whether two controllers differ
about trailing slashes, prefix boundaries, case folding, normalization or
redirects.

## Decision

One case family per behavior. A family is a request shape that isolates exactly
one matching rule, so a difference is attributed by the `family` field rather
than by guesswork:

- `base`, `trailing-slash`, `subpath`, `prefix-boundary`, `case-flip`,
  `dot-segments`, `double-slash` for path matching (blog behaviors 1, 2, 3, 4, 5);
- `http-redirect` for the HTTP to HTTPS redirect (TLS or `ssl-redirect`);
- `cors-preflight` for `enable-cors`;
- `rewrite` for `rewrite-target`, replacing `base` and `subpath` on that Ingress;
- `app-root` and `redirect-annotation` for the redirect annotations;
- `default-backend` for the plan-level unknown host (`varco-unknown.invalid`,
  with SNI `varco-unknown.invalid` over HTTPS) and `unmatched-route` for an
  unknown path on a host with no catch-all `/` route. These two are synthetic:
  they are emitted per scheme and per host rather than from a declared route, so
  what a controller answers before any route matches - the default backend, the
  default certificate, an unmatched path - is attributable too.

The families are emitted from the manifest only - plus the two synthetic
families above, which come from the plan rather than from a route - because path
type, regex scope, TLS entries and annotations decide what exists. Regex scope
is computed per host
across all Ingress resources, because that is how ingress-nginx behaves
(behaviors 2 and 3), and the scope is surfaced as a `VAR-PLAN-0006` warning.

Escalations and refusals:

- A regex path is sampled only when it is a literal prefix with an optional `^`
  anchor and one of the trailers `(/|$)(.*)`, `(/|$)`, `(/.*)`, `/(.*)`, `/.*`
  or `(.*)` (for example `/api/(.*)` or `/api/.*`), or a bare literal; anything
  else is reported as `VAR-PLAN-0002` and produces no case. Guessing a matching
  path would produce a case whose expected result nobody can state.
- `Exact` and `Prefix` paths are kept verbatim; `ImplementationSpecific` is
  treated as `Prefix`-like with a note, because ingress-nginx treats it as a
  prefix (or a regex when regex scope is on).
- `trailing-slash` is not emitted for regex paths: ingress-nginx does not
  redirect those (behavior 4).
- `case-flip` is emitted where matching is actually case-insensitive: on an
  Ingress with `use-regex` or `rewrite-target`, on a path that looks like a
  regex, and on every `Exact`/`Prefix` path of a host whose regex scope was
  turned on by another Ingress. Emitting it everywhere would claim a behavior
  ingress-nginx does not have.
- A rule without a host needs `--default-host`; without it the rule is reported
  as `VAR-PLAN-0005` instead of being planned against a made-up host.
- `http-redirect` is emitted per path, so a redirect difference is attributable
  to a specific route. A host with TLS redirects by default, but an explicit
  `ssl-redirect: "false"` (or `force-ssl-redirect: "false"`) suppresses the
  redirect and is recorded as a note.
- A `spec.tls` entry with no hosts is the Kubernetes default certificate and
  covers every rule host of the Ingress.
- An omitted `ImplementationSpecific` path is treated as `/`, which is what
  ingress-nginx does, and is noted on the case.
- `app-root` applies to the whole Ingress; the case is planned once, on the
  first rule that has a host, and the source records that rule index.
- Annotations varco cannot observe black-box (timeouts, body size, auth,
  canary weights, snippets, upstream TLS, and so on) and nginx annotations
  unknown to varco are never translated into cases; they are listed once per
  Ingress and annotation as `VAR-PLAN-0001`.

Determinism is part of the contract: cases carry
`hex(sha256(canonical case))[:12]` as `id`, are de-duplicated by that ID, and are
sorted by `(host, path, scheme, method, family, id)`. Findings and warnings are
sorted and de-duplicated. Planning is pure and never reads the clock.

## Consequences

- The plan is bigger than one case per path, and for a TLS host it roughly
  doubles: every GET path also gets an `http-redirect` case. That is the price of
  attribution, and the families can be filtered when running the plan later.
- Case families name ingress-nginx behaviors. `varco` is not tied to
  ingress-nginx: the same cases describe what any gateway must answer for the
  declared routes.
- Adding a family means adding a constant, a row in `docs/plan.md`, and an entry
  in the golden corpus under `testdata/plan`.

## Host-variant and head families (appended 2026-10-20)

Two families were added so that host matching and HEAD handling become visible
in a plan without guesswork:

- `host-variant` sends the same request target as the `base` case with a
  modified `Host` header: the host upper-cased, the host with a trailing dot
  (`example.com.`), and the host with the explicit default port for the case
  scheme (`example.com:443` over https, `example.com:80` over http). Sources:
  RFC 3986 section 3.2.2 (the host is case-insensitive) and RFC 9110 section 7.2
  (the `Host` header may carry the port).
- `head` sends one `HEAD` request instead of `GET` on the same target (RFC 9110
  section 9.3.2). HEAD is one of the default safe methods.

Both families are emitted for every concrete declared host on the smallest
planned path of that host, and both the `nginx` and the `generic` profiles emit
them: host matching and HEAD handling are portable HTTP behaviors, not
ingress-nginx annotations (ADR 0008). Hosts substituted from a wildcard label or
filled in from `--default-host` are skipped - for those, a host-variant
difference would probe varco's substitution instead of a declared host. SNI stays
the plain host, so only the `Host` header differs on the wire. Like every
family, these cases probe and assert nothing about which behavior is right:
equivalence between old and new is what is compared.

On the probe side, the planner may carry exactly one `Host` header override per
request, and the prober now accepts exactly one such override, validates it like
a case host (rejecting an empty value, duplicate or multi-valued `Host` headers),
and writes it as the request's only `Host` line. `varco diff --header` still
forbids `Host`: setting it by hand remains reserved for planned cases.

## Order independence of the plan output (appended 2026-10-20)

The plan output does not depend on the order of the input resources: the same
resources in any order produce the same plan, byte for byte. Two parts of the
output span several resources and are therefore chosen by a stable rule:

- the `head` and `host-variant` cases of a host use the smallest planned path
  of that host - the lexicographically smallest request target planned for it,
  in byte order, chosen once every resource has been read. They used to use the
  first planned path, which depended on the order of the input;
- the `VAR-PLAN-0006` warning names the Ingress with the smallest namespace and
  name among those that turned host-wide regex matching on for the host. It
  used to name the first one varco read.

The rule is what makes the guarantee hold, and the plan keeps it: cases are
sorted by a stable key, findings and warnings are sorted and de-duplicated,
and no map iteration reaches the output.

The id of a case is the hash of the case, so the head and host-variant cases of
a host whose smallest planned path is not the path planned first get a new id.
The ids of every other family are unchanged: only the path of those two
families, and the note of the `head` case, moved.
