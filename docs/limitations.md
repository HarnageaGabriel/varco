# Limitations

`varco` compares two live endpoints. It is not a proof that the new controller
is a drop-in replacement for the old one. This page says what a green
`EQUIVALENT` verdict does and does not establish, so nobody over-trusts it.

Every statement below cites the ADR, the doc page or the code location it
comes from. Where a claim could not be checked against this repository, it is
not made.

## 1. What a verdict covers

`EQUIVALENT` means **equivalent on the covered cases** - not equivalent
everywhere. The verdict model states it in one line: coverage is reported
beside the verdict and "every report states what was not covered"
([ADR 0007](adr/0007-verdict-model.md); also the Coverage section of
[run.md](run.md)). A verdict has no percentage threshold: one regression on one
route outweighs any number of passing cases ([ADR 0007](adr/0007-verdict-model.md)).

Four things bound a verdict.

**Only the generated cases.** The verdict covers the cases the plan produced
and the run executed, nothing else. The families and when each one is emitted
are in [plan.md](plan.md) and [ADR 0005](adr/0005-plan-case-families.md). A
behavior varco has no family for is not compared, and no family asserts which
behavior is right: "the cases probe and assert nothing about which behavior is
right: equivalence between old and new is what is compared"
([ADR 0005](adr/0005-plan-case-families.md)).

**Only anonymous requests.** No credential is ever generated
([CONVENTIONS.md](../CONVENTIONS.md), Safety: "No real credentials in generated
cases; none in logs or reports") and no credential-bearing request header is
sent: the prober refuses `Authorization`, `Cookie`, `Proxy-Authorization`,
`X-Api-Key`, `X-Auth-Token`, `Private-Token` and `Api-Key`
([`internal/probe/probe.go`](../internal/probe/probe.go), `CredentialHeader`).
A route behind a login answers `401` or a redirect to a login page, and that
answer is what gets compared - the authenticated content behind it never does
(the [README](../README.md) says the same: "varco only checks the answer to an
anonymous request, for example a 401 or a redirect to a login page").
Authenticated flows are explicitly out of scope in the
[README](../README.md) roadmap.

**Only the probed hosts and paths.** The plan is derived from the Ingress
resources that were read, so a route that is not declared there is never
probed. Three selectors narrow that set further:

- `--ingress-class NAME` drops every Ingress of another class. The drop is
  reported: every excluded Ingress produces a `VAR-PLAN-0011` warning that
  names the Ingress, the class it declares and the class that was requested
  ([plan.md](plan.md), Warnings). The regex scope is still computed over the
  included resources only, so an excluded `use-regex` Ingress changes the host
  scope silently.
- A rule with no host is planned on `--default-host`, so the probed host is
  one you supplied, not one the manifest declares
  ([plan.md](plan.md); [`internal/plan/generate.go`](../internal/plan/generate.go),
  `resolveHosts`).
- A wildcard host is planned as a concrete substitution
  `varco-probe.<zone>` ([plan.md](plan.md), `VAR-PLAN-0004`).

**Only the coverage the report prints.** The run report carries the numbers
that say how much was observed ([run.md](run.md), Report and Coverage):

| Field | What it counts |
|---|---|
| `planned` | the cases of the plan that survived `--family` and `--exclude-path` |
| `executed` | `planned - skipped` |
| `equivalent`, `different`, `inconclusive` | the per-case outcomes |
| `skipped` | the cases a cancellation never ran, or that `--max-requests` dropped |
| `excluded` | the cases removed by `--family` / `--exclude-path` before the run |
| `unsupported_count`, `unsupported` | the plan's unsupported findings |

`planned` counts the cases that were left to run; the cases removed by
`--family` or `--exclude-path` are never probed and are counted under
`excluded` instead ([run.md](run.md), Request budget and filters;
[`internal/engine/run.go`](../internal/engine/run.go), `FilterCases`, called
from [`internal/cli/run.go`](../internal/cli/run.go)).

## 2. What cannot be tested from outside

Some ingress-nginx behavior has no observable effect on an anonymous request.
varco never guesses it: it is reported as an **unsupported finding** and never
turned into a case ([ADR 0005](adr/0005-plan-case-families.md);
[plan.md](plan.md), Findings).

The unsupported findings the planner produces are these codes of the registry in
[`internal/diag/diag.go`](../internal/diag/diag.go) (`CodePlan*`); each has a
page under [docs/diagnostics](diagnostics/).

| Code | What it means | Where it comes from |
|---|---|---|
| `VAR-PLAN-0001` | an annotation varco cannot test black-box, or one unknown to varco | [`internal/plan/nginx.go`](../internal/plan/nginx.go), `addAnnotationFindings` |
| `VAR-PLAN-0002` | a regex path whose sample cannot be derived without guessing | [`internal/plan/nginx.go`](../internal/plan/nginx.go), `regexSample` |
| `VAR-PLAN-0003` | a manifest that cannot be parsed, is over the 8 MiB input cap, uses an unsupported Ingress API version, has a non-scalar annotation, or yields a case with a control byte | [`internal/ingress/ingress.go`](../internal/ingress/ingress.go), `Parse`; [`internal/plan/generate.go`](../internal/plan/generate.go), `appendCase` |
| `VAR-PLAN-0005` | a rule with no host and no `--default-host` | [`internal/plan/generate.go`](../internal/plan/generate.go), `resolveHosts` |
| `VAR-PLAN-0008` | an Ingress or rule with no plannable HTTP path, or one that serves only `spec.defaultBackend` | [`internal/plan/generate.go`](../internal/plan/generate.go), `addResource` |

`VAR-PLAN-0001` is the one that matters most for a cutover. The annotations
varco classifies as not testable black-box are named in
[`internal/plan/nginx.go`](../internal/plan/nginx.go), `notTestableAnnotation`:

- exactly these names: `upstream-vhost`, `backend-protocol`, `ssl-passthrough`,
  `configuration-snippet`, `server-snippet`, `custom-headers`,
  `whitelist-source-range`, `denylist-source-range`, `service-upstream`,
  `affinity`, `server-alias`, `keepalive`, `upstream-hash-by`;
- every annotation whose name starts with `proxy-`, `auth-`, `canary`,
  `limit-`, `session-cookie`, `opentelemetry`, `modsecurity`, `cors-` or
  `server-`;
- `cors-allow-headers` without `enable-cors: true`, which has no effect
  without it.

The annotations varco *handles* - `use-regex`, `rewrite-target`,
`ssl-redirect`, `force-ssl-redirect`, `enable-cors`, `cors-allow-headers`,
`app-root`, `permanent-redirect`, `temporal-redirect`,
`permanent-redirect-code` and `temporal-redirect-code` - produce no
`VAR-PLAN-0001` at all, because they are translated into cases
([`internal/plan/nginx.go`](../internal/plan/nginx.go), `isHandledAnnotation`).
Every other `nginx.ingress.kubernetes.io/*` annotation produces one: the
not-testable ones with the message "cannot be tested black-box by varco", the
ones varco does not know with "is unknown to varco" (same file,
`annotationMessage`). Controller ConfigMap defaults are not visible in an
Ingress at all, so they are never planned and never guessed
([plan.md](plan.md)).

Four codes are **warnings**, not findings: they qualify the cases rather than
replace them. `VAR-PLAN-0004` (wildcard host substituted),
`VAR-PLAN-0006` (host-wide regex scope), `VAR-PLAN-0007` (an annotation value
that is not a boolean) and `VAR-PLAN-0009` (annotations ignored by the
`generic` profile) are listed in [plan.md](plan.md), Warnings. A fifth,
`VAR-PLAN-0010`, names an ingress-nginx controller ConfigMap setting that can
change what is on the wire; the fixed table of 16 keys is in
[`internal/plan/configmap.go`](../internal/plan/configmap.go). It is only
produced with `--from-cluster`, because the ConfigMap is read from the cluster
and nowhere else ([`internal/cli/plan.go`](../internal/cli/plan.go),
`loadClusterPlan`), and only in the `nginx` profile: both callers add the
warnings only when `result.Profile == plan.ProfileNginx`
([`internal/cli/plan.go`](../internal/cli/plan.go);
[`internal/cli/run.go`](../internal/cli/run.go),
`loadRunPlanFromClusterWithRunner`).

## 3. Sampling limits

**Regex paths are sampled, not enumerated.** A path that contains regex
metacharacters is reduced to one literal request path, and only when the
pattern has a literal prefix with an optional `^` anchor and one of the
suffixes `(/|$)(.*)`, `(/|$)`, `(/.*)`, `/(.*)`, `/.*` or `(.*)`. Anything else
is reported as `VAR-PLAN-0002` and produces no case at all, because "guessing a
matching path would produce a case whose expected result nobody can state"
([ADR 0005](adr/0005-plan-case-families.md);
[`internal/plan/nginx.go`](../internal/plan/nginx.go), `regexSample`).
One sample per regex path is not a statement that every matching path behaves
the same.

**Regex scope is host-wide.** `use-regex` on any Ingress, or a `rewrite-target`
anywhere on the host, makes ingress-nginx match *every* path of that host as a
case-insensitive regex, including paths declared by other Ingress resources
([ADR 0005](adr/0005-plan-case-families.md); [plan.md](plan.md);
`VAR-PLAN-0006`). varco plans for that, but the scope itself is a property of
the old controller that the new controller is free not to have.

**Wildcard hosts are substituted.** `*.example.com` is planned as
`varco-probe.example.com` with the `VAR-PLAN-0004` warning
([plan.md](plan.md); [`internal/plan/generate.go`](../internal/plan/generate.go),
`resolveHosts`). Only that one substituted host is probed; the other
subdomains the wildcard covers are not.

**No authenticated flows.** Covered in section 1.

**No request bodies.** A probe writes the method, the request target, `Host`,
the planned headers, a `User-Agent: varco` line when the plan carries none, a
`Connection: close` line and a closing empty line - there is no body
([`internal/probe/probe.go`](../internal/probe/probe.go), `buildRequest`), and
the only planned methods are `GET`, `HEAD` and `OPTIONS`
([`internal/plan/generate.go`](../internal/plan/generate.go), the `Method*`
constants). A route that only differs on a `POST` body is invisible.

**No HTTP/2, and no ALPN comparison.** The request line is `HTTP/1.1` and the
TLS client offers exactly one protocol, `http/1.1`
([`internal/probe/probe.go`](../internal/probe/probe.go), `buildRequest` and
`NextProtos`). The negotiated ALPN and cipher suite are captured
([`internal/result/result.go`](../internal/result/result.go)) but the
comparator never reads them: it compares the status, `Location`, the
`Set-Cookie` attributes, a fixed list of security and CORS headers, the TLS
version, the certificate fields and the body hash
([`internal/compare/compare.go`](../internal/compare/compare.go), `Results`).
"TLS version range and ALPN probes" are still open for contributors in the
[README](../README.md) roadmap.

**The TLS version range is not probed.** A probe performs one handshake with
`MinVersion: tls.VersionTLS10` and records the version the server negotiated
([`internal/probe/probe.go`](../internal/probe/probe.go)). Whether the endpoint
also still accepts an older protocol is never tested; only the two negotiated
versions are compared. The negotiated protocol is one of the fields a
`VAR-CMP-0004` difference reports.

**No load, latency or timing comparison.** Load testing is out of scope in the
[README](../README.md). The captured result has no latency or duration field
([`internal/result/result.go`](../internal/result/result.go)) and the comparator
has no timing field, so two endpoints that answer the same request with very
different latencies compare as equivalent. A new connection is opened per
probe on purpose - "a probe must not share connection state with another
endpoint or path" ([`internal/probe/probe.go`](../internal/probe/probe.go)) - so
connection reuse, keep-alive behavior and pooling are not exercised either.

**The rate limit and the request budget change what is covered.**

- `--rate R` (default 5) is a maximum of requests per second **across both
  endpoints**; every request counts, repeats included
  ([run.md](run.md), Flags; [`internal/engine/run.go`](../internal/engine/run.go),
  `rateLimiter`). It paces the run, it does not reduce the coverage: no case
  is dropped because of the rate limit.
- `--max-requests N` caps the total number of requests. When the next case
  would push the run past the budget, that case **and every later case** are
  never probed: they are `INCONCLUSIVE` with the error `budget` and counted as
  skipped ([run.md](run.md), Request budget and filters;
  [`internal/engine/run.go`](../internal/engine/run.go), `budgetEvaluation`).
  A skipped case is not in `executed`, so a run that hit its budget has
  proportionally lower coverage.
- `--repeat N` (1 to 5) probes each endpoint N times per case and every one of
  those requests counts against the rate limit and the budget
  ([run.md](run.md), Repeated observations).

## 4. How redaction can hide differences

Redaction happens at serialization, never before the comparison, so a redacted
value that changed still produces a difference ([ADR 0006](adr/0006-redaction.md)).
But two things are genuinely invisible in the output.

**`Location` query values.** The comparison runs on the raw captured
`Location`, so two different secrets still produce a difference. The report
then replaces every query value, the fragment and the userinfo with the
literal `REDACTED`; scheme, host, path, query keys and query-key order stay
visible ([ADR 0006](adr/0006-redaction.md)). The consequence for a reader is
that a `header.location` row shows `REDACTED` on both sides: the verdict tells
you the redirect target changed, but the table does not tell you which value
changed. `--show-sensitive` prints the raw values and warns on stderr
([run.md](run.md), Flags). Three known limits remain: a secret in a path
segment is not redacted, a secret used as a bare query *key* is printed, and a
URL inside a diagnostic message is only redacted when it starts with `http://`
or `https://` ([ADR 0006](adr/0006-redaction.md), Known limits).

**Cookie values.** `Set-Cookie` is parsed before comparison and only the
cookie *names* and the attributes `Secure`, `HttpOnly`, `SameSite`, `Path`,
`Domain`, the presence of `Max-Age`, the presence of `Expires` and
`Partitioned` are compared. "Cookie values are neither compared nor rendered"
([ADR 0006](adr/0006-redaction.md);
[`internal/compare/compare.go`](../internal/compare/compare.go),
`parseSetCookies`). A changed session token therefore produces **no**
difference at all - and it is not even reported as an unstable field, because
the instability check runs the same comparator over the repeated observations
and that comparator never looks at cookie values
([`internal/engine/run.go`](../internal/engine/run.go), `unstableFields`).
Cookies are also keyed by name in a map, so two `Set-Cookie` headers with the
same name collapse into one
([`internal/compare/compare.go`](../internal/compare/compare.go),
`equalCookieSets`).

Beyond that, the comparator only looks at a fixed set of fields. The status,
`Location`, the `Set-Cookie` attributes, a fixed list of security and CORS
response headers, the TLS version, the certificate fields and the body hash
are compared; the volatile headers `Date`, `Server`, `Via` and `X-Request-Id`
are left out, and the report lists them under `ignored_fields`
([`internal/compare/compare.go`](../internal/compare/compare.go),
`comparedHeaders`; [`internal/policy/policy.go`](../internal/policy/policy.go),
`ignoredFields`). A response header outside that list is captured by the
prober but never compared, and it does not reach the report: the run report
carries the differences, not the responses
([`internal/model/model.go`](../internal/model/model.go), `RunReport`;
[`internal/report/run.go`](../internal/report/run.go), `RunJSON`).

## 5. What the safe defaults imply

The probe-safety defaults are recorded in [ADR 0003](adr/0003-probe-safety.md)
and enforced in [`internal/probe/probe.go`](../internal/probe/probe.go). They
bound what a run can show.

**`GET`, `HEAD` and `OPTIONS` only.** Any other method needs an explicit
opt-in for that case and run ([ADR 0003](adr/0003-probe-safety.md)). `varco
run` refuses a plan that carries another method, with `VAR-RUN-0001` and exit
code 2 ([run.md](run.md), Flags; [`internal/cli/run.go`](../internal/cli/run.go),
`runMethods`). `varco diff` has the same default and an
`--allow-unsafe-methods` opt-in (`varco diff --help`).

**Redirects are never followed.** "A redirect response is compared as received"
([ADR 0003](adr/0003-probe-safety.md)). The prober is a raw socket writer with
no redirect logic ([`internal/probe/probe.go`](../internal/probe/probe.go)), so
the target of a redirect is never fetched: only the `Location` value is
compared. A login redirect is compared as a string, never followed to the page
behind it.

**TLS trust is never asserted.** Certificate verification is disabled for
probes so the certificate details can be captured, and "certificate trust is
not reported as pass or fail" ([ADR 0003](adr/0003-probe-safety.md)). An
expired or untrusted certificate does not fail a run; only the compared leaf
fields differ.

**Rate limiting is always on** and requests go only to the endpoints the user
names ([ADR 0003](adr/0003-probe-safety.md)).

## 6. When `INCONCLUSIVE` is the right answer

A case is `INCONCLUSIVE` when it could not be observed, and the aggregate
verdict is `INCONCLUSIVE` when no case is `DIFFERENT` and at least one case is
`INCONCLUSIVE` ([ADR 0007](adr/0007-verdict-model.md);
[`internal/model/model.go`](../internal/model/model.go), `Verdict`). The
process exit code is `3` ([exit-codes.md](exit-codes.md)).

`INCONCLUSIVE` is the honest answer - not a failure of the comparison, and not
a pass - in these situations:

- one endpoint could not be reached, handshaked, or read: a dial, TLS,
  timeout or read error makes the case `INCONCLUSIVE` with `VAR-NET-0001`
  ([ADR 0007](adr/0007-verdict-model.md); [run.md](run.md), Report).
- the request budget skipped the case (`budget`) or the run was cancelled
  (`cancelled`); both are counted as skipped
  ([`internal/engine/run.go`](../internal/engine/run.go), `budgetEvaluation`
  and `cancelledEvaluation`).
- the status itself was unstable across the repeated observations of one
  endpoint, because "no comparison is meaningful"
  ([run.md](run.md), Repeated observations).

The precedence matters: a proven difference outranks missing data, so a run
with both a `DIFFERENT` and an `INCONCLUSIVE` case exits `1`, not `3`, and the
report still lists every inconclusive case
([ADR 0007](adr/0007-verdict-model.md); [exit-codes.md](exit-codes.md)). Exit
`3` therefore means "not proven different, and not fully observed": the
coverage numbers in the report say how much was actually compared, and a
`DIFFERENT` case is never hidden by an inconclusive one.

## 7. What varco assumes about the two endpoints

The comparison is only meaningful under assumptions varco cannot check from
outside. It makes these:

**The two endpoints receive the same request.** One case produces one request
per side with the default `--repeat 1` (N requests per side with `--repeat
N`), and all of them carry the same `Host`, the same SNI and the same request
target ([`internal/engine/run.go`](../internal/engine/run.go), `runCase`). The
request target is written to the wire byte for byte, so `/a%2Fb`, `/a/../b` and
`//x` reach the server unchanged
([`internal/probe/probe.go`](../internal/probe/probe.go), `Do` and
`buildRequest`; the [README](../README.md), Probe). The `host-variant` family
is the deliberate exception: it changes the `Host` header and keeps the SNI
plain, so only the `Host` line differs
([ADR 0005](adr/0005-plan-case-families.md)).

**Both endpoints serve the routes the manifests declare.** The plan is derived
from one set of Ingress resources, and varco has no way to learn what the new
controller was actually configured with. A route that exists on the old
controller and is missing from the new one shows up as a difference - but only
if the plan probed it, and only for the paths the manifests declare. The
`--ingress-class` filter and the `generic` profile narrow the plan further
([ADR 0008](adr/0008-planner-profiles.md)).

**Both endpoints serve the same data.** The body is compared as a hash, so two
endpoints must be answering for the same content: a different deployment, a
different feature flag, a different cache or a different database behind each
controller produces a body difference that is not a routing difference. The
default `--body 2xx` policy compares the body only when both statuses are
equal and 2xx, precisely because a controller answers an unknown route with
its own `404`/`5xx` page, and those pages differ by design
([run.md](run.md), Body policy; [`internal/compare/compare.go`](../internal/compare/compare.go),
`compareBody`).

**The endpoints are reachable directly by IP.** The endpoint syntax accepts an
IP address only and "never resolves names"
([`internal/endpoint/endpoint.go`](../internal/endpoint/endpoint.go), `Parse`;
[ADR 0004](adr/0004-endpoint-syntax.md)). A run therefore exercises no DNS,
no resolver, no TTL, no load balancer in front of the controllers and no
proxy: it dials the two IPs the user named, with no DNS and no proxy
([README](../README.md), Probe).

**The old endpoint is the reference.** varco compares and reports; it does not
decide which side is right. The plan's notes describe the ingress-nginx
behavior the case was built from, and a case "probes and asserts nothing about
which behavior is right" ([ADR 0005](adr/0005-plan-case-families.md)). The
explanations attached to a `DIFFERENT` case point at a known ingress-nginx
behavior; several of them carry a `VERIFY:` marker because the target
controller's setting still has to be confirmed
([run.md](run.md), Explanations).

## 8. What to verify manually before a DNS cutover

varco does not cover the following. Check them yourself before you move the
record; each item names the reason it is outside varco's reach.

1. **Authenticated flows.** A login, a session, a per-user response. varco
   sends no credential and no credential header
   ([ADR 0003](adr/0003-probe-safety.md)).
2. **Write methods and request bodies.** `POST`, `PUT`, `PATCH` and the
   payload they carry. varco plans `GET`, `HEAD` and `OPTIONS` only
   ([ADR 0003](adr/0003-probe-safety.md)).
3. **HTTP/2 and ALPN.** The probe is HTTP/1.1 with a single offered protocol
   ([`internal/probe/probe.go`](../internal/probe/probe.go)).
4. **The TLS version range, cipher suites, client-certificate (mTLS)
   authentication and OCSP stapling.** One handshake is performed and the leaf
   fields are compared; trust is never asserted
   ([ADR 0003](adr/0003-probe-safety.md)).
5. **Load, latency, throughput, connection reuse and upstream health under
   traffic.** No timing is compared and load testing is out of scope
   ([README](../README.md)).
6. **WebSocket, streaming and long-lived responses.** A probe reads one
   response and closes the connection
   ([`internal/probe/probe.go`](../internal/probe/probe.go)).
7. **Every annotation in the `VAR-PLAN-0001` list** - timeouts and body size
   (`proxy-*`), `upstream-vhost`, `backend-protocol`, `ssl-passthrough`,
   snippets, `custom-headers`, source-range allow and deny lists, canary
   weights, `auth-*`, CORS tuning, rate limits, session cookies,
   `server-*`, `modsecurity*` and `opentelemetry*` - plus any annotation
   varco reports as unknown. The list is in
   [`internal/plan/nginx.go`](../internal/plan/nginx.go), `notTestableAnnotation`.
8. **The controller ConfigMap settings on both controllers.** The 16 keys
   varco names (redirects, HSTS, TLS policy, CORS, `use-http2`, auth, snippets,
   error pages) are in [`internal/plan/configmap.go`](../internal/plan/configmap.go);
   `VAR-PLAN-0010` only fires for the values it read from one controller.
9. **Wildcard hosts.** varco probes `varco-probe.<zone>`, not your real
   subdomains, and never the wildcard certificate they share
   ([plan.md](plan.md); [ADR 0005](adr/0005-plan-case-families.md)).
10. **Routes that no Ingress declares**, and the Ingresses that
    `--ingress-class` left out of the plan: they are reported as
    `VAR-PLAN-0011`, but no case was planned for them
    ([plan.md](plan.md), Known limitations).
11. **DNS itself**: the record, its TTL, the resolver caches and the moment of
    the switch. varco dials IPs and never resolves a name
    ([`internal/endpoint/endpoint.go`](../internal/endpoint/endpoint.go)).
12. **The content behind the hash.** varco compares a body hash, not the
    rendered page ([run.md](run.md), Body policy).

## Sources

| Page | What it decides |
|---|---|
| [ADR 0003](adr/0003-probe-safety.md) | safe methods, no redirect following, rate limiting, TLS trust |
| [ADR 0004](adr/0004-endpoint-syntax.md) | the endpoint syntax; no name resolution |
| [ADR 0005](adr/0005-plan-case-families.md) | the case families, the sampling rules, the refusals |
| [ADR 0006](adr/0006-redaction.md) | what is redacted, when, and the known limits |
| [ADR 0007](adr/0007-verdict-model.md) | per-case outcome, aggregate verdict, exit code |
| [ADR 0008](adr/0008-planner-profiles.md) | the `nginx` and `generic` profiles |
| [plan.md](plan.md) | the families, the findings, the warnings, the known limitations |
| [run.md](run.md) | the flags, the body policy, the budget, the report, the exit codes |
| [exit-codes.md](exit-codes.md) | the meaning of exit code 3 |
| [architecture.md](architecture.md) | which file makes which decision |
