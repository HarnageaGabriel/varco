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
- `app-root` and `redirect-annotation` for the redirect annotations.

The families are emitted from the manifest only: path type, regex scope, TLS
entries and annotations decide what exists. Regex scope is computed per host
across all Ingress resources, because that is how ingress-nginx behaves
(behaviors 2 and 3), and the scope is surfaced as a `VAR-PLAN-0006` warning.

Escalations and refusals:

- A regex path is sampled only in the two trivial forms
  `/literal(/|$)(.*)` and `^?/literal`; anything else is reported as
  `VAR-PLAN-0002` and produces no case. Guessing a matching path would produce a
  case whose expected result nobody can state.
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
  to a specific route.
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
