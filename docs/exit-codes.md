# Exit codes

Varco uses stable process exit codes so callers can distinguish comparison results from operational failures.

| Code | Meaning |
|---:|---|
| 0 | Endpoints are identical, or every difference is explicitly accepted. |
| 1 | Differences were found. |
| 2 | Command usage or configuration is invalid. |
| 3 | A network or probe operation failed. |
| 4 | An internal error occurred. |
