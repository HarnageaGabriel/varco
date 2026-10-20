# Install and verify varco (fixture)

`v1.2.3` is published as a pre-alpha pre-release on the
[release page](https://github.com/HarnageaGabriel/varco/releases/tag/v1.2.3).
The example uses `v1.2.3` on Linux amd64:

```sh
version=v1.2.3
archive=varco_1.2.3_linux_amd64.tar.gz
base=https://github.com/HarnageaGabriel/varco/releases/download/$version
```

Krew support is prepared, not published: `.krew.yaml` in the repository has
the final `v1.2.3` archive hashes but is not submitted to a Krew index yet.
