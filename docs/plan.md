# varco plan

`varco plan` reads Kubernetes Ingress manifests and derives a deterministic list
of test cases, plus an explicit list of things it cannot test. The cases are the
input for `varco run` (still to come). Planning never touches a cluster and never
sends traffic.

The semantics of the generated cases come from the ingress-nginx retirement
guidance, [Before you migrate](https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate)
(behaviors 1 to 5), and are recorded in
[ADR 0005](adr/0005-plan-case-families.md).

## Usage

```sh
varco plan -f ingress.yaml --format json
varco plan -f ./manifests -f - < api.yaml --format table
varco plan -f api.yaml --profile generic --format json
varco plan -f api.yaml --ingress-class nginx --default-host fallback.example.com
```

Flags: `-f <file|directory|->` (repeatable; a directory is walked recursively
and only regular `*.yaml`, `*.yml` and `*.json` files are read, sorted by path;
symlinks and other non-regular files are skipped), `--format (table|json)`,
`--ingress-class NAME` (matches `spec.ingressClassName` or the
`kubernetes.io/ingress.class` annotation), `--default-host HOST` (used by rules
that declare no host), and `--profile (nginx|generic)` (default `nginx`; also
available on `varco run -f`). One input, file or stream, is read through an 8 MiB cap;
a larger input is rejected with `VAR-PLAN-0003`.

Exit codes: `0` when a plan was produced, even if it contains unsupported
findings (findings are part of the output, not an error), `2` for usage errors
and manifest parse errors, `4` if the report cannot be written.

## Accepted manifests

- `apiVersion: networking.k8s.io/v1`, `kind: Ingress`.
- `kind: List` (for example `kubectl get ingress -o yaml`) and multi-document
  YAML streams. Other kinds, including ConfigMaps and Deployments, are ignored
  silently.
- `extensions/v1beta1` and `networking.k8s.io/v1beta1` are rejected with
  `VAR-PLAN-0003`: their semantics and fields differ, so planning them with v1
  rules would be a guess.
- JSON is accepted wherever YAML is (JSON is a YAML subset).

## Output

`--format json` writes a stable envelope with `schema_version: 1`, `profile`,
`cases`, `unsupported` and `warnings`; `--format table` prints the case and diagnostic sections.
Cases are ordered by `(host, path, scheme, method, family, id)` and identical
cases are de-duplicated. `id` is the first 12 hex characters of the SHA-256 of
the canonical case (host, SNI, scheme, method, path, sorted headers, family,
source, note), so an ID is stable across runs and independent of generation
order. No output order depends on map iteration, and no case carries a clock, a
credential header or a method other than `GET`, `HEAD` or `OPTIONS`.

The JSON envelope is published as a JSON Schema:
[plan.schema.json](schema/plan.schema.json). It names every field, its type
and the closed lists of values, so any standard JSON Schema validator can
check an output. The envelope accepts an unknown top-level field, because a
new top-level field is an additive change that keeps `schema_version` at `1`;
see [docs/schema](schema/) for the compatibility promise.

## Profiles

The planner has two selectable profiles, recorded as `profile` in JSON:

- `nginx` is the default and preserves the ingress-nginx knowledge documented
  in [ADR 0005](adr/0005-plan-case-families.md): annotation-driven cases,
  host-wide regex scope, rewrite, CORS, app-root, redirect annotations, and
  ingress-nginx trailing-slash notes.
- `generic` contains only cases valid for an HTTP proxy without ingress-nginx
  quirks: `base`, `subpath`, `prefix-boundary` with Kubernetes element-wise
  `Prefix` semantics, `trailing-slash`, `dot-segments`, `double-slash`,
  `http-redirect` only when `spec.tls` covers the host, `default-backend`, and
  `unmatched-route`.

The generic profile never reads `nginx.ingress.kubernetes.io/*` values and never
emits `case-flip`, `rewrite`, `app-root`, `cors-preflight`, or
`redirect-annotation` cases. It also emits no `VAR-PLAN-0001`,
`VAR-PLAN-0006`, or `VAR-PLAN-0007` findings from those annotations. Instead,
each included Ingress with annotations gets one `VAR-PLAN-0009` warning naming
the ignored annotation count. The profile is selected with `--profile` on
`varco plan` and on `varco run -f`; an invalid value is `VAR-CFG-0001` and
exits 2. Profile selection and its rationale are recorded in
[ADR 0008](adr/0008-planner-profiles.md).

## Case families

Each family isolates one behavior so a difference can be attributed to it.

| Family | Request | Why it exists |
|---|---|---|
| `base` | the path verbatim | the declared route |
| `trailing-slash` | toggled trailing slash | probes the other slash form; the note states the direction probed and does not assume the answer (behavior 4) |
| `subpath` | `path + /varco-probe` | a deeper path must still route |
| `prefix-boundary` | `path + varco` (for example `/api` -> `/apivarco`) | `Prefix` matches element-wise, so this must not match; with `use-regex` the prefix is a regex and it may match (behavior 1) |
| `case-flip` | path with letter case swapped | `use-regex` and `rewrite-target` make matching case-insensitive for the whole host (behaviors 1 to 3) |
| `dot-segments` | `/varco/../` + path | URL normalization resolves `..` before matching (behavior 5) |
| `double-slash` | `//` + path | repeated slashes are normalized or answered with `301` (behavior 5) |
| `http-redirect` | plain HTTP, same path | a host with TLS, or with `ssl-redirect`/`force-ssl-redirect` set true, answers HTTP with a redirect; an explicit false on a TLS host suppresses it |
| `cors-preflight` | `OPTIONS` with `Origin: https://varco.invalid` and `Access-Control-Request-Method: GET` | `enable-cors` answers preflight (plus `Access-Control-Request-Headers` when `cors-allow-headers` is set) |
| `rewrite` | the `base` and `subpath` requests | `rewrite-target` rewrites the upstream path (behavior 3) |
| `app-root` | `GET /` | the `app-root` annotation redirects `/` to the application root |
| `redirect-annotation` | `GET path` | `permanent-redirect` and `temporal-redirect` answer with a redirect; the expected code comes from `permanent-redirect-code`/`temporal-redirect-code` (default 301/302) and is recorded in the note |
| `default-backend` | `GET /` on `varco-unknown.invalid` | an unknown host exercises the default backend and, over HTTPS, the default certificate |
| `unmatched-route` | `GET /varco-unmatched-route` | a path no rule declares, on a host with no `Prefix`/`ImplementationSpecific` `/` route, must fall to whatever the controller serves for an unmatched path |
| `host-variant` | the host's smallest planned path with the `Host` header upper-cased, written with a trailing dot (`example.com.`) or with the explicit default port for the scheme (`example.com:443` over https, `example.com:80` over http) | the host is case-insensitive (RFC 3986 section 3.2.2) and the `Host` header may carry the port (RFC 9110 section 7.2); SNI stays the plain host, so only the `Host` header differs, and the case asserts nothing about which behavior is right - equivalence between old and new is what is compared |
| `head` | `HEAD` on the host's smallest planned path | HEAD handling (RFC 9110 section 9.3.2) must be equivalent between old and new; the case probes and asserts nothing |

Two families are synthetic: they are emitted once per plan, not from a declared
route. A `default-backend` case is added for every scheme the plan uses (host
`varco-unknown.invalid`, `GET /`, and SNI `varco-unknown.invalid` over HTTPS). An
`unmatched-route` case is added for every host that declares no `Prefix` or
`ImplementationSpecific` `/` path (`GET /varco-unmatched-route`). Both exist so a
run can see what a controller does before any route matches - an unknown host
(and, over HTTPS, the default certificate) and an unknown path.

Two more families come from the plan as a whole rather than from a single
declared route: `head` and `host-variant`. Every concrete declared host (a
wildcard host or a host filled in by `--default-host` is skipped) gets one `head`
case and three `host-variant` cases on the smallest planned path of that host:
the lexicographically smallest request target planned for the host, in byte
order, chosen after every resource has been read. The rule does not depend on
the order of the input, so the same resources in any order plan the same
cases. Both the `nginx` and the `generic` profile emit them, because host
matching and HEAD handling are portable HTTP behaviors (ADR 0008). SNI stays
the plain host, so only the `Host` header differs.

Rule host and path type decide what is emitted: `Exact` paths get `base`,
`trailing-slash` (unless regex), `case-flip` (when matching is case-insensitive),
`dot-segments` and `double-slash`; `Prefix` adds `subpath` and
`prefix-boundary`; `ImplementationSpecific` is treated as `Prefix`-like with a
note, because that is what ingress-nginx does. When a host's regex scope is
active, an `Exact` path is matched as a prefix regex too, so it also gets
`subpath` and `prefix-boundary`, with a note citing blog behavior 2. An omitted path is treated as `/`
with a note. A path containing regex metacharacters (`^$()[]{}*+?|\`) is only
sampled when it has a literal prefix with an optional `^` anchor and one of a
small set of suffixes, otherwise the path is reported as unsupported rather than
guessed.

- `/literal(/|$)(.*)`, `/literal(/|$)`, `/literal(/.*)`, `/literal/(.*)`,
  `/literal/.*` or `/literal(.*)` -> `/literal/varco-probe`
- `^?/literal` -> `/literal`

`use-regex` on any Ingress, or a `rewrite-target` anywhere on the host, turns on
host-wide regex matching. Planning then adds a `case-flip` case for every
`Exact` and `Prefix` path of that host, including paths declared by other
Ingress resources, and records a `VAR-PLAN-0006` warning naming the host and
the Ingress that turned the scope on. When several Ingresses turn it on, the
warning names the one with the smallest namespace and name, so it does not
depend on the order of the input.
Annotation booleans (`use-regex`, `ssl-redirect`, `force-ssl-redirect`,
`enable-cors`) are parsed with `strconv.ParseBool`; an unparsable value is
treated as unset and records a `VAR-PLAN-0007` warning.

Hosts for TLS cases use `scheme: https` with `sni` equal to the host. A
`spec.tls` entry with no hosts is the default certificate and covers every rule
host of the Ingress, and a host served over TLS answers plain HTTP with a
redirect by default. `ssl-redirect: "false"` or `force-ssl-redirect: "false"`
suppresses that redirect on a TLS host and records a note instead. A wildcard
host `*.example.com` is planned as `varco-probe.example.com` with a
`VAR-PLAN-0004` warning. A rule without a host is planned on `--default-host`, or
reported as `VAR-PLAN-0005`.

## Findings

Findings are the `unsupported` array: work varco could not plan.

| Code | Meaning |
|---|---|
| `VAR-PLAN-0001` | an annotation varco cannot test black-box, or one unknown to varco (including `cors-allow-headers` without `enable-cors: true`) |
| `VAR-PLAN-0002` | a regex path whose sample cannot be derived without guessing |
| `VAR-PLAN-0003` | a manifest that cannot be parsed, is larger than the 8 MiB input cap, uses an unsupported Ingress API version, has an annotation that is not a scalar, or yields a case with a control byte |
| `VAR-PLAN-0005` | a rule without a host and no `--default-host` |
| `VAR-PLAN-0008` | an Ingress or rule with no plannable HTTP path, or one that serves only `spec.defaultBackend` |

## Warnings

Warnings are the `warnings` array: conditions that qualify the cases.

| Code | Meaning |
|---|---|
| `VAR-PLAN-0004` | a wildcard host planned with the concrete label `varco-probe` |
| `VAR-PLAN-0006` | host-wide regex scope caused by `use-regex` or `rewrite-target` |
| `VAR-PLAN-0007` | an annotation value is not a boolean, so varco treats it as unset |
| `VAR-PLAN-0009` | annotations ignored by the generic profile, with the count for one Ingress |

Cases are validated before they are emitted: a host, path or header value
containing CR, LF or another control byte (or a path with a space or without a
leading `/`) drops the case with a `VAR-PLAN-0003` finding instead of producing a
malformed request.

In the `nginx` profile, annotation handling below applies. The `generic` profile
does not interpret annotation values; it reports their count once per Ingress
with `VAR-PLAN-0009` and emits no annotation-specific findings.
Annotations that are handled and therefore never reported: `use-regex`,
`rewrite-target`, `ssl-redirect`, `force-ssl-redirect`, `enable-cors`,
`cors-allow-headers`, `app-root`, `permanent-redirect`, `temporal-redirect`,
`permanent-redirect-code`, `temporal-redirect-code`,
`kubernetes.io/ingress.class`. Anything else under
`nginx.ingress.kubernetes.io/` is reported as `VAR-PLAN-0001`. Recognized but
not testable black-box are timeouts and body size (`proxy-*`), `upstream-vhost`,
`backend-protocol`, `ssl-passthrough`, snippets (`configuration-snippet`,
`server-snippet`), `custom-headers`, `whitelist-source-range`,
`denylist-source-range`, canary annotations, `auth-*`, CORS tuning
(`cors-*` other than `cors-allow-headers`), and a few smaller groups
(`limit-*`, `session-cookie*`, `server-*`, `modsecurity*`, `opentelemetry*`).
Controller ConfigMap defaults are not visible in an Ingress at all, so they are
never planned and never guessed.

## Determinism guarantees

- Same input, same output: cases are sorted by a stable key, findings and
  warnings are sorted and de-duplicated, and no map iteration reaches the output.
- The output does not depend on the order of the input resources: the same
  resources in any order produce the same plan, byte for byte. The two parts
  that span several resources are chosen by a stable rule: the `head` and
  `host-variant` cases use the smallest planned path of their host, and the
  `VAR-PLAN-0006` warning names the Ingress with the smallest namespace and
  name among those that turned host-wide regex matching on.
- No I/O and no clock inside `internal/ingress` and `internal/plan`; the CLI is
  the only place that reads files.
- Unsupported work is always listed, never silently dropped.

## Known limitations

- The generic profile ignores controller annotations rather than translating
  their behavior; use `nginx` when those annotations are under test.
- `ssl-redirect` on a host without `spec.tls` plans an `http-redirect` case and cases that are still served with `scheme: http`, so some cases differ only by family and note.
- When `--ingress-class` excludes an Ingress, that resource is dropped without a finding, and regex scope is computed over the included resources only, so an excluded `use-regex` Ingress changes the host scope silently.
- Not-testable annotation classification is name-based and is not reclassified per annotation.
