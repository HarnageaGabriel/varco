# Examples

Ready-to-copy examples that use varco outside this repository. Nothing in
this directory is part of the varco build, test suite or release.

| Example | What it does |
|---|---|
| [github-actions/varco-compare.yml](github-actions/varco-compare.yml) | Runs varco `v0.1.0` in a GitHub Actions migration pipeline: verifies the release checksum and build attestation, plans the probes from the Ingress manifests in the repository, compares the two endpoints, publishes the report and gates the job on the verdict. |

## Using an example

1. Copy the file into `.github/workflows/` of the repository that holds the
   Ingress manifests.
2. Point the `MANIFESTS` and `EXCEPTIONS` paths in the `env:` block at your
   files.
3. Keep the exceptions file in the repository and review every change to it
   in a pull request.
4. Run the workflow from the Actions tab and give it the two endpoint
   addresses (`old_ip` and `new_ip`).

The walk-through, the exit-code policy for gating, the exceptions file and
the optional cluster access are in [docs/ci.md](../docs/ci.md).
