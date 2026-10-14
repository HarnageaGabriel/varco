# Usage

## varco diff

Sends the same request to two numeric IP endpoints and reports differences. `--old` and `--new` accept `IP`, `IPv4:port` or `[IPv6]:port`; hostnames are rejected (`VAR-CFG-0001`). `--host` sets the Host header. `--sni` accepts a hostname or IP address without a port and defaults to the hostname/IP portion of `--host`. Redirects are never followed. Exit codes: see [exit-codes.md](exit-codes.md).

Flags: `--old --new --host --sni --scheme (http|https) --method --header NAME=VALUE --timeout --rate --ignore-header --format (table|json) --allow-unsafe-methods`. Positional arguments are paths (default `/`). Request headers `Authorization`, `Cookie` and `Proxy-Authorization` are refused.

### Request and endpoint rules

- Paths are sent to the wire exactly as given (no re-escaping, no normalisation): `/a%2Fb`, `/a/../b`, `//x` and `/q?x=1` arrive byte-identical. Paths are de-duplicated and probed in sorted order. A path with a space or control byte (including CR/LF) is refused (exit 2).
- `--host` must be non-empty and contain no whitespace, control bytes, `/ ? # @` or backslash.
- `--header NAME=VALUE`: names must be valid tokens and values free of control bytes (exit 2). `Host` (use `--host`), `Content-Length`, `Transfer-Encoding`, `Trailer` and `Connection` are refused. Refused credential headers: `Authorization`, `Cookie`, `Proxy-Authorization`, `X-Api-Key`, `X-Auth-Token`, `Private-Token`, `Api-Key`. Known gap: other custom credential headers are not detected.
- `--rate` must be between 0.001 and 10000 requests per second (exit 2 otherwise).
- An unbracketed IPv6 address is address-only: `2001:db8::1:8443` is the address `2001:db8::1:8443` on the default port. To give a port, bracket it: `[2001:db8::1]:8443`.
- Probes never use proxies, never decompress (the body hash covers the bytes on the wire and `Content-Encoding` is compared), accept TLS 1.0 and newer, and open a new connection per probe by design. Bodies are hashed up to 256 MiB by default, then reported truncated. Response status lines and headers are limited to 64 KiB; up to eight informational responses are skipped (except `101 Switching Protocols`).
- Response trailers are compared as headers named `Trailer:<Name>`.
- Multi-valued response headers are compared value by value, in order; an absent header differs from an empty one (`(absent)` in output).
- If a probe fails for some path, the remaining paths are still compared, failures are listed on stderr as `VAR-NET-0001`, and the exit code is 3 (it wins over 1).
- `Location` values are printed verbatim, including query strings: they can contain tokens, so treat reports as sensitive. Query redaction may come later as an opt-in flag.
- `-h`/`--help` prints flag help and exits 0.

### Example 1: one route, table output

```sh
varco diff --old 10.0.0.1 --new 10.0.0.2 --host api.example.com /
```

### Example 2: several paths, JSON, custom ports

```sh
varco diff --old 10.0.0.1:8443 --new [2001:db8::2]:8443 --host api.example.com --format json /health /v1/items
```

### Example 3: plain HTTP, ignore a noisy header, separate SNI

```sh
varco diff --scheme http --old 10.0.0.1:8080 --new 10.0.0.2:8080 --host api.example.com --ignore-header X-Frame-Options /
varco diff --old 10.0.0.1 --new 10.0.0.2 --host api.example.com --sni edge.example.com /
```

Ignored by default: Date, Server, Via, X-Request-Id, Set-Cookie values, and Content-Length when chunked. VERIFY: these are simply not part of the compared field set.
