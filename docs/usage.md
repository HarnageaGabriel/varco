# Usage

## Choosing between diff, plan and run

All three read only what you pass them; nothing sends traffic without `diff` or `run`:

- **`varco diff`** sends one request (or a few paths) to two endpoints and reports the differences directly. Fastest way to check one route; no manifests needed.
- **`varco plan`** derives the probe cases `run` would execute from Ingress manifests (`-f`, or a live cluster with `--from-cluster`) and lists what it cannot test. Sends nothing, contacts nothing (outside `kubectl get` with `--from-cluster`).
- **`varco run`** executes a plan against the two endpoints and evaluates the observed differences through the policy layer with a verdict per [ADR 0007](adr/0007-verdict-model.md). Start with `--dry-run` to see the requests a run would send.

## varco plan

Reads Kubernetes Ingress manifests and prints the probe cases `varco run` will execute, plus what varco cannot test. Nothing is sent to the network and no cluster is contacted.

```sh
varco plan -f ingress.yaml --format json
```

Flags: `-f <file|directory|->` (repeatable; directories are walked recursively over `*.yaml`, `*.yml` and `*.json`, sorted by path), `--format (table|json)`, `--ingress-class NAME`, `--default-host HOST`, `--profile (nginx|generic)`. Manifests must be `networking.k8s.io/v1`; `kind: List` and multi-document streams are accepted and other kinds are ignored. Exit code 0 means a plan was produced (unsupported findings are part of the output), 2 is a usage or manifest parse error, 4 means the report could not be written. Case families, findings and the stable JSON schema are documented in [plan.md](plan.md).

`--profile` picks the planner behavior ([ADR 0008](adr/0008-planner-profiles.md)): `nginx` (default) adds the ingress-nginx annotation families; `generic` is controller-agnostic, plans only the core HTTP families, and warns that annotations are ignored (`VAR-PLAN-0009`). The top-level `profile` of the JSON output records which one was used.

### Example: plan a directory of manifests

```sh
varco plan -f ./manifests --ingress-class nginx --format table
varco plan -f - --format json < ingress.yaml
```

### Reading from a cluster

`varco plan --from-cluster` and `varco run --from-cluster` read Ingresses and the ingress-nginx controller ConfigMap from the user's current kubeconfig. They invoke only `kubectl get`; varco never writes to the cluster or sets credentials.

```sh
varco plan --from-cluster --namespace apps --format json
varco run --from-cluster --old 10.0.0.1 --new 10.0.0.2
```

Use `--kube-context CONTEXT`, `--kubeconfig FILE`, and `--kube-timeout DURATION` to select or bound kubectl access. `--controller-configmap NAMESPACE/NAME` defaults to `ingress-nginx/ingress-nginx-controller`; pass an empty value to disable ConfigMap warnings. Recognized settings surface as `VAR-PLAN-0010` warnings with sensitive values redacted; unrecognized keys are ignored. `--from-cluster` is mutually exclusive with `-f` and `--plan`.

A read-only identity needs permission to list Ingresses cluster-wide (or in `--namespace`) and get the controller ConfigMap. For example:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: varco-plan-reader
rules:
  - apiGroups: ["networking.k8s.io"]
    resources: ["ingresses"]
    verbs: ["get", "list"]
  - apiGroups: [""]
    resources: ["configmaps"]
    resourceNames: ["ingress-nginx-controller"]
    verbs: ["get"]
```

## varco run

Executes a plan against two endpoints, evaluates the differences through a
separate policy layer with an optional exceptions file, and prints a verdict per
[ADR 0007](adr/0007-verdict-model.md). The plan is either
`varco plan --format json` output or manifests planned in-process.

```sh
varco run --plan plan.json --old 10.0.0.1 --new 10.0.0.2
varco run -f ingress.yaml --old 10.0.0.1 --new 10.0.0.2 --exceptions exceptions.yaml --format json
```

Flags: `--plan FILE|-`, `-f <file|directory|->`, `--ingress-class`, `--default-host`, `--profile (nginx|generic)`, `--old`, `--new`, `--old-http`, `--old-https`, `--new-http`, `--new-https`, `--exceptions FILE`, `--format (table|json)`, `--output FILE`, `--rate`, `--concurrency`, `--timeout`, `--repeat N`, `--body (2xx|hash|none)`, `--max-requests N`, `--family NAME` (repeatable), `--exclude-path GLOB` (repeatable), `--dry-run`, `--suggest-exceptions FILE`, `--quiet`, `--show-sensitive`. For cluster input, the same flags as `varco plan` apply: `--from-cluster`, `--namespace`, `--kube-context`, `--kubeconfig`, `--kube-timeout`, `--controller-configmap`. `--plan` and `-f` are mutually exclusive, and both are mutually exclusive with `--from-cluster`. The exceptions file reference, the request budget, the dry-run format and the report schema are documented in [run.md](run.md).

### Exit codes and verdicts

`varco run` exits `0` when the aggregate verdict is `EQUIVALENT`, `1` for `DIFFERENT`, `3` for `INCONCLUSIVE` (including cases skipped by `--max-requests` or a failed probe), `2` for usage or configuration errors and `4` for internal errors. A proven difference outranks missing data: `1` wins over `3`. Three layers — per-case outcome, aggregate verdict, exit code — are defined in [ADR 0007](adr/0007-verdict-model.md); the full table is in [exit-codes.md](exit-codes.md).

## varco diff

Sends the same request to two numeric IP endpoints and reports differences. `--old` and `--new` accept `IP`, `IPv4:port` or `[IPv6]:port`; hostnames are rejected (`VAR-CFG-0001`). `--host` sets the Host header. `--sni` accepts a hostname or IP address without a port and defaults to the hostname/IP portion of `--host`. Redirects are never followed. Exit codes: see [exit-codes.md](exit-codes.md).

Flags: `--old --new --host --sni --scheme (http|https) --method --header NAME=VALUE --timeout --rate --ignore-header --format (table|json) --allow-unsafe-methods --show-sensitive`. Positional arguments are paths (default `/`). Request headers `Authorization`, `Cookie` and `Proxy-Authorization` are refused.

### Request and endpoint rules

- Paths are sent to the wire exactly as given (no re-escaping, no normalisation): `/a%2Fb`, `/a/../b`, `//x` and `/q?x=1` arrive byte-identical. Paths are de-duplicated and probed in sorted order. A path with a space or control byte (including CR/LF) is refused (exit 2).
- `--host` must be non-empty and contain no whitespace, control bytes, `/ ? # @` or backslash.
- `--header NAME=VALUE`: names must be valid tokens and values free of control bytes (exit 2). `Host` (use `--host`), `Content-Length`, `Transfer-Encoding`, `Trailer` and `Connection` are refused. Refused credential headers: `Authorization`, `Cookie`, `Proxy-Authorization`, `X-Api-Key`, `X-Auth-Token`, `Private-Token`, `Api-Key`. Known gap: other custom credential headers are not detected.
- `--rate` must be between 0.001 and 10000 requests per second (exit 2 otherwise).
- An unbracketed IPv6 address is address-only: `2001:db8::1:8443` is the address `2001:db8::1:8443` on the default port. To give a port, bracket it: `[2001:db8::1]:8443`.
- Probes never use proxies, never decompress (the body hash covers the bytes on the wire and `Content-Encoding` is compared), accept TLS 1.0 and newer, and open a new connection per probe by design. Bodies are hashed up to 256 MiB by default, then reported truncated. Response status lines and headers are limited to 64 KiB; up to eight informational responses are skipped (except `101 Switching Protocols`).
- Response trailers are compared as headers named `Trailer:<Name>`.
- Multi-valued response headers are compared value by value, in order; an absent header differs from an empty one (`(absent)` in output).
- If a probe fails for some path, the remaining paths are still compared and failures are listed on stderr as `VAR-NET-0001`. A probe failure exits 3 only when no path is different; a proven difference on another path exits 1 (a difference outranks missing data, `ADR 0007`).
- `Location` values are redacted by default: query keys and order remain visible, every query value becomes `REDACTED`, fragments become `REDACTED`, and URL userinfo becomes `REDACTED@`. The raw values are still compared, so redirects that differ only in hidden values remain differences. `--show-sensitive` prints raw Location values and emits a warning to stderr.
- JSON diff reports include top-level `"redacted": true` by default and `false` with `--show-sensitive`; `schema_version` remains 1.
- `Set-Cookie` compares cookie names and the `Secure`, `HttpOnly`, `SameSite`, `Path`, `Domain`, `Max-Age` presence, `Expires` presence and `Partitioned` attributes. Cookie values are never compared or printed. Attribute differences use `VAR-CMP-0007`.
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

Ignored by default: Date, Server, Via, X-Request-Id, Set-Cookie values (attributes are compared), and Content-Length when chunked. VERIFY: these are simply not part of the compared field set.
