# ADR 0008: Planner profiles for ingress-nginx and generic HTTP proxies

## Status

Accepted

## Context

The planner grew around ingress-nginx retirement guidance. That knowledge is
useful when varco plans a migration from ingress-nginx, but several generated
cases and warnings are controller-specific rather than properties of Kubernetes
Ingress or HTTP routing in general. A single implicit behavior therefore makes
varco less useful for comparing arbitrary proxies.

The profile must be selectable without changing the default ingress-nginx
output, and controller-specific code must remain isolated so the generic core
does not accumulate annotations, regex scope, rewrite rules, or controller
notes.

## Decision

`plan.Options` gains `Profile`, with `nginx` as the default and `generic` as the
portable alternative. `varco plan` and `varco run -f` expose
`--profile (nginx|generic)`; an invalid value is `VAR-CFG-0001` and exits 2.
The plan JSON gains an additive top-level `profile` while `schema_version`
stays 1.

The `nginx` profile keeps ADR 0005 behavior unchanged: ingress-nginx annotation
cases, host-wide regex scope, rewrite-family replacement, CORS preflight,
app-root and redirect annotations, annotation findings, and the existing
trailing-slash 301 note. Ingress-nginx details live in `internal/plan/nginx.go`
behind a small profile interface; `internal/plan/generate.go` owns the generic
HTTP case core.

The `generic` profile emits only cases whose request shape is meaningful for
any HTTP proxy:

- `base`, `subpath`, and `prefix-boundary` using Kubernetes element-wise
  `Prefix` semantics;
- `trailing-slash`, `dot-segments`, and `double-slash`;
- `http-redirect` only when `spec.tls` covers the host;
- the synthetic `default-backend` and `unmatched-route` cases.

It does not emit `case-flip`, `rewrite`, `app-root`, `cors-preflight`, or
`redirect-annotation`. It does not interpret
`nginx.ingress.kubernetes.io/*` values and emits no annotation,
regex-scope, or annotation-boolean findings from them. Each included Ingress
with annotations receives one `VAR-PLAN-0009` warning stating how many
annotations the generic profile ignored.

## Consequences

- Existing callers and goldens retain ingress-nginx behavior by default; the
  only additive nginx output change is the top-level `profile` field.
- Generic plans are smaller and do not claim controller-specific behavior, at
  the cost of not testing annotation-driven routing unless `nginx` is selected.
- Future controller profiles can implement the same narrow interface without
  expanding the generic core.
- Profile choice is data in the plan envelope, so a saved plan records the
  semantics used to generate it.
