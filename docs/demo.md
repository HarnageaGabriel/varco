# 60-second local demo

This demo needs only Go and a source checkout. It starts two plain-HTTP
servers on loopback; it does not need or contact a Kubernetes cluster.

## Start the endpoints

From the repository root, run:

```sh
make demo
```

The captured output is:

```text
old ingress-nginx-like server: http://127.0.0.1:18081
new controller-like server: http://127.0.0.1:18082

varco plan -f testdata/demo/ingress.yaml --format json > plan.json
varco run --plan plan.json --old 127.0.0.1:18081 --new 127.0.0.1:18082 --dry-run
varco run --plan plan.json --old 127.0.0.1:18081 --new 127.0.0.1:18082

Expected verdict: DIFFERENT (exit code 1)
```

Leave that terminal running. In another terminal, generate and review the
plan:

```sh
varco plan -f testdata/demo/ingress.yaml --format json > plan.json
varco run --plan plan.json --old 127.0.0.1:18081 --new 127.0.0.1:18082 --dry-run
```

The plan command writes `plan.json` and prints nothing. The dry run prints:

```text
old http 127.0.0.1:18081 GET demo.example.com - //app
new http 127.0.0.1:18082 GET demo.example.com - //app
old http 127.0.0.1:18081 GET demo.example.com - //health
new http 127.0.0.1:18082 GET demo.example.com - //health
old http 127.0.0.1:18081 GET demo.example.com - /APP
new http 127.0.0.1:18082 GET demo.example.com - /APP
old http 127.0.0.1:18081 GET demo.example.com - /HEALTH
new http 127.0.0.1:18082 GET demo.example.com - /HEALTH
old http 127.0.0.1:18081 GET demo.example.com - /app
new http 127.0.0.1:18082 GET demo.example.com - /app
old http 127.0.0.1:18081 GET demo.example.com - /app
new http 127.0.0.1:18082 GET demo.example.com - /app
old http 127.0.0.1:18081 GET demo.example.com - /app
new http 127.0.0.1:18082 GET demo.example.com - /app
old http 127.0.0.1:18081 GET demo.example.com - /app
new http 127.0.0.1:18082 GET demo.example.com - /app
old http 127.0.0.1:18081 HEAD demo.example.com - /app
new http 127.0.0.1:18082 HEAD demo.example.com - /app
old http 127.0.0.1:18081 GET demo.example.com - /app/varco-probe
new http 127.0.0.1:18082 GET demo.example.com - /app/varco-probe
old http 127.0.0.1:18081 GET demo.example.com - /appvarco
new http 127.0.0.1:18082 GET demo.example.com - /appvarco
old http 127.0.0.1:18081 GET demo.example.com - /health
new http 127.0.0.1:18082 GET demo.example.com - /health
old http 127.0.0.1:18081 GET demo.example.com - /health/varco-probe
new http 127.0.0.1:18082 GET demo.example.com - /health/varco-probe
old http 127.0.0.1:18081 GET demo.example.com - /healthvarco
new http 127.0.0.1:18082 GET demo.example.com - /healthvarco
old http 127.0.0.1:18081 GET demo.example.com - /varco-unmatched-route
new http 127.0.0.1:18082 GET demo.example.com - /varco-unmatched-route
old http 127.0.0.1:18081 GET demo.example.com - /varco/../app
new http 127.0.0.1:18082 GET demo.example.com - /varco/../app
old http 127.0.0.1:18081 GET demo.example.com - /varco/../health
new http 127.0.0.1:18082 GET demo.example.com - /varco/../health
old http 127.0.0.1:18081 GET varco-unknown.invalid - /
new http 127.0.0.1:18082 GET varco-unknown.invalid - /
TOTAL 36
```

The required `use-regex: 'true'` annotation makes the nginx planner include
the `base`, `case-flip`, `subpath`, `trailing-slash`, `dot-segments`, and
`double-slash` families for the literal `/app` Prefix and `/health` Exact
paths. Synthetic head, host-variant, unmatched-route, and default-backend
coverage brings this plan to 18 cases.

## Run and read the report

```sh
varco run --plan plan.json --old 127.0.0.1:18081 --new 127.0.0.1:18082
```

After the progress lines, the captured table is:

```text
VERDICT             DIFFERENT
COVERAGE            planned=18 executed=18 equivalent=11 different=7 inconclusive=0 skipped=0
BODY POLICY         2xx
REPEAT              1
EXCLUDED            0
UNSUPPORTED         0
EXPIRED EXCEPTIONS  -
IGNORED FIELDS      Date, Server, Via, X-Request-Id, Set-Cookie values
CASES 7
ID            HOST              METHOD  PATH     FAMILY        OUTCOME    FIELD            CODE          OLD    NEW       NOTE
92f915f3076e  demo.example.com  GET     /APP     case-flip     DIFFERENT  status           VAR-CMP-0001  200    404       -
                                                                                                                          why: ingress-nginx matches use-regex/rewrite-target paths case-insensitively; Gateway API matches are case-sensitive.  fix: Use a RegularExpression match with (?i), or correct the path casing.  see: https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate
27589c9d24fc  demo.example.com  GET     /HEALTH  case-flip     DIFFERENT  status           VAR-CMP-0001  200    404       -
                                                                                                                          why: ingress-nginx matches use-regex/rewrite-target paths case-insensitively; Gateway API matches are case-sensitive.  fix: Use a RegularExpression match with (?i), or correct the path casing.  see: https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate
2e0f2d3183d3  demo.example.com  GET     /app     base          DIFFERENT  header.location  VAR-CMP-0002  /app/  (absent)  -
2e0f2d3183d3  demo.example.com  GET     /app     base          DIFFERENT  status           VAR-CMP-0001  301    200       -
3d4115a477f9  demo.example.com  GET     /app     host-variant  DIFFERENT  header.location  VAR-CMP-0002  /app/  (absent)  -
3d4115a477f9  demo.example.com  GET     /app     host-variant  DIFFERENT  status           VAR-CMP-0001  301    200       -
c3714732a32f  demo.example.com  GET     /app     host-variant  DIFFERENT  header.location  VAR-CMP-0002  /app/  (absent)  -
c3714732a32f  demo.example.com  GET     /app     host-variant  DIFFERENT  status           VAR-CMP-0001  301    200       -
f2dad9faf490  demo.example.com  GET     /app     host-variant  DIFFERENT  header.location  VAR-CMP-0002  /app/  (absent)  -
f2dad9faf490  demo.example.com  GET     /app     host-variant  DIFFERENT  status           VAR-CMP-0001  301    200       -
888b8bd09e43  demo.example.com  HEAD    /app     head          DIFFERENT  header.location  VAR-CMP-0002  /app/  (absent)  -
888b8bd09e43  demo.example.com  HEAD    /app     head          DIFFERENT  status           VAR-CMP-0001  301    200       -
```

The old endpoint matches `/APP` and `/HEALTH` case-insensitively because
`use-regex` applies host-wide; the new endpoint is case-sensitive. The old
endpoint redirects the missing-slash `/app` to `/app/` with 301, while the new
one serves it directly with 200. Both facts match the ingress-nginx behaviors
documented in [Five Surprising Ingress-NGINX Behaviors](https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate).

The endpoints also send different `Server` headers, but the `IGNORED FIELDS`
line shows why no `header.server` difference appears. Date, Server, Via,
X-Request-Id, and Set-Cookie values are deliberately volatile and excluded
from comparisons. The expected verdict is `DIFFERENT`, and the command exits
with status 1.

## Accept the intentional differences

The checked-in [exceptions example](../testdata/demo/exceptions.yaml) uses the
versioned format documented in [run.md](run.md). Apply it with:

```sh
varco run --plan plan.json --old 127.0.0.1:18081 --new 127.0.0.1:18082 --exceptions testdata/demo/exceptions.yaml
```

The same differences remain visible and are marked accepted; the captured
summary becomes:

```text
VERDICT             EQUIVALENT
COVERAGE            planned=18 executed=18 equivalent=18 different=0 inconclusive=0 skipped=0
BODY POLICY         2xx
REPEAT              1
EXCLUDED            0
UNSUPPORTED         0
EXPIRED EXCEPTIONS  -
IGNORED FIELDS      Date, Server, Via, X-Request-Id, Set-Cookie values
CASES 7
```

The accepted rows follow that summary in the real output, each naming
`demo-case-sensitive`, `demo-trailing-slash-status`, or
`demo-trailing-slash-location`. The verdict is now `EQUIVALENT` and the exit
status is 0.

## Stop

Press `Ctrl+C` in the terminal running `make demo`. Both listeners shut down
gracefully. You can remove the generated `plan.json` afterward.
