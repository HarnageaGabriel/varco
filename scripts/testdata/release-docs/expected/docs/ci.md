# CI: varco in a migration pipeline (fixture)

The example workflow runs the published `v1.2.3` release in a GitHub Actions
job and gates the job on the verdict.

| Step | What it does |
|---|---|
| Download | `gh release download v1.2.3` fetches `varco_1.2.3_linux_amd64.tar.gz` and `checksums.txt` from the release. |
