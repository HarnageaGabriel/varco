# CI: varco in a migration pipeline (fixture)

The example workflow runs the published `v0.1.0` release in a GitHub Actions
job and gates the job on the verdict.

| Step | What it does |
|---|---|
| Download | `gh release download v0.1.0` fetches `varco_0.1.0_linux_amd64.tar.gz` and `checksums.txt` from the release. |
