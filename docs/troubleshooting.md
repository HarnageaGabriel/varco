# Troubleshooting

One section per failure that shows up in practice. Every section has the
same shape: the symptom you see, the cause, and the fix.

The diagnostic codes are stable, and every code varco emits has a page under
[diagnostics](diagnostics/README.md). A few failures carry no code of their
own - they are listed below with the code they are closest to, and with what
to read instead.

## Quick index

| What you see | Code | Section |
|---|---|---|
| `VAR-NET-0001: probe new: ...` | [VAR-NET-0001](diagnostics/VAR-NET-0001.md) | [Endpoint unreachable](#var-net-0001-endpoint-unreachable) |
| `VAR-CFG-0002: ...` | [VAR-CFG-0002](diagnostics/VAR-CFG-0002.md) | [Invalid exceptions file](#var-cfg-0002-invalid-exceptions-file) |
| `VAR-PLAN-0003: ...` | [VAR-PLAN-0003](diagnostics/VAR-PLAN-0003.md) | [Manifest parse error](#var-plan-0003-manifest-parse-error) |
| `kube: kubectl not found: ...` | none, exits 2 | [kubectl not found](#kubectl-not-found) |
| a case whose note is `budget` | none, exits 3 | [Budget skips](#budget-skips) |
| `EXPIRED EXCEPTIONS` lists an id | none, listed in the report | [Expired exceptions](#expired-exceptions) |
| `REDACTED` in the report | none, the difference keeps its `VAR-CMP-*` code | [Redaction surprises](#redaction-surprises) |

## VAR-NET-0001: Endpoint unreachable

**Symptom.** A case row is `INCONCLUSIVE` and the note names the side that
failed:

```
aaaaaaaaaaaa  app.example.com  GET     /     base    INCONCLUSIVE  -      -     -    -    VAR-NET-0001: probe new: probe 192.0.2.20:443: dial tcp 192.0.2.20:443: connection refused
```

The run ends with exit status 3 (unless another case was `DIFFERENT`, then
1). `varco diff` prints the same code on stderr.

**Cause.** varco could not open a connection to the endpoint it was given.
`connection refused` means nothing is listening on that address and port;
`context deadline exceeded` means the endpoint did not answer within
`--timeout` (10s by default). The last part of the message is the one your
operating system prints (`connection refused` on Linux, `connectex: ...` on
Windows). The certificate is never the cause: varco captures the TLS version
and the certificate without trusting them.

**Fix.**

- Check the address and the port. `--old` and `--new` use port 80 for `http`
  cases and port 443 for `https` cases. A controller behind a NodePort needs
  `--old-http`, `--old-https`, `--new-http` or `--new-https` with `IP:port`.
- Check that the controller is actually running and bound to that address.
  During a cutover the new controller is often deployed but not yet serving
  the hosts.
- Check the network path. varco dials the IP directly, with no DNS and no
  proxy, so it has to run from a host that can reach that address.
- Check the SNI. It comes from the plan (the rule host), so a server that
  requires a matching SNI fails the handshake when the host does not match
  the certificate.
- Raise `--timeout` for a slow endpoint.

See [VAR-NET-0001](diagnostics/VAR-NET-0001.md) for the code page and
[exit-codes.md](exit-codes.md) for what exit status 3 means for a CI job.

## VAR-CFG-0002: Invalid exceptions file

**Symptom.** The run stops before it probes anything, with exit status 2:

```
VAR-CFG-0002: exception "server-header" is missing a required reason
```

**Cause.** The exceptions file was rejected by the loader. Every problem in
the file is reported with this one code: the file is empty, it cannot be
parsed, it has an unknown key, its `version` is not 1, an exception has no
`id` or a duplicate `id`, an exception has no `reason`, `expires` is not a
`YYYY-MM-DD` date, or `match.host` / `match.path` is not a valid glob.

**Fix.** Compare the file against the schema in [run.md](run.md). The shape
is:

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

The two usual mistakes are a typo in a key name (an unknown key is an
error, not a warning) and a date in another format. The message names the
offending exception.

When matches overlap, the first non-expired matching exception in file order
supplies the report id and reason; put narrow entries first.

See [VAR-CFG-0002](diagnostics/VAR-CFG-0002.md) for the code page.

## VAR-PLAN-0003: Manifest parse error

**Symptom.** `varco plan` or `varco run -f` stops with exit status 2:

```
VAR-PLAN-0003: ./manifests/legacy.yaml: unsupported Ingress apiVersion "extensions/v1beta1"; only networking.k8s.io/v1 is supported
```

**Cause.** A manifest could not be decoded, or uses an Ingress API version
varco does not support, or exceeds the 8 MiB input cap, or carries an
annotation that is not a scalar, or yields a case with a control byte.

**Fix.**

- Export the Ingresses in the supported version:
  `kubectl get ingress -A -o yaml` writes `networking.k8s.io/v1`. The older
  `extensions/v1beta1` and `networking.k8s.io/v1beta1` are refused on
  purpose: their fields differ, so planning them with v1 rules would be a
  guess.
- A `kind: List` and multi-document YAML streams are fine, and other kinds
  (ConfigMaps, Deployments) are ignored silently.
- Split a file that is larger than 8 MiB, or plan it piecewise.
- A path or host with a control byte, a space, or no leading `/` cannot
  become a request. Rewrite the path.

See [VAR-PLAN-0003](diagnostics/VAR-PLAN-0003.md) for the code page and
[plan.md](plan.md) for the accepted manifests.

## kubectl not found

**Symptom.** `--from-cluster` fails with exit status 2 and this line on
stderr:

```
kube: kubectl not found: install kubectl or add it to PATH
```

**Cause.** varco reads a cluster by running the user's `kubectl get`. It
embeds no Kubernetes client, so `kubectl` has to exist on `PATH`. This
failure carries no `VAR-` code of its own: it is a configuration error, and
it exits 2 like the invalid-flag error
([VAR-CFG-0001](diagnostics/VAR-CFG-0001.md)).

**Fix.**

- Install kubectl and put it on `PATH`, or point at it with
  `--kubeconfig FILE` and `--kube-context CONTEXT`.
- Or skip the cluster entirely and plan from exported manifests:
  `kubectl get ingress -A -o yaml > ingress.yaml` and then
  `varco plan -f ingress.yaml`.

A different message, `kubectl get failed (exit N): ...`, means kubectl ran
and failed. Check the context (`kubectl config current-context`), the
kubeconfig path, and the permissions: the reader needs `get` and `list` on
ingresses and `get` on the controller ConfigMap. The example role is in
[usage.md](usage.md). `--kube-timeout` (30s by default) bounds each call.

## Budget skips

**Symptom.** Some cases are never probed. Their rows read `INCONCLUSIVE`
with the note `budget`, and the coverage line counts them as skipped:

```
COVERAGE            planned=1 executed=0 equivalent=0 different=0 inconclusive=1 skipped=1
aaaaaaaaaaaa  app.example.com  GET     /     base    INCONCLUSIVE  -      -     -    -    budget
```

The run ends with exit status 3.

**Cause.** `--max-requests N` caps the total number of requests a run sends.
The budget counts requests, not cases: one case costs `2 * --repeat`
requests, because both endpoints are probed. The case that would push the
run past the budget, and every case after it, is never probed at all.

**Fix.** Raise `--max-requests`, lower `--repeat`, or narrow the run with
`--family NAME` and `--exclude-path GLOB` so the budget covers what you
actually want to measure. The number of requests a run needs is `2 * cases *
--repeat`; a dry run prints it as `TOTAL`:

```sh
varco run --plan plan.json --old 192.0.2.10 --new 192.0.2.20 --dry-run
```

This failure carries no `VAR-` code: the case error is the literal
`budget`. The exit code is 3, so a CI job sees missing data, not a
difference.

## Expired exceptions

**Symptom.** The summary block names exceptions that no longer apply, and a
difference they used to accept is `DIFFERENT` again:

```
EXPIRED EXCEPTIONS  old-server-header
```

**Cause.** An exception with an `expires` date in the past stops accepting
anything. varco lists its id so the drift is visible, and the difference it
covered shows up in the report again.

**Fix.** Renew the exception with a new date and a reason that still holds,
or accept the difference in another way. Check the date format: `expires`
is a `YYYY-MM-DD` scalar, and a date in another format is a
[VAR-CFG-0002](#var-cfg-0002-invalid-exceptions-file) error rather than a
silent fallback.

This is not a diagnostic code; the expired ids are a field of the report
(`expired_exceptions` in JSON). The exception schema is in [run.md](run.md).

## Redaction surprises

**Symptom.** The report shows `REDACTED` where a value should be, and a
case still reads `DIFFERENT` even though the two values look the same:

```
aaaaaaaaaaaa  app.example.com  GET     /     base    DIFFERENT  header.location  VAR-CMP-0002  /x/?code=REDACTED  (absent)  -
```

The JSON report carries `"redacted": true`.

**Cause.** Redaction is on by default ([ADR 0006](adr/0006-redaction.md)).
Query values, URL fragments and URL userinfo are replaced with `REDACTED`
before anything is printed; the query keys and their order stay visible. The
comparison itself uses the raw values, so two redirects that differ only in a
hidden value are still a difference - the report shows you that something
differs, not what it was.

**Fix.**

- Read the row as a real difference and decide it as one. The difference
  keeps its comparator code ([VAR-CMP-0002](diagnostics/VAR-CMP-0002.md) for
  `Location`).
- To see the raw value, run once with `--show-sensitive`. It prints raw
  `Location` and error values and warns on stderr. Keep that report out of
  CI artifacts.
- Do not expect a difference on a field that is never compared:
  `Date`, `Server`, `Via`, `X-Request-Id` and the values of `Set-Cookie` are
  ignored by design and are listed in the `IGNORED FIELDS` line of every
  report. Only the attributes of `Set-Cookie` are compared.

## Everything else

- [diagnostics/README.md](diagnostics/README.md) lists every stable code with
  its severity, description and remediation.
- [exit-codes.md](exit-codes.md) explains what each exit status means.
- [cookbook.md](cookbook.md) is the cutover rehearsal, from two controllers
  side by side to a green CI run.
