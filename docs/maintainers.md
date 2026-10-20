# Maintainers

HarnageaGabriel maintains varco.

## Decisions

The maintainer makes project decisions after contributor discussion.
Significant architecture, protocol, safety, and process decisions are recorded
as ADRs in [docs/adr](adr/).

## Review protocol

Every change needs green CI and adversarial review. Maintainers read every
change affecting request sending, TLS handling, or redaction, and reject
semantic claims without a source. The full rules are in
[CONVENTIONS.md](../CONVENTIONS.md).

## Response targets

Security reports target acknowledgement within 3 business days, initial triage
within 7 calendar days, and a remediation plan within 14 calendar days, as
defined in [SECURITY.md](../SECURITY.md).

## Releases

Releases follow [docs/release-process.md](release-process.md). The binaries of a
release are reproducible; the check is `make verify-reproducible`, described in
[Verify reproducibility](release-process.md#verify-reproducibility).

## Branch protection

- Pull requests are squash-merged only and keep a linear history.
- Required checks are `Test (ubuntu-latest)`, `Test (windows-latest)`,
  `Lint`, and `Supply-chain checks`. CodeQL is enforced separately by a
  code scanning rule that blocks merges on high-severity alerts.
- The main branch ruleset blocks deletion and requires pull requests and
  an up-to-date branch.
- Force pushes are not allowed.
- Signed release tags are immutable through the repository ruleset once
  published.
