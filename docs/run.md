# varco run

`varco run` executes a probe plan against two endpoints, evaluates the observed
differences through a separate policy layer, and prints a verdict. The plan is
either the JSON produced by `varco plan --format json`, manifests planned
in-process with the same options `varco plan` takes, or Ingresses read from a
live cluster with `--from-cluster` (kubectl get only). Nothing but the two
endpoints you name is contacted.

The three layers — per-case outcome, aggregate verdict, exit code — are recorded
in [ADR 0007](adr/0007-verdict-model.md). Redaction of every sensitive value is
recorded in [ADR 0006](adr/0006-redaction.md). Probe safety is recorded in
[ADR 0003](adr/0003-probe-safety.md).

## Usage

```sh
varco run --plan plan.json --old 10.0.0.1 --new 10.0.0.2
varco run -f ingress.yaml --ingress-class nginx --old 10.0.0.1 --new 10.0.0.2 --format json
varco run --plan plan.json --old 10.0.0.1 --new 10.0.0.2 --exceptions exceptions.yaml
varco run --plan plan.json --old 10.0.0.1 --new 10.0.0.2 \
  --old-http 10.0.0.1:30080 --new-http 10.0.0.2:30080   # kind / NodePort
varco run --plan plan.json --old 10.0.0.1 --new 10.0.0.2 --dry-run
varco run --plan plan.json --old 10.0.0.1 --new 10.0.0.2 --repeat 3 \
  --suggest-exceptions suggested-exceptions.yaml
varco run --from-cluster --old 10.0.0.1 --new 10.0.0.2
```

## Flags

| Flag | Meaning |
|---|---|
| `--plan FILE\|-` | plan JSON from `varco plan --format json`; `-` reads stdin. `schema_version` must be 1. |
| `-f <file\|directory\|->` | manifests to plan in-process (repeatable); mutually exclusive with `--plan`. |
| `--from-cluster` | read Ingresses and the controller ConfigMap with `kubectl get`; mutually exclusive with `--plan` and `-f`. |
| `--namespace NS` | read Ingresses from this namespace (with `--from-cluster`; default: all namespaces). |
| `--kube-context C` / `--kubeconfig F` / `--kube-timeout D` | select or bound kubectl access (with `--from-cluster`; `D` defaults to 30s). |
| `--controller-configmap NS/NAME` | controller ConfigMap to read with `--from-cluster` (`ingress-nginx/ingress-nginx-controller` by default; empty disables). |
| `--profile (nginx\|generic)` | planner profile for `-f`/`--from-cluster` input ([ADR 0008](adr/0008-planner-profiles.md)). Default `nginx`. |
| `--ingress-class NAME` | plan only Ingress resources of this class (with `-f`). |
| `--default-host HOST` | host for rules that declare none (with `-f`). |
| `--old IP` / `--new IP` | the two endpoints; port 80 for `http` cases, 443 for `https` cases. |
| `--old-http`, `--old-https`, `--new-http`, `--new-https` | per-scheme overrides that accept `IP:port` ([ADR 0004](adr/0004-endpoint-syntax.md)), for NodePort and `kind` setups. |
| `--exceptions FILE` | accepted-difference policy; see below. |
| `--format (table\|json)` | output format. Default `table`. |
| `--output FILE` | write the report here instead of stdout. |
| `--rate R` | maximum requests per second across **both** endpoints. Default 5. |
| `--concurrency N` | cases probed in parallel. Default 4, allowed 1..64. |
| `--timeout D` | per-request timeout. Default 10s. |
| `--repeat N` | probe each endpoint N times per case (1..5). Default 1. Every repeat is a real request and counts against `--rate`. |
| `--body (2xx\|hash\|none)` | when the response body hash is compared. Default `2xx`. |
| `--max-requests N` | stop after N requests in total; `0` (the default) means unlimited. |
| `--family NAME` | run only cases of this family (repeatable). |
| `--exclude-path GLOB` | exclude cases whose request path matches this `path.Match` glob (repeatable). |
| `--dry-run` | print the requests a run would send and exit without probing. |
| `--suggest-exceptions FILE` | write an exceptions file for the fields observed unstable; never overwrites an existing file. |
| `--quiet` | disable the `[i/n]` progress lines on stderr. |
| `--show-sensitive` | print raw `Location` and error values instead of `REDACTED`; warns on stderr. |

Only the methods present in the plan are sent, and only `GET`, `HEAD` and
`OPTIONS` are allowed; any other method is refused with `VAR-RUN-0001`
(exit 2). Old and new are probed back to back for each case, repeated
`--repeat` times per endpoint, and one global rate limiter covers every request
to either endpoint (repeats included); cases are emitted in plan order
regardless of completion order.

## Repeated observations

Unstable fields are a fact of real clusters: a header that carries a timestamp,
a rotating `Set-Cookie`, a status that depends on which replica answers.
`--repeat N` probes each endpoint N times per case (1 to 5; default 1), and every
one of those requests is a real request that the rate limiter counts.

A field that takes different values across the N observations of the *same*
endpoint is **unstable**. An unstable field never makes a case `DIFFERENT`:

- a difference on an unstable field is kept in the report as accepted, with the
  reason `unstable: differs between repeated observations of <old|new>` and the
  exception id `auto:unstable`;
- if `status` itself is unstable on either endpoint, the case is `INCONCLUSIVE`
  with the error `unstable status`, because no comparison is meaningful.

`--suggest-exceptions FILE` writes a valid exceptions file (version 1) with one
entry per `(host, field)` pair the run observed unstable, so the acceptance is
explicit and reviewable on the next run. Each entry uses the exact host as its
`match.host`, the reason `observed unstable across N observations`, and expires
30 days after the run date. varco never overwrites an existing file: if the file
already exists it exits 2.

## Body policy

`--body` decides when the response body hash is compared:

| Value | Behavior |
|---|---|
| `2xx` | the default: compare the body only when both statuses are equal and 2xx. |
| `hash` | always compare the body hash. |
| `none` | never compare the body hash. |

The default exists because a controller answers an unknown or unmatched route
with its own `404`/`5xx` page, and those pages differ between controllers by
design. Comparing them would report a difference that is not a routing
difference. The policy in force is always recorded in the report as
`body_policy`.

## Request budget and filters

- `--max-requests N` caps the total number of requests a run sends (`0`, the
  default, means unlimited). When the next case would push the run past the
  budget, that case and every later case are never probed: they are
  `INCONCLUSIVE` with the error `budget` and counted as skipped.
- `--family NAME` (repeatable) runs only cases of the named families.
- `--exclude-path GLOB` (repeatable) drops cases whose request path matches the
  glob, using `path.Match` semantics (`*` does not cross `/`, so use `/api` and
  `/api/*` to cover a route and its subpaths).

Filtered-out cases are not planned into the run at all; the report counts them
under `excluded`.

## Dry run

`--dry-run` makes no network connection. It prints one line per request the run
*would* send - `side scheme endpoint method host sni path`, with `-` for an
absent SNI - followed by a `TOTAL <n>` line, and exits 0. It honors `--repeat`,
`--family`, `--exclude-path` and `--max-requests`, so a plan can be reviewed
before anything is contacted.

## Exceptions file

An exceptions file is versioned YAML. Every problem in it is `VAR-CFG-0002`
(exit 2): unknown keys, a missing or duplicate `id`, a missing `reason`, a bad
date, or an invalid glob.

```yaml
version: 1
exceptions:
  - id: server-header            # required, unique
    reason: "Envoy sets its own Server header"   # required
    expires: 2026-12-31          # optional date; after it passes the exception
                                 # is ignored and listed under expired_exceptions
    match:                       # all present fields must match; omitted = any
      host: "*.example.com"      # path.Match glob on the request host
      path: "/api/*"             # path.Match glob on the request path
      family: trailing-slash     # exact case family (docs/plan.md)
      case: 3f2a9c1b04de         # exact case id
      field: "header:Server"     # exact comparator field or a shorthand (below)
```

An accepted difference is never removed: it stays in the report marked
`accepted`, with the reason and the exception id.

### `match.field` reference

`field` matches the comparator's field name exactly, and these documented
shorthands map onto it:

| Shorthand | Comparator field |
|---|---|
| `status` | `status` |
| `location` | `header.location` |
| `cookie` / `set-cookie` | `header.set-cookie` (one difference covers every cookie) |
| `tls-version` | `tls.version` |
| `certificate` | `certificate.<field>` (any certificate field) |
| `header:<Name>` | `header.<lowercased name>` |

The full comparator field set is `status`, `header.location`,
`header.set-cookie`, `header.<response-header>` for the compared headers, the
trailer headers as `Trailer:<Name>`, `tls.version`, `certificate.<field>`
(`subject`, `issuer`, `not_after`, `fingerprint`, `dns_sans`) and `body`.

## Report

`--format json` writes a versioned `RunReport` with ordered fields:

| Field | Meaning |
|---|---|
| `schema_version` | always `1`. |
| `verdict` | `EQUIVALENT`, `DIFFERENT` or `INCONCLUSIVE`. |
| `coverage` | `planned`, `executed`, `equivalent`, `different`, `inconclusive`, `skipped`. |
| `repeat` | the `--repeat` value this run used. |
| `body_policy` | the `--body` policy in force (`2xx`, `hash` or `none`). |
| `excluded` | cases removed by `--family`/`--exclude-path` before the run. |
| `unsupported_count`, `unsupported` | the plan's unsupported findings. |
| `expired_exceptions` | ids of exceptions past their `expires` date. |
| `ignored_fields` | fields left out of every comparison, reported so nothing is invisible. |
| `cases` | one evaluation per case, in plan order: `case_id`, `outcome`, `differences`, `explanations` when present, `error`. |
| `redacted` | `true` unless `--show-sensitive`. |

The last three fields are additive; `schema_version` stays `1`.

`--format table` prints a summary block (verdict, coverage, body policy, repeat,
excluded, unsupported count, expired exceptions, ignored fields) followed by one
row per reportable case and its differences. Each row carries the case `ID`,
`HOST`, `METHOD`, `PATH` and `FAMILY` beside the outcome, so a difference is
attributable without opening the plan JSON. A `body` value in the table is
shortened to `sha256=<first 12 hex> size=<n>`; the JSON report keeps the full
value. A case is reportable when it is not `EQUIVALENT` or when policy accepted
all of its differences; accepted rows are marked
`accepted (<exception id>): <reason>`.

After a `DIFFERENT` case's difference rows, the table prints one indented line
per explanation in the form `why: <summary>  fix: <fix>  see: <source>`. JSON
uses the additive `explanations` array on that case, with `schema_version`
unchanged.

Skipped cases (a cancelled run, or one dropped by `--max-requests`) are
`INCONCLUSIVE` with the error `cancelled` or `budget`. A probe failure on either
endpoint is `INCONCLUSIVE` with `VAR-NET-0001`.

### Coverage

`EQUIVALENT` means equivalent on the covered cases. `planned` is the number of
cases, `skipped` the cases a cancellation never ran, and
`executed = planned - skipped`. Every report states what was not covered.

### Explanations

Explanations are deterministic and ordered; each applies only to a `DIFFERENT`
case whose family and differing fields match the trigger.

| Rule id | Trigger | Source |
|---|---|---|
| `nginx-regex-case-insensitive` | `case-flip` with a `status` difference. ingress-nginx matches `use-regex`/`rewrite-target` paths case-insensitively while Gateway API matching is case-sensitive. | [Before you migrate](https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate), behaviors 1–3 |
| `nginx-regex-prefix-host-wide` | `prefix-boundary` or `subpath` with a `status` difference on a host with regex scope. ingress-nginx regex paths are prefix matches and `use-regex` is host-wide. | [Before you migrate](https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate), behaviors 1–2 |
| `nginx-trailing-slash-redirect` | `trailing-slash` with a `status` difference where the old status is `301`; ingress-nginx redirects a missing trailing slash. | [Before you migrate](https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate), behavior 4 |
| `nginx-url-normalization` | `dot-segments` or `double-slash` with a `status` or `header.location` difference; ingress-nginx normalizes dot-segments and repeated slashes before matching. VERIFY the new controller's Envoy Gateway ClientTrafficPolicy path-settings name. | [Before you migrate](https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate), behavior 5 |
| `ingress2gateway-rewrite-target` | `rewrite` with a `body` difference; `rewrite-target` may translate differently and capture groups are not supported by ingress2gateway. | [ingress2gateway ingress-nginx provider notes](https://github.com/kubernetes-sigs/ingress2gateway/blob/main/pkg/i2gw/providers/ingressnginx/README.md) |
| `nginx-http-redirect` | VERIFY: `http-redirect` with a `status` difference; configure an explicit HTTP-to-HTTPS redirect with the same code. VERIFY the ingress-nginx default status code (308 vs 301). | [ingress-nginx ConfigMap](https://kubernetes.github.io/ingress-nginx/user-guide/nginx-configuration/configmap/) |
| `nginx-default-backend` | VERIFY: `default-backend` with a `status`, `body` or certificate difference; decide the new catch-all backend and certificate. | [ingress-nginx CLI arguments](https://kubernetes.github.io/ingress-nginx/user-guide/cli-arguments/) |
| `ingress-nginx-cors-defaults` | VERIFY: `cors-preflight` with a `header.access-control-*` difference; configure allowed origins, methods, headers and max-age explicitly. | [ingress-nginx annotations](https://kubernetes.github.io/ingress-nginx/user-guide/nginx-configuration/annotations/) |

## Exit codes

`0` when the verdict is `EQUIVALENT`, `1` for `DIFFERENT`, `3` for
`INCONCLUSIVE`, `2` for usage or configuration errors, `4` for internal errors.
A proven difference outranks missing data: `1` wins over `3`. See
[exit-codes.md](exit-codes.md).

## Examples

```sh
# Execute a plan against a staging pair and fail CI on any unaccepted difference.
varco run --plan plan.json --old 10.0.0.1 --new 10.0.0.2 --format json > report.json

# Accept a known Server header difference for one host family.
cat > exceptions.yaml <<'YAML'
version: 1
exceptions:
  - id: server-header
    reason: "Envoy sets its own Server header"
    match:
      family: trailing-slash
      field: "header:Server"
YAML
varco run --plan plan.json --old 10.0.0.1 --new 10.0.0.2 --exceptions exceptions.yaml
```

## Determinism

Same input, same output: cases are emitted in plan order regardless of
completion order, no map iteration reaches the output, and the clock is injected
(used only to expire exceptions). No AI is in the main path.
