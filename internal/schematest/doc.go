// Package schematest proves that the published JSON Schemas describe the
// outputs of varco.
//
// The package holds no production code: it carries a small JSON Schema
// validator and the table-driven tests that check every golden plan and run
// output against the schema published for it. The schemas themselves live in
// docs/schema.
package schematest
