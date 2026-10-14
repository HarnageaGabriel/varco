# Exit codes

Varco uses stable process exit codes so callers can distinguish comparison results from operational failures.

| Code | Meaning |
|---:|---|
| 0 | Aggregate verdict `EQUIVALENT`: endpoints are equivalent on the covered cases after policy. |
| 1 | Aggregate verdict `DIFFERENT`: at least one difference is not accepted by policy. |
| 2 | Command usage or configuration is invalid. |
| 3 | Aggregate verdict `INCONCLUSIVE`: at least one case could not be observed or was skipped, and no case was `DIFFERENT`. |
| 4 | An internal error occurred. |

The verdict model is recorded in [ADR 0007](adr/0007-verdict-model.md). Exit
code 1 takes precedence over 3 when both proven differences and inconclusive
cases occur; the report still lists the inconclusive cases. Both `varco diff`
and `varco run` use this precedence.
