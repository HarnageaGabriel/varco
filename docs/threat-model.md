# Threat model

This page is the threat model of `varco`: what the tool protects, who it
trusts, what an attacker gains at each boundary and what is left over after
the controls. It is written for adopters and for security reviewers, not for
contributors to the probe code.

Every claim below names the file, the function or the ADR it comes from. Where
a control does not exist, the row says so instead of implying one does. The
companion pages are [limitations.md](limitations.md) (what a verdict does not
prove), [ADR 0003](adr/0003-probe-safety.md) (probe safety),
[ADR 0004](adr/0004-endpoint-syntax.md) (endpoint syntax),
[ADR 0006](adr/0006-redaction.md) (redaction) and
[ADR 0007](adr/0007-verdict-model.md) (verdicts). The rules the whole
repository follows are in [CONVENTIONS.md](../CONVENTIONS.md).

## 1. Assets and trust boundaries

`varco` is a single local binary. It runs with the privileges of the operator
who started it, it sends HTTP/1.1 requests to two addresses the operator named,
it may run `kubectl` once, it reads local files and it writes a report. There
is no daemon, no database, no telemetry and no external service
([CONVENTIONS.md](../CONVENTIONS.md), Safety; [ADR 0003](adr/0003-probe-safety.md)).

| Asset | Boundary | What crosses it | Trust assumption |
|---|---|---|---|
| Operator machine: the process, its memory, the terminal, the working files | the process boundary | the CLI reads the files it is given and writes the report it is asked for | fully trusted; varco does not defend it (see section 4) |
| The two endpoints under test (the old and the new controller) | the network | one HTTP/1.1 request per case, with a controlled `Host` header and a controlled SNI | untrusted: the answer may be hostile, malformed, endless or secret-bearing |
| The Kubernetes cluster | the `kubectl` child process | `kubectl get ingress` as JSON, and one `kubectl get configmap` ([internal/kube/kube.go](../internal/kube/kube.go), `FetchIngresses`, `FetchConfigMap`) | the returned content is untrusted and validated; the `kubectl` binary on `PATH` is trusted with the operator's privileges ([SECURITY.md](../SECURITY.md)) |
| Manifests and plan files | the filesystem and stdin | YAML documents and the plan JSON envelope | untrusted input: size-bounded, control-byte-checked, validated |
| Exceptions files | the filesystem | a versioned YAML policy that says which observed differences are accepted | untrusted input, strictly validated; it is the operator's policy and it can change a verdict |
| Report consumers and CI logs | stdout, stderr, `--output` files, CI step summaries and artifacts | the table report and the JSON report | the report is publishable by default: redacted and escaped, and written owner-only when it goes to `--output` |

## 2. Actors and what each is trusted to do

| Actor | Trusted to | Not trusted to |
|---|---|---|
| The operator | everything on their own machine: the binaries, the files, the cluster credential, the endpoints they name | nothing is out of reach for them; varco adds no privilege and removes none |
| A compromised or hostile backend (one of the two endpoints) | nothing | to send a well-formed, complete, honest or secret-free answer. Its bytes are data: they are compared, never obeyed and never executed |
| A malicious manifest or plan file (a repository contributor, a CI artifact) | to be read | to change what varco requests beyond the request shapes varco's own case families define, to carry control bytes, or to be unbounded in size |
| A user with a poisoned kubeconfig (or a hostile `kubectl` on `PATH`) | to the extent the operator chooses to trust their own machine | a kubeconfig that names a hostile API server receives the cluster credential; a hostile `kubectl` runs with the operator's privileges. varco only ever runs `kubectl get` and never writes to the cluster |
| A CI log reader (a colleague, a bot, a public log) | to read the published report | to learn a secret. The report must be safe to publish by default, and `--show-sensitive` output is credential-bearing material ([ADR 0006](adr/0006-redaction.md)) |

## 3. Threats, by boundary

Residual risk is what is left after the mitigation, not what the mitigation
achieves.

### 3.1 The endpoint boundary

| Threat | Impact | Mitigation | Residual risk |
|---|---|---|---|
| A request reaches a host other than the two named endpoints (a redirect to an internal address, a name that resolves elsewhere) | probes a system the operator never named, with the operator's network position | the prober is a raw socket writer with no redirect logic and no HTTP client: it dials the numeric endpoint and writes the request itself ([internal/probe/probe.go](../internal/probe/probe.go), `Do`, `buildRequest`); a redirect is compared as received ([ADR 0003](adr/0003-probe-safety.md)); the endpoint is an IP address only and is never resolved ([internal/endpoint/endpoint.go](../internal/endpoint/endpoint.go), `Parse`; [ADR 0004](adr/0004-endpoint-syntax.md)); no proxy environment is honored | the named IP may itself be a proxy or a NAT that forwards elsewhere, and the `Host` header and SNI are operator-controlled values |
| Header or request-line injection from a manifest, a plan or a flag | a smuggled second request, a changed framing, a header the prober does not own | the request path must start with `/` and carry no byte `<= 0x20` and no `0x7f`; the host rejects whitespace, control bytes and the URL-structural characters `/`, `?`, `#`, `@` and `\`; a header name must be an RFC 9110 token and a header value must carry no control byte but tab ([internal/probe/probe.go](../internal/probe/probe.go), `ValidPath`, `ValidHost`, `ValidHeaderName`, `ValidHeaderValue`); `Host`, `Content-Length`, `Transfer-Encoding`, `Trailer` and `Connection` are refused (`ForbiddenHeader`); at most one `Host` override is accepted (`hostOverride`); a planned case whose host, path or header carries a control byte is dropped with `VAR-PLAN-0003` ([internal/plan/generate.go](../internal/plan/generate.go), `appendCase`, `hasControlBytes`) | a path that is legal on the wire (`/a%2Fb`, `/a/../b`, `//x`) reaches the server unchanged, by design |
| An unsafe method reaches a live route | a state-changing request against production | only `GET`, `HEAD` and `OPTIONS` are planned; `varco run` refuses a plan that carries another method with `VAR-RUN-0001` and exit code 2 ([internal/cli/run.go](../internal/cli/run.go), `runMethods`); `varco diff` needs `--allow-unsafe-methods`; the prober refuses an unsafe method unless it is explicitly allowed ([internal/probe/probe.go](../internal/probe/probe.go), `safeMethod`, `AllowUnsafeMethod`); [ADR 0003](adr/0003-probe-safety.md) | `GET`, `HEAD` and `OPTIONS` are still real requests against a live route; a `GET` with a side effect is outside varco's model |
| A secret leaks into the report, a log or a CI artifact | a redirect token, a cookie value or a presigned signature becomes readable by anyone with the log | redaction happens at serialization, never before the comparison ([ADR 0006](adr/0006-redaction.md)); every `Location` query value, the fragment and the userinfo become `REDACTED` and `Set-Cookie` values are neither compared nor rendered ([internal/redact/url.go](../internal/redact/url.go), `URL`; [internal/report/report.go](../internal/report/report.go), `displayValue`); `kubectl` stderr is redacted before it is printed ([internal/kube/kube.go](../internal/kube/kube.go), `CommandError`); a probe-failure diagnostic carries the request path as a length only, and a parse error carries no rejected header text ([ADR 0006](adr/0006-redaction.md)) | the known limits of ADR 0006 stand: a secret in a path segment is not redacted, a secret used as a bare query key is printed, a non-`http(s)` URL inside a diagnostic message keeps its credentials, and `--show-sensitive` prints the raw values |
| A hostile backend injects terminal escapes into the report | the operator's terminal is repainted, a line is rewritten, a difference is hidden | every control character and every invalid UTF-8 byte of a reported value becomes a visible `\xNN` escape before it reaches the table writer ([internal/redact/escape.go](../internal/redact/escape.go), `EscapeControl`; [internal/report/escape.go](../internal/report/escape.go), `safeCell`); the CLI error output for a failed `kubectl` escapes the same way ([internal/cli/plan.go](../internal/cli/plan.go)); the JSON report is escaped by `encoding/json` and its schema version stays 1 ([ADR 0006](adr/0006-redaction.md), Terminal output) | printable text is not escaped, so a hostile value can still be misleading text; the escape is a rendering control, not a trust decision |
| A hostile response stalls the run: slow-loris headers, endless interim (1xx) responses, a header flood, an endless chunked body | a hung run, an exhausted worker, unbounded memory | a per-request timeout with a socket deadline (default 10 s) and a per-`kubectl` timeout (default 30 s); at most 8 interim responses and at most 64 KiB of response header ([internal/probe/probe.go](../internal/probe/probe.go), `maxInterimResponses`, `maxHeaderBytes`, `readHeaderBlock`); the body is hashed under a cap (256 MiB) and truncated rather than buffered (`capture`); the rate limit is always on and `--max-requests` caps the total number of requests ([internal/engine/run.go](../internal/engine/run.go), `rateLimiter`, `budgetEvaluation`) | the timeout is per request, so a long run is possible; the body cap is a memory ceiling, not a bandwidth ceiling; a server that answers within the timeout can still keep the run long |

### 3.2 The cluster boundary

| Threat | Impact | Mitigation | Residual risk |
|---|---|---|---|
| The `kubectl` on `PATH` is hostile or swapped | arbitrary code execution with the operator's privileges | none inside varco, by design: varco runs the binary the operating system resolves, exactly as any other tool does | **the binary on `PATH` is fully trusted.** [SECURITY.md](../SECURITY.md) says so: make sure the `kubectl` on `PATH` is the one you trust. varco passes only the `get` verb, with fixed arguments ([internal/kube/kube.go](../internal/kube/kube.go), `DefaultRunner.Run`) |
| The kubeconfig is poisoned, or points at an attacker's API server | the cluster credential is presented to a hostile server, and the plan is built from content the attacker chose | varco never writes a credential and never writes to the cluster; the kubeconfig and the context are passed through unchanged and only `get` is run; the returned JSON is decoded and the names are validated as DNS-1123 subdomains, so a name can never be read as a flag ([internal/kube/kube.go](../internal/kube/kube.go), `validName`, `dnsName`); a read-only kubeconfig is the recommended configuration ([docs/ci.md](ci.md), Cluster input is optional) | the credential is handed to whatever server the kubeconfig names; varco cannot tell a legitimate cluster from a hostile one, so the plan's fidelity to the real cluster is only as good as that trust |
| The cluster returns hostile content (a control byte in a host, a path or an annotation) | a request line or a report line the operator did not write | the same validation as for a file: control bytes in a case are dropped with `VAR-PLAN-0003` ([internal/plan/generate.go](../internal/plan/generate.go), `hasControlBytes`); the ConfigMap values that can carry a URL with credentials are redacted ([internal/plan/configmap.go](../internal/plan/configmap.go)) | the attacker controls the `Host` header and the SNI the probe sends; those are headers, not destinations - varco still dials only the named IP |

### 3.3 The file boundary

| Threat | Impact | Mitigation | Residual risk |
|---|---|---|---|
| A YAML or JSON bomb, a huge file, or an endless stdin | memory exhaustion, a hang, a crash | an 8 MiB input cap applied through `io.LimitReader` to manifests, to plan files and to exceptions files ([internal/ingress/ingress.go](../internal/ingress/ingress.go), `MaxInputBytes`; [internal/cli/run.go](../internal/cli/run.go), `readCapped`; [internal/cli/plan.go](../internal/cli/plan.go), `parseInput`); a YAML node limit of 100 000 and a nesting limit of 64 ([internal/ingress/ingress.go](../internal/ingress/ingress.go), `validateDocument`); `kubectl` output is capped at 16 MiB and its stderr at 2 KiB ([internal/kube/kube.go](../internal/kube/kube.go), `MaxOutputBytes`, `maxStderrBytes`); only regular `.yaml`, `.yml` and `.json` files are collected, so a named pipe or a device file is skipped ([internal/cli/plan.go](../internal/cli/plan.go), `walkableManifest`) | an 8 MiB input still costs memory, and a valid but adversarial plan can make many cases - each one a request, bounded by `--max-requests` and the rate limit |
| A control byte in a manifest or a plan file | terminal injection through the report | a planned case with a control byte in the host, the path, a header name or a header value is dropped with `VAR-PLAN-0003` ([internal/plan/generate.go](../internal/plan/generate.go), `appendCase`); a plan document whose case, finding or warning carries a control byte is refused, and the reason names the field without repeating its value ([internal/cli/run.go](../internal/cli/run.go), `validPlanText`, `planCaseProblem`) | a printable value stays printable; the check stops control bytes, not misleading text |
| A hostile exceptions file | a green verdict that is not true: a real difference is silently accepted | the file is versioned and every problem in it is `VAR-CFG-0002` with exit code 2: the version, the ids, the reasons, the dates and the globs are validated ([internal/policy/policy.go](../internal/policy/policy.go), `Load`); an accepted difference never leaves the report - it stays listed and marked `accepted (<id>): <reason>` ([ADR 0007](adr/0007-verdict-model.md)); expired exceptions are listed separately | a valid, reviewed exceptions file can still accept a real difference. That is what the file is for; it is the operator's policy and it belongs in the repository ([docs/ci.md](ci.md)) |

### 3.4 The supply chain

| Threat | Impact | Mitigation | Residual risk |
|---|---|---|---|
| A tampered release archive or a tampered build | a malicious binary runs with the operator's privileges | `checksums.txt` is signed with Sigstore and verified with `cosign verify-blob` against a certificate identity restricted to the release workflow; every archive has a Syft SBOM in SPDX/JSON; the archives, the checksums and the SBOMs carry a GitHub build attestation verified with `gh attestation verify`; every third-party action in the release workflow is pinned by commit SHA ([.github/workflows/release.yml](../.github/workflows/release.yml), the `release` job; [docs/install.md](install.md)) | the verification is opt-in: varco cannot verify itself, and the steps are only as good as the operator's execution of them. `go install` is a different trust path from the release archive, and it is not covered by the attestation |

### 3.5 The report boundary

| Threat | Impact | Mitigation | Residual risk |
|---|---|---|---|
| The report file is world-readable | a redacted report still holds host names, paths, certificate subjects and internal structure | `--output` creates the report with mode `0600`; `--suggest-exceptions` creates the file with `0600` and `O_EXCL`, so it never overwrites an existing file ([internal/cli/run.go](../internal/cli/run.go), `writeRunReport`, `writeSuggestions`) | the mode bits are not enforced the same way on Windows, and a report the operator redirects to stdout is under the shell's umask |
| A report is published where a secret is reachable | the redaction limits of ADR 0006 become public | the default report is redacted at serialization; `--show-sensitive` is the only way to see raw values and it warns on stderr ([internal/cli/run.go](../internal/cli/run.go), the `--show-sensitive` warning) | the ADR 0006 limits apply to the published report too: a secret in a path segment, a secret used as a bare query key and a non-`http(s)` URL in a diagnostic message stay visible |

## 4. Non-goals

These are decisions, not omissions.

- **varco does not defend a compromised operator machine.** It is a local binary
  that runs with the operator's privileges, executes the `kubectl` on `PATH`,
  reads the files the operator names and writes the report the operator asks
  for. It adds no privilege and can remove none.
- **TLS verification is intentionally off for probes**
  ([ADR 0003](adr/0003-probe-safety.md)). The probe captures the certificate;
  it does not assert trust. Certificate trust is never reported as pass or
  fail, so an expired or untrusted certificate does not fail a run. varco is
  not a TLS validator, and it does not check the TLS version range, cipher
  suites, mTLS or OCSP stapling ([limitations.md](limitations.md), section 8).
- **varco is not a scanner.** It does not enumerate routes, fuzz payloads, test
  authenticated flows, check for CVEs, or audit the controller's configuration
  beyond the 16 ConfigMap keys it names (`VAR-PLAN-0010`). It compares two
  answers to the same anonymous request and reports the difference.
- **varco does not protect the cluster.** It reads Ingress resources and one
  controller ConfigMap through `kubectl get` and nothing else.
- **varco does not protect the data behind the hash.** The body is compared as
  a SHA-256 hash, not as content.

## 5. Reporting a vulnerability

Use the private vulnerability reporting of the repository, as described in
[SECURITY.md](../SECURITY.md): do not open a public issue and do not disclose
before a coordinated release. Include the affected versions, the impact, the
reproduction steps and a proposed mitigation. The project targets an
acknowledgement within 3 business days, an initial triage within 7 calendar
days and a remediation plan within 14 calendar days.

A report is most useful with the boundary it crosses: the endpoint boundary
(3.1), the cluster boundary (3.2), the file boundary (3.3), the supply chain
(3.4) or the report boundary (3.5).

## 6. The regression tests that guard the mitigations

Each mitigation above is proved by a test in this repository. The names are
the test functions as they are written today.

| Mitigation | Test |
|---|---|
| Only the named endpoint is contacted; a redirect is not followed | `TestDoRedirectNotFollowed`, `TestDoRedirectNotFollowedHTTPS`, `TestHostileRedirectToAnotherHost` ([internal/probe/probe_test.go](../internal/probe/probe_test.go), [internal/probe/wire_test.go](../internal/probe/wire_test.go), [internal/e2e/e2e_hostile_test.go](../internal/e2e/e2e_hostile_test.go)); `TestDoIgnoresProxyEnvironment` |
| The endpoint is an IP and is never resolved | `TestParse` and `FuzzParse` ([internal/endpoint/endpoint_test.go](../internal/endpoint/endpoint_test.go)) |
| Header and request-line validation | `TestDoRejectsUnsafeInput`, `TestValidators`, `TestDoSNIValidation` ([internal/probe/wire_test.go](../internal/probe/wire_test.go), [internal/probe/probe_test.go](../internal/probe/probe_test.go)); `TestRunDiff` (the "path CRLF" case, [internal/cli/diff_test.go](../internal/cli/diff_test.go)) |
| A control byte in a planned case drops it | `TestBuildRejectsControlBytes` ([internal/plan/plan_test.go](../internal/plan/plan_test.go)) |
| Only safe methods are planned and sent | `TestDoRefusals` and `TestDoUnsafeMethodAllowed` ([internal/probe/probe_test.go](../internal/probe/probe_test.go)); `TestRunRejectsInvalidPlanCases` ([internal/cli/run_input_test.go](../internal/cli/run_input_test.go)) |
| Redaction of `Location`, cookies and kubectl stderr | `TestRunRedaction`, `TestShowSensitive`, `TestLocationDifferenceNotes` ([internal/report/run_test.go](../internal/report/run_test.go), [internal/report/report_test.go](../internal/report/report_test.go), [internal/report/redaction_test.go](../internal/report/redaction_test.go)); `TestURL`, `FuzzURL`, `TestText`, `FuzzText` ([internal/redact/url_test.go](../internal/redact/url_test.go)); `TestRunDiffRedactsLocationUnlessOptedIn` ([internal/cli/diff_test.go](../internal/cli/diff_test.go)); `TestDefaultRunnerCommandErrorRedactsQuotedURL` ([internal/kube/kube_exec_test.go](../internal/kube/kube_exec_test.go)); `TestConfigMapWarningsRedactURLValues` ([internal/plan/configmap_test.go](../internal/plan/configmap_test.go)) |
| Terminal escapes in a reported value | `TestEscapeControl`, `TestEscapeControlRemovesEveryControlCharacter`, `TestEscapeControlIsDeterministic` ([internal/redact/escape_test.go](../internal/redact/escape_test.go)); `TestSafeCell`, `TestTableEscapesControlCharacters`, `TestSafeCellProperties` ([internal/report/escape_test.go](../internal/report/escape_test.go), [internal/report/fuzz_render_test.go](../internal/report/fuzz_render_test.go)); `TestRunTableEscapesHostileHeader`, `TestRunTableEscapesTabInHeader`, `TestRunTableEscapesHostileCertificate` ([internal/e2e/e2e_escape_test.go](../internal/e2e/e2e_escape_test.go)); `FuzzRunTable`, `FuzzDiffTable`, `FuzzRunJSON` |
| Hostile responses: slow-loris, interim responses, header flood, chunk flood | `TestHostileSlowLorisHeaders`, `TestHostileSilentServer`, `TestHostileInterimResponses`, `TestHostileHeaderFlood`, `TestHostileEndlessChunkedBody`, `TestHostileConnectionCloseMidBody`, `TestHostileMalformedResponse`, `TestHostileSmugglingShapedResponse`, `TestHostileTLS` ([internal/e2e/e2e_hostile_test.go](../internal/e2e/e2e_hostile_test.go)); `TestDoLimitsInterimResponses`, `TestDoRejectsOversizeHeader`, `TestDoBodyTruncation`, `TestDoHashesBeyondCap`, `TestDoTimeout` ([internal/probe/wire_test.go](../internal/probe/wire_test.go), [internal/probe/probe_test.go](../internal/probe/probe_test.go)) |
| Bounded inputs: size, node, depth and stdout caps | `TestParseLimits`, `TestParseSizeLimitEdges`, `TestParseDepthLimitEdges`, `TestParseNodeLimitEdges`, `FuzzParse` ([internal/ingress/ingress_test.go](../internal/ingress/ingress_test.go), [internal/ingress/edge_manifest_test.go](../internal/ingress/edge_manifest_test.go), [internal/ingress/fuzz_test.go](../internal/ingress/fuzz_test.go)); `TestPlanRejectsOversizedFile`, `TestPlanBoundsEndlessStdin` ([internal/cli/plan_test.go](../internal/cli/plan_test.go)); `TestRunRejectsOversizedPlanInput`, `TestRunRejectsOversizedPlanFile`, `TestRunRejectsOversizedExceptionsFile` ([internal/cli/run_input_test.go](../internal/cli/run_input_test.go)); `TestOutputCap` ([internal/kube/kube_test.go](../internal/kube/kube_test.go)); `TestPlanInputSkipsNamedPipe`, `TestWalkableManifestRejectsDeviceFiles`, `TestPlanInputSkipsSymlinks` ([internal/cli/edge_manifest_unix_test.go](../internal/cli/edge_manifest_unix_test.go), [internal/cli/edge_manifest_test.go](../internal/cli/edge_manifest_test.go)) |
| A control byte in a plan document is refused | `TestRunRejectsInvalidPlanCases`, `TestRunRejectsInvalidPlanFindingsAndWarnings` ([internal/cli/run_input_test.go](../internal/cli/run_input_test.go)) |
| Only `kubectl get` runs; a name is never read as a flag | `TestDefaultRunnerRejectsNonGet`, `TestDefaultRunnerRejectsOtherVerbs`, `TestFetchRejectsFlagLikeNames` ([internal/kube/kube_test.go](../internal/kube/kube_test.go), [internal/kube/kube_exec_test.go](../internal/kube/kube_exec_test.go)); `TestPlanFromClusterRejectsInvalidNamespace` ([internal/cli/plan_edges_test.go](../internal/cli/plan_edges_test.go)) |
| The exceptions file is validated | `TestLoadRejects`, `TestPropertyLoadRejectsBadDocuments`, `TestPropertyLoadAcceptsTheDocumentedLimits`, `FuzzLoad` ([internal/policy/policy_test.go](../internal/policy/policy_test.go), [internal/policy/properties_test.go](../internal/policy/properties_test.go), [internal/policy/fuzz_test.go](../internal/policy/fuzz_test.go)) |
| The report file is owner-only | `TestRunReportFileMode` ([internal/cli/run_input_test.go](../internal/cli/run_input_test.go)) |
| The rate limit and the request budget are always on | `TestRunRateLimiterCountsBothEndpoints` ([internal/engine/run_test.go](../internal/engine/run_test.go)); `TestRunCommandTrustFlags` ([internal/cli/run_flags_test.go](../internal/cli/run_flags_test.go)) |
| The release artifacts are signed, attested and SBOM-carrying | the release workflow itself ([.github/workflows/release.yml](../.github/workflows/release.yml)); the verification steps are documented in [docs/install.md](install.md) and exercised in [docs/ci.md](ci.md) |
