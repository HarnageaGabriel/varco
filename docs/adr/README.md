# Architecture decision records

This index lists the architecture decisions in this directory. Dates are the
date each ADR was first recorded in the repository.

| ADR | Title | Status | Date | Summary |
|---|---|---|---|---|
| [0001](0001-record-architecture-decisions.md) | Record architecture decisions | accepted | 2026-10-12 | Record decisions as short documents covering context, decision, and consequences. |
| [0002](0002-diagnostic-codes.md) | Stable diagnostic codes | Accepted | 2026-10-12 | Give every diagnostic a stable `VAR-<AREA>-NNNN` code that is never reassigned. |
| [0003](0003-probe-safety.md) | Probe safety defaults | Accepted | 2026-10-12 | Keep probes read-oriented, bounded, non-redirecting, and rate-limited by default. |
| [0004](0004-endpoint-syntax.md) | Endpoint syntax | Accepted | 2026-10-12 | Accept unambiguous IPv4, IPv6, and explicit-port endpoint forms. |
| [0005](0005-plan-case-families.md) | Plan case families | Accepted | 2026-10-14 | Generate cases for routing behaviors that can differ during migration. |
| [0006](0006-redaction.md) | Redact sensitive response metadata at serialization | Accepted | 2026-10-14 | Redact credentials in redirect targets and cookie values from serialized output. |
| [0007](0007-verdict-model.md) | Separate outcomes, verdicts, and exit codes | Accepted | 2026-10-14 | Keep observed outcomes, run verdicts, and process exit codes distinct. |
| [0008](0008-planner-profiles.md) | Planner profiles for ingress-nginx and generic HTTP proxies | Accepted | 2026-10-15 | Isolate controller-specific planning behavior behind selectable profiles. |
| [0009](0009-planned-host-override.md) | Planned Host header override | Accepted | 2026-10-20 | Let host-variant cases safely control the request Host header. |
| [0010](0010-input-limits-and-plan-validation.md) | Input limits and plan validation | Accepted | 2026-10-20 | Bound and validate manifests, plans, exceptions, and generated case data. |
| [0011](0011-terminal-output-safety.md) | Terminal output safety | Accepted | 2026-10-20 | Escape unsafe remote and operator-controlled bytes before terminal output. |

To propose a new decision, follow [ADR 0001](0001-record-architecture-decisions.md):
add the next zero-padded number, explain the context, record the decision, and
describe its consequences.
