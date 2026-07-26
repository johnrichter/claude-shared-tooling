package agentcontract

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSchema(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing fixture schema: %v", err)
	}
	return path
}

// TestCheckOutputSchema_ProseInsteadOfPathFails checks that an output_schema value describing a schema in prose fails as not-a-path.
func TestCheckOutputSchema_ProseInsteadOfPathFails(t *testing.T) {
	b := &Brief{
		Path: "a.md", Dir: t.TempDir(),
		Frontmatter: Frontmatter{Name: "a", Contract: Contract{
			OutputSchema: "a JSON object with a status field and a message field",
		}},
	}
	findings := checkOutputSchema(b, Options{})
	if !hasRule(findings, "SCHEMA-REF-NOT-A-PATH") {
		t.Fatalf("expected a prose description to fail as not-a-path, got: %v", findings)
	}
}

// TestCheckOutputSchema_MissingFails checks that an empty output_schema fails.
func TestCheckOutputSchema_MissingFails(t *testing.T) {
	b := &Brief{Path: "a.md", Dir: t.TempDir(), Frontmatter: Frontmatter{Name: "a"}}
	findings := checkOutputSchema(b, Options{})
	if !hasRule(findings, "SCHEMA-REF-MISSING") {
		t.Fatalf("expected an empty output_schema to fail, got: %v", findings)
	}
}

// TestCheckOutputSchema_ResolvablePathPasses checks that an output_schema resolving to a real file relative to the brief's directory passes.
func TestCheckOutputSchema_ResolvablePathPasses(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, dir, "out.schema.json", `{"type":"object"}`)
	b := &Brief{
		Path: "a.md", Dir: dir,
		Frontmatter: Frontmatter{Name: "a", Contract: Contract{OutputSchema: "out.schema.json"}},
	}
	if findings := checkOutputSchema(b, Options{}); len(findings) != 0 {
		t.Fatalf("expected a resolvable schema path to pass, got: %v", findings)
	}
}

// TestCheckOutputSchema_UnresolvablePathFails checks that a path-shaped output_schema pointing at a nonexistent file fails.
func TestCheckOutputSchema_UnresolvablePathFails(t *testing.T) {
	b := &Brief{
		Path: "a.md", Dir: t.TempDir(),
		Frontmatter: Frontmatter{Name: "a", Contract: Contract{OutputSchema: "does-not-exist.schema.json"}},
	}
	findings := checkOutputSchema(b, Options{})
	if !hasRule(findings, "SCHEMA-REF-UNRESOLVABLE") {
		t.Fatalf("expected an unresolvable path to fail, got: %v", findings)
	}
}

// TestCheckFailurePaths_NoStatedActionFails checks that a declared failure path with an empty action fails.
func TestCheckFailurePaths_NoStatedActionFails(t *testing.T) {
	b := &Brief{Path: "a.md", Frontmatter: Frontmatter{Name: "a", Contract: Contract{
		FailurePaths: []FailurePath{{Name: "malformed-input", Action: ""}},
	}}}
	findings := checkFailurePaths(b)
	if !hasRule(findings, "FAILUREPATH-NO-ACTION") {
		t.Fatalf("expected a failure path with no action to fail, got: %v", findings)
	}
}

// TestCheckFailurePaths_StatedActionPasses checks that a declared failure path with a stated action passes.
func TestCheckFailurePaths_StatedActionPasses(t *testing.T) {
	b := &Brief{Path: "a.md", Frontmatter: Frontmatter{Name: "a", Contract: Contract{
		FailurePaths: []FailurePath{{Name: "malformed-input", Action: "stop and report the parse error to the caller"}},
	}}}
	if findings := checkFailurePaths(b); len(findings) != 0 {
		t.Fatalf("expected a failure path with a stated action to pass, got: %v", findings)
	}
}

// TestCheckDecisions_RestatedUnderTwoNamesFails checks that two decisions sharing one normalized statement fail as a restated rule.
func TestCheckDecisions_RestatedUnderTwoNamesFails(t *testing.T) {
	b := &Brief{Path: "a.md", Frontmatter: Frontmatter{Name: "a", Contract: Contract{
		Decisions: []Decision{
			{Name: "rule-one", Statement: "a large artifact is written in bounded fragments to disk and assembled"},
			{Name: "rule-two", Statement: "a large artifact is written in bounded fragments to disk and assembled"},
		},
	}}}
	findings := checkDecisions(b)
	if !hasRule(findings, "DECISION-RESTATED") {
		t.Fatalf("expected two decisions sharing one statement to fail, got: %v", findings)
	}
}

// TestCheckDecisions_RestatedInBodyFails checks that a decision's statement appearing verbatim in the body again, instead of being referenced by name, fails.
func TestCheckDecisions_RestatedInBodyFails(t *testing.T) {
	statement := "a large artifact is written in bounded fragments to disk and assembled before it is returned"
	b := &Brief{
		Path: "a.md",
		Body: "Some intro text.\n\n" + statement + "\n\nMore text.",
		Frontmatter: Frontmatter{Name: "a", Contract: Contract{
			Decisions: []Decision{{Name: "fragment-write-rule", Statement: statement}},
		}},
	}
	findings := checkDecisions(b)
	if !hasRule(findings, "DECISION-RESTATED-IN-BODY") {
		t.Fatalf("expected a decision statement restated verbatim in the body to fail, got: %v", findings)
	}
}

// TestCheckDecisions_ReferencedByNamePasses checks that referencing a decision by name in the body, rather than restating its statement, passes.
func TestCheckDecisions_ReferencedByNamePasses(t *testing.T) {
	statement := "a large artifact is written in bounded fragments to disk and assembled before it is returned"
	b := &Brief{
		Path: "a.md",
		Body: "See the `fragment-write-rule` decision for how large artifacts are handled.",
		Frontmatter: Frontmatter{Name: "a", Contract: Contract{
			Decisions: []Decision{{Name: "fragment-write-rule", Statement: statement}},
		}},
	}
	if findings := checkDecisions(b); len(findings) != 0 {
		t.Fatalf("expected a decision referenced by name (not restated) to pass, got: %v", findings)
	}
}
