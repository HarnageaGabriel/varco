# Usage

## varco diff

Sends the same request to two numeric IP endpoints and reports differences. `--old` and `--new` accept `IP`, `IPv4:port` or `[IPv6]:port`; hostnames are rejected (`VAR-CFG-0001`). `--host` sets the Host header, `--sni` the TLS server name (defaults to `--host`). Redirects are never followed. Exit codes: see [exit-codes.md](exit-codes.md).

Flags: `--old --new --host --sni --scheme (http|https) --method --header NAME=VALUE --timeout --rate --ignore-header --format (table|json) --allow-unsafe-methods`. Positional arguments are paths (default `/`). Request headers `Authorization`, `Cookie` and `Proxy-Authorization` are refused.

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
