// Package e2e holds the in-process end-to-end suite for varco run.
//
// The scenarios drive cli.Run - the same entry point cmd/varco/main.go wires -
// against two httptest servers that stand in for the old and the new ingress.
// Endpoints are passed as numeric 127.0.0.1:<random-port> addresses (the
// endpoint syntax of docs/adr/0004-endpoint-syntax.md) so no DNS or Docker is
// involved. Verdicts and exit codes follow docs/adr/0007-verdict-model.md and
// docs/exit-codes.md. Plans are handwritten JSON under ../../testdata/e2e in
// the format produced by varco plan --format json, so changes to the plan
// generator cannot break the suite.
package e2e
