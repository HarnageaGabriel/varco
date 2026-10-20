# Install and verify varco

`v0.1.0` is published as a pre-alpha pre-release on the
[release page](https://github.com/HarnageaGabriel/varco/releases/tag/v0.1.0).
The commands below download and verify the signed artifacts that the release
workflow published.

## Release archive

Choose the archive for your operating system and architecture on the
[release page](https://github.com/HarnageaGabriel/varco/releases/tag/v0.1.0).
The example uses `v0.1.0` on Linux amd64:

```sh
set -euo pipefail

version=v0.1.0
archive=varco_0.1.0_linux_amd64.tar.gz
base=https://github.com/HarnageaGabriel/varco/releases/download/$version
curl -fLO "$base/$archive"
curl -fLO "$base/checksums.txt"
curl -fLO "$base/checksums.txt.sigstore.json"

cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github\.com/HarnageaGabriel/varco/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt

checksum_line=$(awk -v file="$archive" '
  ($2 == file || $2 == "*" file) { count++; line = $1 "  " file }
  END { if (count == 1) print line; else exit 1 }
' checksums.txt)
printf '%s\n' "$checksum_line" | sha256sum -c -
gh attestation verify "$archive" --repo HarnageaGabriel/varco

tar -xzf "$archive"
./varco version
```

On macOS, replace `sha256sum` with `shasum -a 256`. On Windows PowerShell,
verify `checksums.txt` and the archive with `Get-FileHash -Algorithm SHA256`
before using `Expand-Archive`.

`gh attestation verify` requires `gh auth login` or `GH_TOKEN`. Each archive
also has an SBOM (Syft, SPDX/JSON) beside it as `<archive>.sbom.json`.

## Go

```sh
go install github.com/HarnageaGabriel/varco/cmd/varco@latest
varco version
```

## Homebrew (planned)

Homebrew support is planned after `v0.1.0` (GoReleaser deprecated its `brews` section, so the tap format is being decided). After the release and
`HarnageaGabriel/homebrew-tap` are published:

```sh
brew tap HarnageaGabriel/tap
brew install HarnageaGabriel/tap/varco
```

## Krew (planned)

Krew support is prepared, not published: `.krew.yaml` in the repository has
the final `v0.1.0` archive hashes but is not submitted to a Krew index yet.
Once it is available in a configured Krew index:

```sh
kubectl krew update
kubectl krew install varco
kubectl varco version
```

Krew resolves the plugin name from `metadata.name: varco`. The manifest's
`bin` must point to the archive's `varco` executable (`varco.exe` on Windows);
GoReleaser ships that binary, after which Krew exposes it as `kubectl varco`.

## Rebuild a release and compare hashes

Every release from the next one on (`v0.1.2`) can be rebuilt bit for bit: the
release binaries carry the commit metadata, not the build time. `v0.1.0` and
`v0.1.1` stamped the build time into the binary, so they cannot be reproduced
bit for bit; a rebuild of those two differs in that field and in the Go build
ID that follows from it.

To rebuild a release and compare the hashes:

```sh
set -euo pipefail

version=v0.1.2
tag=v0.1.2
archive=varco_0.1.2_linux_amd64.tar.gz
base=https://github.com/HarnageaGabriel/varco/releases/download/$version
mkdir -p /tmp/varco-release
curl -fLo "/tmp/varco-release/$archive" "$base/$archive"

git clone https://github.com/HarnageaGabriel/varco.git
cd varco
git checkout "$tag"

sh scripts/verify-reproducible.sh "/tmp/varco-release/$archive"
```

The script prints the SHA-256 of the released binary and of the rebuild and
reports whether they are identical. It needs `git`, the Go toolchain of the
release (the Go version of `go.mod`, Go 1.26 or newer) and a SHA-256 tool: a
different toolchain produces a different binary. Use the same archive name
(`varco_VERSION_os_arch.tar.gz`, `.zip` on Windows) as the one you
downloaded. The repository keeps the line endings of its files at LF
(`.gitattributes`), so a clone has the same bytes as the release; a checkout
with CRLF line endings produces a different binary.

## Build from source

Requires Go 1.26 or newer:

```sh
git clone https://github.com/HarnageaGabriel/varco.git
cd varco
make build
./bin/varco version
```
