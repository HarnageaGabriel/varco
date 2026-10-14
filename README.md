# varco

Prove that the new ingress answers like the old one, route by route, before the DNS cutover.

varco derives test cases from your Ingress resources, sends the same request (same `Host`, same SNI) to two endpoints, and compares status, redirects, security and CORS headers, TLS version and certificate, and body hash. It does not translate manifests (use [ingress2gateway](https://github.com/kubernetes-sigs/ingress2gateway)); it verifies behavior.

> Status: pre-alpha, design phase. Nothing below works yet.

## Try it now

```sh
varco plan -f ./manifests --format table   # derive cases from Ingress manifests
varco diff --old 10.0.0.1 --new 10.0.0.2 --host api.example.com /
```

`varco plan` turns Ingress manifests into test cases and lists what it cannot test ([docs/plan.md](docs/plan.md)); `varco diff` compares one route on two numeric IP endpoints, without cluster access. Exit code 0 means identical, 1 differences, 2 usage error, 3 network error. See [docs/usage.md](docs/usage.md).

## Principles

Single Go binary, Apache-2.0, read-only, deterministic, no telemetry. Safe methods by default, rate limited, no credentials. Any two endpoints, not tied to ingress-nginx.

See [CONVENTIONS.md](CONVENTIONS.md) and `docs/`.

