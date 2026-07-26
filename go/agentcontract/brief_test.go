package agentcontract

import "testing"

// TestParseBrief_ValidFrontmatter checks that a well-formed frontmatter block parses into the fields agentcontract reads, including a nested discriminator cell.
func TestParseBrief_ValidFrontmatter(t *testing.T) {
	content := `---
name: alpha
description: "Does X"
contract:
  output_schema: out.schema.json
  discriminators:
    beta:
      relation: not-confusable
      reason: different domain
---
# Alpha

Body text.
`
	b, err := ParseBrief("alpha.md", []byte(content))
	if err != nil {
		t.Fatalf("ParseBrief: %v", err)
	}
	if b.Frontmatter.Name != "alpha" {
		t.Fatalf("expected name alpha, got %q", b.Frontmatter.Name)
	}
	if b.Frontmatter.Contract.OutputSchema != "out.schema.json" {
		t.Fatalf("expected output_schema out.schema.json, got %q", b.Frontmatter.Contract.OutputSchema)
	}
	cell, ok := b.Frontmatter.Contract.Discriminators["beta"]
	if !ok || cell.Relation != RelationNotConfusable {
		t.Fatalf("expected a not-confusable cell for beta, got %+v (present=%v)", cell, ok)
	}
}

// TestParseBrief_NoOpeningFenceFails checks that a file with no opening frontmatter fence is a ParseError, not a brief with zero-value fields.
func TestParseBrief_NoOpeningFenceFails(t *testing.T) {
	if _, err := ParseBrief("bad.md", []byte("# No frontmatter here\n")); err == nil {
		t.Fatalf("expected a missing frontmatter fence to be an error")
	}
}

// TestParseBrief_UnclosedFenceFails checks that an opened but never closed frontmatter fence is a ParseError.
func TestParseBrief_UnclosedFenceFails(t *testing.T) {
	if _, err := ParseBrief("bad.md", []byte("---\nname: a\n")); err == nil {
		t.Fatalf("expected an unclosed frontmatter fence to be an error")
	}
}

// TestParseBrief_NoNameFails checks that frontmatter with no declared name is a ParseError, since a brief cannot be matched into a roster without one.
func TestParseBrief_NoNameFails(t *testing.T) {
	content := "---\ndescription: missing a name\n---\nbody\n"
	if _, err := ParseBrief("bad.md", []byte(content)); err == nil {
		t.Fatalf("expected frontmatter with no name to be an error")
	}
}
