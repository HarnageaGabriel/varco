# varco run

`varco run` executes a probe plan against two endpoints, evaluates the observed
differences through a separate policy layer, and prints a verdict. The plan is
either the JSON produced by `varco plan --format json` or manifests planned
in-process with the same options `varco plan` takes. Nothing but the two
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
```

## Flags

| Flag | Meaning |
|---|---|
| `--plan FILE\|-` | plan JSON from `varco plan --format json`; `-` reads stdin. `schema_version` must be 1. |
| `-f <file\|directory\|->` | manifests to plan in-process (repeatable); mutually exclusive with `--plan`. |
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
| `--quiet` | disable the `[i/n]` progress lines on stderr. |
| `--show-sensitive` | print raw `Location` and error values instead of `REDACTED`; warns on stderr. |

Only the methods present in the plan are sent, and only `GET`, `HEAD` and
`OPTIONS` are allowed; any other method is refused with `VAR-RUN-0001`
(exit 2). Old and new are probed back to back for each case, one global rate
limiter covers every request to either endpoint, and cases are emitted in plan
order regardless of completion order.

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
| `unsupported_count`, `unsupported` | the plan's unsupported findings. |
| `expired_exceptions` | ids of exceptions past their `expires` date. |
| `ignored_fields` | fields left out of every comparison, reported so nothing is invisible. |
| `cases` | one evaluation per case, in plan order: `case_id`, `outcome`, `differences`, `error`. |
| `redacted` | `true` unless `--show-sensitive`. |

`--format table` prints a summary block (verdict, coverage, unsupported count,
expired exceptions, ignored fields) followed by one row per reportable case and
its differences. A case is reportable when it is not `EQUIVALENT` or when policy
accepted all of its differences; accepted rows are marked
`accepted (<exception id>): <reason>`.

Skipped cases (a cancelled run) are `INCONCLUSIVE` with the error `cancelled`. A
probe failure on either endpoint is `INCONCLUSIVE` with `VAR-NET-0001`.

### Coverage

`EQUIVALENT` means equivalent on the covered cases. `planned` is the number of
cases, `skipped` the cases a cancellation never ran, and
`executed = planned - skipped`. Every report states what was not covered.

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