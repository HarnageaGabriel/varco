# ADR 0011: Terminal output safety

## Status

Accepted (2026-10-20)

## Context

A remote server chooses the bytes of the values varco reports. A compared
header, a `Location`, a certificate subject, a cookie name, a probe error, a
finding message, an annotation value, a warning detail and the stderr of
`kubectl` are all text a server, a cluster or an operator's file can put
there.

The table is the operator's terminal. A value that carries an ANSI escape
sequence can repaint the screen and hide a difference; a carriage return can
rewrite the line before it; a NUL, a DEL or an invalid byte can truncate or
corrupt what the operator reads. A report that a human reads is exactly where
such a value does the most damage, because the reader trusts the layout.

Varco had two layers of protection and one hole. Redaction decided *what* is
hidden ([ADR 0006](0006-redaction.md)), and the request path decided what may
be sent. The table writers, though, wrote the remote text raw: first the run
table and the diff table, then the plan table and the stderr of `kubectl`.

## Decision

**One escaping function for every layer that prints remote text.**
`redact.EscapeControl`
([`internal/redact/escape.go`](../../internal/redact/escape.go)) replaces every
C0 control byte, DEL, every C1 control and every invalid UTF-8 byte with a
visible `\xNN` escape. Printable runes and valid UTF-8 are unchanged, so
ordinary output keeps its exact bytes. The function is pure, so the same
value always prints the same way, and its result carries no control
character, so escaping an escaped value changes nothing.

**The report layer escapes at one place.** `safeCell` delegates to
`redact.EscapeControl` and `writeRow` escapes every cell on the way into the
table writer ([`internal/report/escape.go`](../../internal/report/escape.go)).
That is the single place where text reaches a table, so every table is
covered: the run table
([`internal/report/run.go`](../../internal/report/run.go)), the diff table
([`internal/report/report.go`](../../internal/report/report.go)) and the plan
table ([`internal/report/plan.go`](../../internal/report/plan.go)).

**The plan table is escaped like the others.** A control byte in a finding
message, in an annotation value or in a warning detail - a ConfigMap value
read with `--from-cluster`, for instance - cannot reach the terminal.

**The stderr of kubectl is redacted, truncated and escaped** before it is
printed ([`internal/cli/plan.go`](../../internal/cli/plan.go), `kubeFailure`): it
is truncated to 2 KiB and escaped, because it is text a server controls.

**The JSON report is not rewritten.** `encoding/json` escapes control
characters on its own, so the JSON output keeps the value and the JSON
contract stays version 1
([`internal/report/run.go`](../../internal/report/run.go), `RunJSON`; the plan
JSON is written the same way,
[`internal/cli/plan.go`](../../internal/cli/plan.go), `writePlanJSON`). The
table escaping does not change a single byte of it.

**What is deliberately not escaped: bidirectional overrides and zero-width
characters.** U+202E (right-to-left override) and U+200B (zero width space)
are printable runes, not control characters, so they pass through unchanged.
They cannot move the cursor, start a line or end one, so they do not change
the framing of the report, and escaping them would change printable text and
every golden that carries it. They are still a hazard for the reader - a
bidirectional override can make a value read backwards - so this is a known
limit and not a safety claim. Both are in the fuzz corpus of the renderers
(`internal/report/testdata/fuzz/FuzzRunTable/09_bidi_override` and
`10_zero_width`), so the renderers are exercised with them.

**The dry-run request line is not escaped.** It prints the plan's own fields
- the side, the scheme, the endpoint, the method, the host, the SNI and the
path - as they are
([`internal/cli/run.go`](../../internal/cli/run.go), `runRun`). The plan loader
refuses the control bytes in the fields it validates
([ADR 0010](0010-input-limits-and-plan-validation.md)), so a control byte in
a plan file cannot reach it; the two fields the loader does not validate, the
SNI and the method, are the two that can still carry one into this line and
into the `VAR-RUN-0001` message. The tables are the output that remote data
reaches, and they are escaped.

## Consequences

- A hostile or compromised backend cannot repaint the operator's terminal,
  add a line to the report or rewrite one through the table output.
- The JSON output is unaffected: a consumer sees the value it always saw,
  with the control character as a JSON escape.
- The goldens of ordinary reports do not change: printable text is
  byte-identical, so only a value that carries a control byte differs. The
  visible effect is in the e2e goldens under `testdata/e2e/`: a tab in a
  compared header is shown as `DENY\x09INJECTED` in the table
  (`escape-tab.table.golden`), and the goldens of `escape-header.*` are the
  case where the server's answer carried the bytes and the prober refused
  the response with `VAR-NET-0001`.
- Grepping the output keeps working, with one change: a value that carried a
  line break is now a literal `\x0a` on one line, so the row no longer
  splits, and a search for the raw byte sequence no longer matches - the
  search has to use the escape form. What the operator reads is what the
  server sent, in the same field, in the same column.
- The report stays one row per line, so a value can no longer fake a second
  row of the report.
- The escaping does not remove a hostile value: it makes it visible. A
  difference that carries control bytes is still a difference, and it is
  still compared and reported.

## Relation to ADR 0006

Redaction and escaping are two different questions asked at the same place.
Redaction decides what is hidden before the value is printed - the query
values of a `Location`, the fragment, the userinfo, the cookie values.
Escaping decides how a printed value is rendered, so that a value which is
*kept* cannot move the cursor or rewrite a line. Both belong to the report
layer, because that is the only layer that serializes what a remote server
sent. ADR 0006 states this in a short "Terminal output" section; this ADR
records the decision in full, including what is left unescaped.

## Sources

- PR #56, `fix(report): escape control characters from remote data in table
  output` - the escaping in the run table and the diff table, and the
  paragraph ADR 0006 gained.
- PR #60, `fix: escape control characters in the plan table and kubectl
  stderr, unify manifest errors` - the escaping in the plan table and in the
  stderr of `kubectl`, and the move of the escape function into the
  `redact` package.
- PR #62, `test(report): fuzz the table and JSON renderers` - the fuzz corpus
  of the renderers, with the bidirectional and zero-width seeds.
