# ADR 0010: Input limits and plan validation

## Status

Accepted (2026-10-20)

## Context

Varco reads three kinds of input, and none of them is trusted by
construction:

- the Ingress manifests, from files, a directory, standard input, or the JSON
  that `kubectl get` prints;
- the plan document, from `--plan FILE` or `--plan -`;
- the exceptions file, from `--exceptions FILE`.

A manifest comes from a cluster export, a plan can be produced by another tool
or edited by hand, and an exceptions file is written by an operator. All three
end up in strings that varco prints, and two of them decide what is sent to
a server.

Three separate problems follow.

**Unbounded reads.** The manifests were capped at 8 MiB, but `--plan` and
`--exceptions` were read with no limit at all. An endless or enormous input
was read into memory before anything rejected it.

**Parser bombs.** A YAML document is not a file of records: it is a tree.
A document nested far deeper than a Kubernetes object, or one with a
hundred thousand nodes, can exhaust the stack or the CPU of the decoder
before a single field is looked at.

**Untrusted text in the output.** The fields of a plan document reach the
terminal: the dry-run prints the request lines, and the report table prints
the case identity, the host, the path and the free text of the findings and
warnings. A control byte in any of them can rewrite a line of the operator's
terminal, and an error message that quotes the offending value would carry
the same bytes into the diagnostic.

The report file had the same shape of problem: it was created with
`os.Create`, so the mode was 0666 before the umask - a report that carries
redacted-but-still-sensitive values was readable by every user on the
machine.

## Decision

**One cap for every input.** `ingress.MaxInputBytes` is 8 MiB
(`8 << 20`, [`internal/ingress/ingress.go`](../../internal/ingress/ingress.go)),
and it is the cap for the manifests, for `--plan` and for `--exceptions`.
Each of them is read through `io.LimitReader(reader, MaxInputBytes+1)` and
an input that exceeds the cap is refused instead of read
([`internal/cli/plan.go`](../../internal/cli/plan.go), `parseInput`;
[`internal/cli/run.go`](../../internal/cli/run.go), `readCapped`). An input of
exactly the cap is still accepted. The output of one `kubectl get` is
bounded separately, at 16 MiB for stdout and 2 KiB for stderr
([`internal/kube/kube.go`](../../internal/kube/kube.go)).

**YAML documents are bounded before they are decoded.** Every document is
walked with an explicit stack before the typed decode, and a document that
nests deeper than 64 levels or holds more than 100000 nodes is refused
([`internal/ingress/ingress.go`](../../internal/ingress/ingress.go),
`validateDocument`). The walk uses an explicit stack on purpose: a validation
step that recursed would be the same stack overflow the limit exists to
prevent.

**Every plan case is validated when the plan is loaded**, before a request
is sent ([`internal/cli/run.go`](../../internal/cli/run.go), `loadRunPlan`):

- the host with `probe.ValidHost`, the path with `probe.ValidPath`;
- the id, the family and the note with the control-byte check, so no field
  of the plan can rewrite a line of the terminal;
- the headers with the probe validators, with one exception: exactly one
  `Host` header is allowed, with one value that is a valid host, because the
  `host-variant` family needs it ([ADR 0009](0009-planned-host-override.md)).

**Every finding and every warning of a plan document is validated the same
way** (same file, `planFindingProblem` and `planWarningProblem`): the code,
the Ingress, the annotation, the path and the message must carry no control
byte. A plan varco did not write can therefore not put bytes into the report
through its free-text fields either.

**The error names the index and the field, never the value.** A rejected
plan reports "plan case at index 3 has an invalid host", "plan finding at
index 0 has an invalid message" or "plan warning at index 1 has an invalid
code". The reason is one of a fixed set of field names - `host`, `path`,
`id`, `family`, `note`, `header name`, `Host header`, `header value`,
`code`, `ingress`, `annotation`, `message` - and the value is never quoted,
so an untrusted plan cannot smuggle bytes into the terminal through the
error message.

**The diagnostics are the existing ones.** An oversized or unparsable
manifest is `VAR-PLAN-0003`; an oversized plan file is `VAR-PLAN-0003` as
well; an oversized or invalid exceptions file is `VAR-CFG-0002`; and a plan
whose cases or texts break the rules is `VAR-CFG-0001`, the same code as an
invalid flag. Every one of them is a usage error, so the exit code is 2.

**The report file is created with mode 0600**, like the file written by
`--suggest-exceptions` ([`internal/cli/run.go`](../../internal/cli/run.go),
`writeRunReport` and `writeSuggestions`).

The planner is the first line of defence and the plan loader is the second:
the planner validates every case it emits and drops an invalid one with a
`VAR-PLAN-0003` finding
([`internal/plan/generate.go`](../../internal/plan/generate.go), `appendCase`),
so the loader's checks only ever apply to a plan document that varco did
not produce.

## Alternatives considered

- **Trust the plan file.** Rejected: a plan is an input, and a plan that
  reaches the terminal unescaped is a terminal-injection channel. The
  request path already refuses an invalid host, path or header; the report
  path must refuse them too, and the same validator can do both.
- **Sanitize only at render time.** Rejected: the dry-run prints the request
  lines before any report is rendered, and the render-time escaping
  ([ADR 0011](0011-terminal-output-safety.md)) makes a value printable, not
  valid. Validating at load fails once, before any request is sent, and
  names the field that is wrong.
- **A separate cap per input.** Rejected: one number is easier to state, to
  test and to document than three, and the manifests' 8 MiB cap was already
  the reference the other two paths lacked.
- **Bounded reading without a YAML limit.** Rejected: a small document can
  still be a deep or wide tree, so the byte cap alone does not bound the
  work the decoder does.

## Consequences

- Every input varco reads is bounded, so no input can exhaust memory or the
  decoder's stack. An input over the cap is refused with a diagnostic that
  names the limit.
- A plan that is not a valid plan is refused at load, with the index and the
  field named. The offending case is not silently skipped: the run does not
  start with a smaller plan than the operator asked for.
- A plan written by another tool has to satisfy varco's validators. A case
  whose host or path is not valid, or whose text carries a control byte, is
  refused rather than probed.
- The report file is readable by its owner only.
- An operator with a manifest larger than 8 MiB has to split it; the same
  limit applies to a plan and to an exceptions file.

## Known limits

- Two fields of a plan case are not validated when the plan is loaded: the
  `sni` and the `method`. The SNI is checked on the wire, so a case with an
  invalid SNI is never sent
  ([ADR 0009](0009-planned-host-override.md)), and a method outside `GET`,
  `HEAD` and `OPTIONS` is refused with `VAR-RUN-0001`
  ([`internal/cli/run.go`](../../internal/cli/run.go), `runRun`). Both are
  printed before the run starts - the SNI in the dry-run request line, the
  method in the `VAR-RUN-0001` message - and neither of those two outputs is
  escaped ([ADR 0011](0011-terminal-output-safety.md)). A plan file that
  varco did not write is therefore still a source of terminal bytes on those
  two paths.

## Sources

- PR #43, `fix(cli): cap plan and exceptions reads, validate plan cases,
  restrict report file mode` - the caps for `--plan` and `--exceptions`, the
  validation of the plan cases and the mode of the report file.
- PR #45, `fix(cli): report close errors, handle unregistered diagnostics,
  validate plan finding text` - the validation of the free text of the
  findings and the warnings.
- `feat: verdict model, YAML limits, coverage floors (M2.5)` - the depth and
  node limits of a YAML document.
