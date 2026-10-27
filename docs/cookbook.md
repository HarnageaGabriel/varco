# Cutover rehearsal cookbook

A rehearsal is a sequence, and this cookbook is that sequence for a cutover
from ingress-nginx to a new controller (Envoy Gateway, for example):

1. run the two controllers side by side, each on its own IP;
2. derive the cases from your Ingress objects;
3. read the plan and the work varco could not do;
4. look at the requests before anything is probed;
5. run, read the table, and decide case by case;
6. repeat until the verdict is `EQUIVALENT`.

Throughout the examples the old controller (ingress-nginx) listens on
`192.0.2.10` and the new controller on `192.0.2.20`. `192.0.2.0/24` is the
documentation range from RFC 5737, so replace both addresses with the real
ones before you run anything.

Every command below is a real flag combination, taken from
`varco plan --help`, `varco run --help` and `varco diff --help`.

## Before you start

- Both controllers answer on their own IP, for the same `Host` and with the
  same SNI. varco dials the IP you name; it never resolves a name, never
  follows a redirect and never touches a cluster beyond `kubectl get`.
- The Ingress objects of the cutover are available as manifests, in the
  cluster, or both.
- varco is installed and verified, per [install.md](install.md).

## Step 1 - the two controllers, side by side

Keep the old controller running and deploy the new one next to it. Give the
new controller its own address (`192.0.2.20` in this cookbook) and let both
serve the same hosts. varco never shadows traffic: it sends a small number of
`GET`, `HEAD` and `OPTIONS` requests to the two addresses you name, and
nothing else.

If a controller is reachable only on a NodePort, give the port explicitly
with `--old-http`, `--old-https`, `--new-http` and `--new-https`, which accept
`IP:port`. `--old` and `--new` alone use port 80 for `http` cases and port 443
for `https` cases.

## Step 2 - one route, to check the wiring

```sh
varco diff --old 192.0.2.10 --new 192.0.2.20 --host api.example.com /
```

`diff` sends one request to each side and prints the differences. It needs no
manifests and no cluster, so it is the fastest way to learn whether the two
addresses really answer for that host. It probes `https` on port 443 by
default; add `--scheme http` for a plain HTTP endpoint. Exit status `0` means
identical answers, `1` a difference, `3` a failed probe (for example the new
controller is not listening yet) and `2` a usage error. Fix the wiring before
going on: every later step assumes both addresses answer.

## Step 3 - derive the cases

The plan is the same whichever way you read the Ingress objects.

From the manifests:

```sh
varco plan -f ./manifests --format json > plan.json
```

From the live cluster, read-only:

```sh
varco plan --from-cluster --format json > plan.json
```

`--from-cluster` reads the Ingresses of the current kubeconfig with
`kubectl get` and, under the `nginx` profile, the ingress-nginx controller
ConfigMap. Narrow it with `--namespace apps`, `--kube-context`, `--kubeconfig`
and `--kube-timeout`, and switch the ConfigMap read off with
`--controller-configmap` set to an empty value. `--from-cluster` cannot be
combined with `-f` or `--plan`.

`plan` sends nothing. It exits `0` even when the plan contains unsupported
findings, because the findings are part of the output, not an error.

You can also plan and run in one step, without an intermediate file:

```sh
varco run -f ./manifests --old 192.0.2.10 --new 192.0.2.20 --dry-run
```

## Step 4 - read the plan and the unsupported findings

```sh
varco plan -f ./manifests --format table
```

The table lists one row per case (`HOST`, `METHOD`, `SCHEME`, `PATH`,
`FAMILY`, `ID`) and then the two findings sections. A real excerpt:

```
UNSUPPORTED 2
CODE           INGRESS       TARGET                                       MESSAGE
VAR-PLAN-0001  shop/catalog  nginx.ingress.kubernetes.io/auth-type        annotation nginx.ingress.kubernetes.io/auth-type cannot be tested black-box by varco
VAR-PLAN-0001  shop/catalog  nginx.ingress.kubernetes.io/proxy-body-size  annotation nginx.ingress.kubernetes.io/proxy-body-size cannot be tested black-box by varco
WARNINGS 1
CODE           INGRESS       MESSAGE
VAR-PLAN-0006  shop/catalog  use-regex or rewrite-target on shop/catalog makes ingress-nginx match every path of host shop.example.com as a case-insensitive regex
```

Read the `UNSUPPORTED` section before the run. Every row is work varco could
not do from outside the controller, so that section is your manual checklist.
The families and the finding codes are documented in [plan.md](plan.md), and
every code has a page under [diagnostics](diagnostics/README.md).

## Step 5 - look at the requests before they are sent

```sh
varco run --plan plan.json --old 192.0.2.10 --new 192.0.2.20 --dry-run
```

The dry run opens no connection. It prints one line per request the run
*would* send - `side scheme endpoint method host sni path`, with `-` for an
absent SNI - and ends with `TOTAL <n>`:

```
old http 192.0.2.10:80 GET alpha.example.com - //alpha
new http 192.0.2.20:80 GET alpha.example.com - //alpha
old http 192.0.2.10:80 GET alpha.example.com - /alpha
new http 192.0.2.20:80 GET alpha.example.com - /alpha
...
TOTAL 64
```

`TOTAL` is twice the number of cases, because each case is probed on both
sides; with `--repeat 3` the same plan prints `TOTAL 192`. `--old` and `--new`
are required even for a dry run, because varco validates the endpoints before
it decides what to print. The dry run honors `--repeat`, `--family`,
`--exclude-path` and `--max-requests`, so this is the place to check that a
filter does what you meant.

## Step 6 - the first real run

```sh
varco run --plan plan.json --old 192.0.2.10 --new 192.0.2.20
```

Nothing but the two addresses is contacted. The progress lines go to stderr;
add `--quiet` to silence them. The exit status tells you where you stand:

| Exit status | Meaning |
|---:|---|
| 0 | the verdict is `EQUIVALENT` |
| 1 | the verdict is `DIFFERENT`: a difference policy did not accept |
| 2 | usage or configuration error, nothing was probed |
| 3 | the verdict is `INCONCLUSIVE`: a case could not be observed or was skipped |
| 4 | internal error |

A proven difference outranks missing data: `1` wins over `3`. The full table
is in [exit-codes.md](exit-codes.md).

## Step 7 - read the table

```
VERDICT             DIFFERENT
COVERAGE            planned=1 executed=1 equivalent=0 different=1 inconclusive=0 skipped=0
BODY POLICY         2xx
REPEAT              1
EXCLUDED            0
UNSUPPORTED         0
EXPIRED EXCEPTIONS  -
IGNORED FIELDS      Date, Server, Via, X-Request-Id, Set-Cookie values
CASES 1
ID            HOST             METHOD  PATH  FAMILY  OUTCOME    FIELD            CODE          OLD                NEW       NOTE
aaaaaaaaaaaa  app.example.com  GET     /     base    DIFFERENT  header.location  VAR-CMP-0002  /x/?code=REDACTED  (absent)  -
aaaaaaaaaaaa  app.example.com  GET     /     base    DIFFERENT  status           VAR-CMP-0001  301                200       -
```

The summary block, from the top:

- `VERDICT` is the aggregate outcome of the run: `EQUIVALENT`, `DIFFERENT` or
  `INCONCLUSIVE`.
- `COVERAGE` says what the verdict rests on. `planned` is the number of
  cases, `executed = planned - skipped`, and the other four counters split
  the executed cases. A verdict on three executed cases out of thirty is not
  a verdict on the cutover.
- `BODY POLICY` is the `--body` policy in force (`2xx`, `hash` or `none`).
- `REPEAT` is the `--repeat` value this run used.
- `EXCLUDED` counts the cases removed by `--family` or `--exclude-path`.
- `UNSUPPORTED` counts the plan findings carried into the run; the same
  findings are listed in full in the JSON report.
- `EXPIRED EXCEPTIONS` lists the ids of exceptions that are past their expiry
  date. An expired exception never accepts anything.
- `IGNORED FIELDS` lists the fields no comparison covers, so nothing is
  invisible.

Then one block per reportable case: the `ID`, `HOST`, `METHOD`, `PATH` and
`FAMILY` of the case, the `OUTCOME`, and one row per differing field with its
`CODE` (`VAR-CMP-*`), the old and the new value, and a `NOTE`. A `NOTE` of
`accepted (<id>): <reason>` marks a difference that policy accepted; the
difference is still there, it is just accounted for.

Only cases that are not `EQUIVALENT` are listed, plus a case that is
`EQUIVALENT` only because policy accepted all of its differences. An
`EQUIVALENT` run therefore prints `CASES 0`.

The same report is available as JSON, which is what a CI job should archive:

```sh
varco run --plan plan.json --old 192.0.2.10 --new 192.0.2.20 --format json --output report.json
```

### Explanations

Some `DIFFERENT` cases carry an explanation of *why* the difference is the
known ingress-nginx behavior rather than a bug. The table prints one
indented line per explanation, `why: ... fix: ... see: ...`:

```
44c0217cdb6f  shop.example.com  GET     /CART  case-flip  DIFFERENT  status           VAR-CMP-0001  301                200       -
                                                                                                                                 why: ingress-nginx matches use-regex/rewrite-target paths case-insensitively; Gateway API matches are case-sensitive.  fix: Use a RegularExpression match with (?i), or correct the path casing.  see: https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate
```

An explanation never decides for you: it names the documented behavior, the
fix and the source. The rule table is in [run.md](run.md). If a case has no
explanation, the difference is unexplained - either a real behavioral change
or something varco has not documented yet.

## Step 8 - fields that are not stable

A real cluster answers differently from one request to the next: a header
that carries a timestamp, a rotating `Set-Cookie`, a status that depends on
which replica answers. Probe each side more than once and let varco find the
unstable fields:

```sh
varco run --plan plan.json --old 192.0.2.10 --new 192.0.2.20 --repeat 3
```

`--repeat N` probes each endpoint N times per case (1 to 5, default 1). Every
repeat is a real request and counts against `--rate`. A field that takes
different values across the observations of one endpoint is unstable, and a
difference on an unstable field is accepted automatically, with the reason
`unstable: differs between repeated observations of <old|new>` and the
exception id `auto:unstable`:

```
ffffffffff10  app.example.com  GET     /     base    EQUIVALENT  header.referrer-policy  VAR-CMP-0003  v1   call-1  accepted (auto:unstable): unstable: differs between repeated observations of new
```

If the `status` itself is unstable on either side, no comparison is
meaningful and the case is `INCONCLUSIVE` with the error `unstable status`.

To turn the automatic acceptance into something you review and keep, write
the exceptions file varco proposes:

```sh
varco run --plan plan.json --old 192.0.2.10 --new 192.0.2.20 --repeat 3 --suggest-exceptions suggested-exceptions.yaml
```

The file it writes has one entry per `(host, field)` pair the run observed
unstable, and it expires 30 days after the run date:

```yaml
version: 1
exceptions:
    - id: unstable-101728ccbb1c
      reason: observed unstable across 3 observations
      expires: "2026-11-19"
      match:
        host: app.example.com
        field: header.referrer-policy
```

varco never overwrites an existing file: if the file is already there, the
run ends with `VAR-CFG-0001` and exit status 2. Delete or rename the old
file first.

## Step 9 - write the exceptions file

An exceptions file is versioned YAML. Every problem in it is `VAR-CFG-0002`
and exit status 2: an unknown key, a missing or duplicate `id`, a missing
`reason`, a bad date, an invalid glob.

```yaml
version: 1
exceptions:
  - id: hsts-policy
    reason: 'the new controller sends its own HSTS policy; the old one sends none'
    expires: 2026-12-31
    match:
      host: '*.example.com'
      path: '/'
      field: 'header:Strict-Transport-Security'
```

- `id` is required and must be unique.
- `reason` is required, and it is the sentence a reviewer reads in the report.
- `expires` is optional and is a `YYYY-MM-DD` date. After that date the
  exception stops accepting anything and its id is listed under
  `EXPIRED EXCEPTIONS`. Give every temporary exception an expiry.
- `match` is optional. Every field that is present must match; an omitted
  field matches anything. `host` and `path` are `path.Match` globs (`*` does
  not cross `/`), `family` is an exact case family, `case` is an exact case
  id, and `field` is a comparator field or one of the shorthands `status`,
  `location`, `cookie`, `set-cookie`, `tls-version`, `certificate` and
  `header:<Name>`.

If two entries match the same difference, the first non-expired match in file
order supplies the report id and reason. Put narrow exceptions before broad
host globs; expired entries are skipped and listed under `EXPIRED EXCEPTIONS`.

Then run with it:

```sh
varco run --plan plan.json --old 192.0.2.10 --new 192.0.2.20 --exceptions exceptions.yaml
```

An accepted difference is never removed from the report. It stays visible,
marked with the exception id and the reason:

```
aaaaaaaaaaaa  app.example.com  GET     /     base    EQUIVALENT  header.strict-transport-security  VAR-CMP-0003  (absent)  max-age=31536000  accepted (hsts-policy): the new controller sends its own HSTS policy; the old one sends none
```

The schema is documented in [run.md](run.md).

## Step 10 - re-run until the verdict is EQUIVALENT

The loop is the same every time: run, read the rows that are not
`EQUIVALENT`, decide per row whether the difference is expected, then either
fix the new controller or accept the difference with a written exception.

- A difference you can fix in the new controller: fix it and run again. The
  case id stays the same across runs, so the report shows the same row.
- A difference that is expected and acceptable: accept it with an exception
  that says why, and give it an expiry date.
- A difference you do not understand: it is a finding. Nothing in the report
  is hidden, and an unexplained difference is the one worth escalating.

Keep the exceptions file in version control next to the manifests. It is the
record of what was deliberately allowed to differ.

## When the verdict is INCONCLUSIVE

`INCONCLUSIVE` means varco could not observe something, not that the
controllers differ. The rows that carry the reason are the ones to read:

| Error in the report | What it means | What to do |
|---|---|---|
| `VAR-NET-0001` | a probe failed on one side (connection refused, timeout, TLS handshake) | check that the address is right and that the controller is listening; see [troubleshooting.md](troubleshooting.md) |
| `budget` | the run stopped before this case, because `--max-requests` was reached | raise `--max-requests`, or narrow the run with `--family` |
| `cancelled` | the run was interrupted | run it again |
| `unstable status` | the status code itself changed between the repeated observations of one side | fix the unstable backend (a replica set that answers differently), then run again |

A skipped case counts as `skipped` in the coverage line and never as
`executed`. Fix the reason and run again: an `INCONCLUSIVE` verdict is not a
pass.

## CI usage

`varco run` exits with the codes in [exit-codes.md](exit-codes.md), so a CI
job can branch on the result without parsing the report. `1` is a proven
difference, `3` is missing data, and `1` wins over `3`.

In a shell step:

```sh
varco run --plan plan.json --old 192.0.2.10 --new 192.0.2.20 --format json --output report.json
status=$?
case $status in
  0) echo 'EQUIVALENT: the cutover is proven on the covered cases' ;;
  1) echo 'DIFFERENT: a difference is not accepted by policy' ;;
  2) echo 'invalid usage or configuration' ;;
  3) echo 'INCONCLUSIVE: a case could not be observed' ;;
  *) echo 'internal error' ;;
esac
exit $status
```

In a GitHub Actions workflow, keep the report and let the step fail on a
difference:

```yaml
    - name: Rehearse the cutover
      id: rehearse
      continue-on-error: true
      run: varco run --plan plan.json --old 192.0.2.10 --new 192.0.2.20 --format json --output report.json

    - name: Fail on a proven difference or an inconclusive run
      if: ${{ steps.rehearse.outcome == 'failure' }}
      run: exit 1
```

`--output report.json` writes the versioned report to a file; archive it with
the build so a failure can be read later. The report schema, the coverage
fields and the exceptions handling are in [run.md](run.md).

## Command reference

Every flag this cookbook touches, as the CLI reports it:

| Command | Flags |
|---|---|
| `varco diff` | `--old`, `--new`, `--host`, `--scheme` |
| `varco plan` | `-f`, `--from-cluster`, `--namespace`, `--kube-context`, `--kubeconfig`, `--kube-timeout`, `--controller-configmap`, `--format` |
| `varco run` | `--plan`, `-f`, `--from-cluster`, `--old`, `--new`, `--old-http`, `--old-https`, `--new-http`, `--new-https`, `--exceptions`, `--suggest-exceptions`, `--format`, `--output`, `--rate`, `--repeat`, `--max-requests`, `--family`, `--exclude-path`, `--dry-run`, `--quiet` |

`varco plan --help`, `varco run --help` and `varco diff --help` print the
authoritative list, and [usage.md](usage.md) has the flag reference in one
place.
