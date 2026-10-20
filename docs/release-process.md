# Release process

No release is created by the release-check target. Publishing starts only
after a maintainer pushes a matching `vMAJOR.MINOR.PATCH` tag.

## Required setup

1. (After v0.1.0) Create `HarnageaGabriel/homebrew-tap`; the release workflow does not publish to it yet.
2. Add a `HOMEBREW_TAP_TOKEN` secret to `HarnageaGabriel/varco` with write
   access to that tap. If the secret is absent or empty, the release still
   completes as a draft.
3. Keep every action in `.github/workflows/release.yml` pinned to a full
   commit SHA with a version comment.

The workflow uses GitHub's automatically provided `GITHUB_TOKEN` for the draft
release, checksum signature, and build attestation. No signing private key is
stored in the repository.

## Checklist

1. Run `go test -race ./...`, the repository lint command, and
   `make release-check`.
2. Move the intended entries from `CHANGELOG.md` under `[Unreleased]` to
   `[0.1.0] - YYYY-MM-DD`, and commit the release preparation.
3. Push the annotated tag `v0.1.0`. Do not move or delete a published tag.
4. Review the workflow's draft release, archives, checksums,
   `checksums.txt.sigstore.json`, SBOMs and attestations.
5. After the draft assets exist, replace every placeholder hash value in
   `.krew.yaml` with the corresponding hash from `checksums.txt`, then commit
   and publish the Krew index manifest. Done for v0.1.0: every hash is filled
   in; the Krew index submission is still pending. The script in step 7 does
   the same replacement once the release is published.
6. Publish the draft only after verification and maintainer review.
7. Right after publishing, update the version strings and the Krew hashes:

   ```sh
   sh scripts/update-release-docs.sh X.Y.Z
   ```

   The version is `MAJOR.MINOR.PATCH` without the leading `v`. The script
   reads the current version from `.krew.yaml`, takes the hash of every
   archive from the `checksums.txt` of the release - downloaded with
   `gh release download vX.Y.Z --pattern checksums.txt`, or passed as the
   second argument - and rewrites `.krew.yaml`, `README.md`,
   `docs/install.md`, `docs/ci.md` and
   `examples/github-actions/varco-compare.yml`. It fails when an archive is
   missing from the checksums, refuses to run when the files it would change
   have uncommitted changes (unless `--force` is given) and changes nothing
   on a second run. It touches no other file: `CHANGELOG.md`, the ADRs and
   the release notes stay as they are. Review the diff and commit it.
   `sh scripts/test-update-release-docs.sh` runs the script against the
   fixtures in `scripts/testdata/release-docs/`.

## Verify a release

Follow [install.md](install.md) to verify the Sigstore checksum signature,
archive SHA-256, and GitHub artifact attestation before extracting a binary.
Confirm the reported version matches the tag:

```sh
varco version
gh release view v0.1.0 --repo HarnageaGabriel/varco
gh attestation verify varco_0.1.0_linux_amd64.tar.gz --repo HarnageaGabriel/varco
```

## Yank a bad release

1. Do not delete or force-move the tag; preserve the evidence and signed
   artifacts.
2. Edit the GitHub release, mark it as a pre-release, and add a prominent
   yanked notice explaining the failure and safe replacement version.
3. Remove or disable the bad Homebrew formula and Krew index entry so new
   installs cannot select it.
4. Revert or fix the defect on `main`, add a changelog entry, and release a new
   higher version. Never reuse the bad version number.
