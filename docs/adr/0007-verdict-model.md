# ADR 0007: Separate outcomes, verdicts, and exit codes

## Status

Accepted

## Context

A comparison needs to distinguish what happened to one case, what the complete
run proves, and how a caller should react. Collapsing those concerns loses
coverage information and can let a large number of passing routes hide one
regression.

Comparison and acceptance policy are separate concerns too. Operators need to
see every observed difference even when an explicit rule permits it.

## Decision

Varco keeps three layers separate: per-case outcome, aggregate verdict, and
process exit code.

Each case has one outcome:

- `EQUIVALENT`: every compared field matches after applying policy.
- `DIFFERENT`: at least one observed difference is not accepted by policy.
- `INCONCLUSIVE`: one or both endpoints could not be observed because of a
  dial, TLS, timeout, or read error, or because the request budget skipped the
  case.

The aggregate verdict is `DIFFERENT` when any case is `DIFFERENT`; otherwise it
is `INCONCLUSIVE` when any case is `INCONCLUSIVE`; otherwise it is
`EQUIVALENT`. There is no percentage threshold: one regression on one route
outweighs any number of passing cases.

Coverage is reported beside the verdict: planned, executed, and skipped case
counts, plus the plan's unsupported-findings count. `EQUIVALENT` therefore
means equivalent on the covered cases, and every report states what was not
covered.

The comparator reports every observed difference. A separate policy evaluator
uses profiles and an exceptions file to decide which differences count.
Accepted differences remain visible in reports, marked accepted and carrying
the reason.

Exit codes are 0 for `EQUIVALENT`, 1 for `DIFFERENT`, 3 for `INCONCLUSIVE`, 2
for usage errors, and 4 for internal errors. The existing "probe failed"
meaning of 3 is a subset of `INCONCLUSIVE`. When a run has both different and
inconclusive cases, `DIFFERENT` and exit code 1 win: a proven regression is more
actionable than missing data, while the report still lists every inconclusive
case.

## Consequences

- A verdict cannot conceal either a proven regression or missing coverage.
- Policy can evolve without weakening the comparator's observations.
- Reports must retain accepted differences and incomplete cases.
- `varco diff` adopts this precedence in M3; today exit code 3 still takes
  precedence over exit code 1.
