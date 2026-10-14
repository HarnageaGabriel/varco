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
varco plan -f api.yaml --ingress-class nginx --default-host fallback.example.com
```

Flags: `-f <file|directory|->` (repeatable; a directory is walked recursively
and only regular `*.yaml`, `*.yml` and `*.json` files are read, sorted by path;
symlinks and other non-regular files are skipped), `--format (table|json)`,
`--ingress-class NAME` (matches `spec.ingressClassName` or the
`kubernetes.io/ingress.class` annotation), `--default-host HOST` (used by rules
that declare no host). One input, file or stream, is read through an 8 MiB cap;
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

`--format json` writes a stable envelope with `schema_version: 1`, `cases`,
`unsupported` and `warnings`; `--format table` prints the same three sections.
Cases are ordered by `(host, path, scheme, method, family, id)` and identical
cases are de-duplicated. `id` is the first 12 hex characters of the SHA-256 of
the canonical case (host, SNI, scheme, method, path, sorted headers, family,
source, note), so an ID is stable across runs and independent of generation
order. No output order depends on map iteration, and no case carries a clock, a
credential header or a method other than `GET`, `HEAD` or `OPTIONS`.

## Case families

Each family isolates one behavior so a difference can be attributed to it.

| Family | Request | Why it exists |
|---|---|---|
| `base` | the path verbatim | the declared route |
| `trailing-slash` | toggled trailing slash | ingress-nginx answers a missing slash on a path declared with one with `301`; regex paths do not redirect (behavior 4) |
| `subpath` | `path + /varco-probe` | a deeper path must still route |
| `prefix-boundary` | `path + varco` (for example `/api` -> `/apivarco`) | `Prefix` matches element-wise, so this must not match; with `use-regex` the prefix is a regex and it may match (behavior 1) |
| `case-flip` | path with letter case swapped | `use-regex` and `rewrite-target` make matching case-insensitive for the whole host (behaviors 1 to 3) |
| `dot-segments` | `/varco/../` + path | URL normalization resolves `..` before matching (behavior 5) |
| `double-slash` | `//` + path | repeated slashes are normalized or answered with `301` (behavior 5) |
| `http-redirect` | plain HTTP, same path | a host with TLS, or with `ssl-redirect`/`force-ssl-redirect` set true, answers HTTP with a redirect; an explicit false on a TLS host suppresses it |
| `cors-preflight` | `OPTIONS` with `Origin: https://varco.invalid` and `Access-Control-Request-Method: GET` | `enable-cors` answers preflight (plus `Access-Control-Request-Headers` when `cors-allow-headers` is set) |
| `rewrite` | the `base` and `subpath` requests | `rewrite-target` rewrites the upstream path (behavior 3) |
| `app-root` | `GET /` | the `app-root` annotation redirects `/` to the application root |
| `redirect-annotation` | `GET path` | `permanent-redirect` and `temporal-redirect` answer with a redirect |

Rule host and path type decide what is emitted: `Exact` paths get `base`,
`trailing-slash` (unless regex), `case-flip` (when matching is case-insensitive),
`dot-segments` and `double-slash`; `Prefix` adds `subpath` and
`prefix-boundary`; `ImplementationSpecific` is treated as `Prefix`-like with a
note, because that is what ingress-nginx does. An omitted path is treated as `/`
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
Ingress resources, and records a `VAR-PLAN-0006` warning naming the host.

Hosts for TLS cases use `scheme: https` with `sni` equal to the host. A
`spec.tls` entry with no hosts is the default certificate and covers every rule
host of the Ingress, and a host served over TLS answers plain HTTP with a
redirect by default. `ssl-redirect: "false"` or `force-ssl-redirect: "false"`
suppresses that redirect on a TLS host and records a note instead. A wildcard
host `*.example.com` is planned as `varco-probe.example.com` with a
`VAR-PLAN-0004` warning. A rule without a host is planned on `--default-host`, or
reported as `VAR-PLAN-0005`.

## Findings

| Code | Meaning |
|---|---|
| `VAR-PLAN-0001` | an annotation varco cannot test black-box, or one unknown to varco |
| `VAR-PLAN-0002` | a regex path whose sample cannot be derived without guessing |
| `VAR-PLAN-0003` | a manifest that cannot be parsed or is larger than the 8 MiB input cap, or uses an unsupported Ingress API version |
| `VAR-PLAN-0004` | a wildcard host planned with the concrete label `varco-probe` |
| `VAR-PLAN-0005` | a rule without a host and no `--default-host` |
| `VAR-PLAN-0006` | host-wide regex scope caused by `use-regex` or `rewrite-target` |

Annotations that are handled and therefore never reported: `use-regex`,
`rewrite-target`, `ssl-redirect`, `force-ssl-redirect`, `enable-cors`,
`cors-allow-headers`, `app-root`, `permanent-redirect`, `temporal-redirect`,
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
- No I/O and no clock inside `internal/ingress` and `internal/plan`; the CLI is
  the only place that reads files.
- Unsupported work is always listed, never silently dropped.
