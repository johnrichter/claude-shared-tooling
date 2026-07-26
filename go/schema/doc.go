// Package schema validates documents against a JSON Schema supplied at runtime by the caller —
// never a schema baked into this package. It also groups a document's frontmatter tags by
// namespace and flags stale or self-contradictory declared dates.
//
// This package validates document content only. CLI configuration has its own contract and is
// never validated here.
package schema
