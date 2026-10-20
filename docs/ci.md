# CI: varco in a migration pipeline

Varco compares two ingress controllers before a migration goes live. The
example workflow in
[examples/github-actions/varco-compare.yml](../examples/github-actions/varco-compare.yml)
runs the published `v0.1.0` release in a GitHub Actions job: it verifies the
release artifacts, plans the probes from the manifests in the repository,
compares the two endpoints, publishes the report and gates the job on the
verdict.

## Where to put the workflow

The file under `examples/` is a template, not a workflow of this repository.
Copy it to `.github/workflows/varco-compare.yml` in the repository that holds
the Ingress manifests, point the two paths in the `env:` block at your files
(`MANIFESTS` and `EXCEPTIONS`) and run it from the Actions tab with
`workflow_dispatch`. The workflow takes two inputs, `old_ip` and `new_ip`,
the two endpoint addresses varco probes; `IP:port` is accepted for a
non-default port. A plan that mixes http and https cases on different ports
needs the per-scheme flags (`--old-http`, `--new-http`, `--old-https`,
`--new-https`), so extend the run step for that setup. The workflows of this
repository live in `.github/workflows/` and are not affected by the example.

## What the example does

| Step | What it does |
|---|---|
| Download | `gh release download v0.1.0` fetches `varco_0.1.0_linux_amd64.tar.gz` and `checksums.txt` from the release. |
| Checksum | `sha256sum --ignore-missing --status -c checksums.txt` verifies the archive against the published checksums. |
| Attestation | `gh attestation verify` checks the build provenance of the archive. |
| Extract | The archive is unpacked into `dist/` and `varco version` prints the build. |
| Plan | `varco plan -f manifests --format json` derives the probe cases from the manifests in the repository. |
| Compare | `varco run` executes the plan against the two endpoints, once with `--format json` and once with `--format table`. |
| Summary | The table report is written to `GITHUB_STEP_SUMMARY`. |
| Artifact | The JSON report is uploaded as the `varco-report` artifact. |
| Gate | The job fails on exit codes 1, 2, 3 and 4, one message per code. |

Varco sends requests only to the two endpoints named in the inputs and nothing
else; the plan is derived from files in the repository, so the job contacts no
cluster and no other service ([run.md](run.md)).

The example runs `varco run` twice, once per output format, so the endpoints
see two passes over the same plan. Drop the table step when a single pass
matters more than the summary: the JSON report carries the same findings.

### Verifying the release before use

The job checks the release twice before it runs the binary: the sha256
checksum against `checksums.txt` from the same release, and the build
provenance with `gh attestation verify`. Both must pass, and the attestation
step runs before the archive is unpacked. `gh attestation verify` needs a
token that can read the release repository: the job uses its own
`GITHUB_TOKEN`, which works when the workflow and the release live in the same
repository. VERIFY: for a release in another repository, set `GH_TOKEN` to a
token with read access to it. [install.md](install.md) documents the same
verification outside CI.

## Exit-code policy

Varco has stable exit codes ([exit-codes.md](exit-codes.md)). The example
gates on `EQUIVALENT` only:

| Code | Meaning | Job |
|---:|---|---|
| 0 | `EQUIVALENT` | pass |
| 1 | `DIFFERENT` | fail |
| 2 | usage or configuration error | fail |
| 3 | `INCONCLUSIVE` | fail |
| 4 | internal error | fail |

The job exits with the same code as varco, so the run page shows which layer
stopped it: a failing migration is `1`, a broken workflow is `2` or `4`.

### EQUIVALENT only, or allowing INCONCLUSIVE

`INCONCLUSIVE` means at least one planned case was not observed: the probe
failed, the response status was unstable between repeats, or the case was
skipped (a cancelled run, or the request budget). It is not a smaller version
of `EQUIVALENT`, it is a blind spot - a difference in a case that was never
observed is not reported. Gating on `EQUIVALENT` only is therefore the default
for a migration pipeline: a case that cannot be observed is a finding, not a
pass.

A proven difference outranks missing data: exit code 1 wins over 3 when both
occur, so allowing `INCONCLUSIVE` never hides a difference that varco
observed. Relaxing the gate is a decision about the cases that were *not*
observed.

To allow `INCONCLUSIVE`, replace the `3)` branch of the gate step with a
warning and no `exit`:

```yaml
            3)
              echo '::warning::verdict INCONCLUSIVE: a case could not be observed or was skipped (docs/exit-codes.md)'
              ;;
```

Only do that when the unobserved cases are known and tracked, and read the
`COVERAGE` line of the summary (`inconclusive=`, `skipped=`) before trusting
a green run. The exit codes are documented in [exit-codes.md](exit-codes.md)
and the verdict model in [ADR 0007](adr/0007-verdict-model.md).

## The exceptions file stays in the repository

`varco/exceptions.yaml` is the accepted-difference policy of the pipeline. It
belongs in the repository: every change to it is then reviewed in a pull
request like every other change, and the accepted differences stay visible in
the report instead of disappearing into a runner secret.

Rules that keep the file reviewable:

- every entry needs a unique `id` and a `reason` that says why the difference
  is acceptable; a missing field, an unknown key or a bad date is
  `VAR-CFG-0002` and exits 2;
- scope each entry as narrowly as the difference allows, with `match.host`,
  `match.path`, `match.family` and `match.field`;
- set `expires` on differences that are expected to disappear with the
  migration, so a stale acceptance cannot outlive its reason;
- an accepted difference is never removed from the report: it stays listed
  and marked `accepted (<id>): <reason>`.

`varco run --suggest-exceptions FILE` writes a starting file for the fields a
run found unstable; it never overwrites an existing file, so it is safe to
run on a scratch path and paste the result into a reviewed pull request. The
format is documented in [run.md](run.md#exceptions-file).

## Cluster input is optional

The example plans from the manifests in the repository and needs no cluster
access. `--from-cluster` is the alternative: it reads the Ingresses and the
controller ConfigMap with `kubectl get` only. It needs a kubeconfig with
read-only access - permission to list Ingresses and to get the controller
ConfigMap, nothing else - and it is optional. Without it the pipeline stays
reproducible from the manifests in the repository; with it, the plan follows
the live cluster and the job needs a credential that is worth protecting.
The required permissions and the flags are documented in
[usage.md](usage.md#reading-from-a-cluster).

## Running the same comparison locally

```sh
varco plan -f manifests --format json > plan.json
varco run --plan plan.json --old 10.0.0.1 --new 10.0.0.2 \
  --exceptions varco/exceptions.yaml --format json --output report.json
```

`--dry-run` prints the requests a run would send without sending any, which
is the quick way to review a plan before it touches the endpoints.

## Notes on the workflow itself

- Every third-party action is pinned by commit SHA with the version in a
  comment; the example passes `actionlint`.
- The job asks for `contents: read` and nothing else.
- `GH_TOKEN` is the job's own `GITHUB_TOKEN`; the example reads no secret
  and writes no credential.
- varco applies its own rate limit (`--rate 5` requests per second across
  both endpoints by default) and redacts sensitive values in the report;
  `--show-sensitive` is not used.
- The report is uploaded as a build artifact and is readable without any
  access to the endpoints.
