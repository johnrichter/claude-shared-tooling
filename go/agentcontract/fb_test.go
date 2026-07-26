package agentcontract

import "testing"

// TestCheckFB3_MissingRuleFails checks that a large_artifact brief stating neither rule fails as missing the fragment-write rule.
func TestCheckFB3_MissingRuleFails(t *testing.T) {
	b := &Brief{Path: "a.md", Body: "This agent writes a report.", Frontmatter: Frontmatter{
		Name: "a", Contract: Contract{LargeArtifact: true},
	}}
	findings := checkFB3(b)
	if !hasRule(findings, "FB3-MISSING-RULE") {
		t.Fatalf("expected large_artifact with no stated fragment-write rule to fail, got: %v", findings)
	}
}

// TestCheckFB3_SupersededRuleAloneFails checks that stating only the superseded split-across-dispatches rule fails.
func TestCheckFB3_SupersededRuleAloneFails(t *testing.T) {
	b := &Brief{Path: "a.md", Body: "If the artifact is too large, split the work across dispatches.",
		Frontmatter: Frontmatter{Name: "a", Contract: Contract{LargeArtifact: true}}}
	findings := checkFB3(b)
	if !hasRule(findings, "FB3-SUPERSEDED-RULE") {
		t.Fatalf("expected the superseded split-across-dispatches rule alone to fail, got: %v", findings)
	}
}

// TestCheckFB3_BothRulesIsContradiction checks that stating both the fragment-write rule and the superseded rule fails as one rule per decision.
func TestCheckFB3_BothRulesIsContradiction(t *testing.T) {
	b := &Brief{Path: "a.md",
		Body:        "Write bounded fragments to disk, assemble, then validate. Do not split the work across dispatches.",
		Frontmatter: Frontmatter{Name: "a", Contract: Contract{LargeArtifact: true}}}
	findings := checkFB3(b)
	if !hasRule(findings, "FB3-CONTRADICTION") {
		t.Fatalf("expected both rules present to fail as a contradiction (one rule per decision), got: %v", findings)
	}
}

// TestCheckFB3_CorrectRulePasses checks that stating only the fragment-write-to-disk rule passes.
func TestCheckFB3_CorrectRulePasses(t *testing.T) {
	b := &Brief{Path: "a.md",
		Body:        "An artifact too large for one payload is written as bounded fragments to disk, assembled, then validated before it is returned.",
		Frontmatter: Frontmatter{Name: "a", Contract: Contract{LargeArtifact: true}}}
	if findings := checkFB3(b); len(findings) != 0 {
		t.Fatalf("expected the correct fragment-write rule to pass, got: %v", findings)
	}
}

// TestCheckFB3_NotApplicableWhenNotLargeArtifact checks that the FB3 check is a no-op for a brief that never declares large_artifact.
func TestCheckFB3_NotApplicableWhenNotLargeArtifact(t *testing.T) {
	b := &Brief{Path: "a.md", Body: "split the work across dispatches",
		Frontmatter: Frontmatter{Name: "a", Contract: Contract{LargeArtifact: false}}}
	if findings := checkFB3(b); len(findings) != 0 {
		t.Fatalf("expected checkFB3 to be a no-op when large_artifact is false, got: %v", findings)
	}
}

const fb11Schema = `{
  "type": "object",
  "properties": {
    "edits": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "other_locations_asserting_claim": {
            "type": "array",
            "items": {"type": "string"},
            "description": "every other place asserting this claim; an explicit empty list means none"
          }
        },
        "required": ["other_locations_asserting_claim"]
      }
    }
  }
}`

// TestCheckFB11_MissingSchemaFieldFails checks that an edit-proposing brief whose output schema lacks the other-locations field fails.
func TestCheckFB11_MissingSchemaFieldFails(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, dir, "out.schema.json", `{"type":"object","properties":{"edits":{"type":"array"}}}`)
	b := &Brief{Path: "a.md", Dir: dir,
		Body: "Each proposed edit names the other locations asserting the same claim, or none.",
		Frontmatter: Frontmatter{Name: "a", Contract: Contract{
			EditProposing: true, OutputSchema: "out.schema.json",
		}}}
	findings := checkFB11(b, Options{})
	if !hasRule(findings, "FB11-SCHEMA-FIELD-MISSING") {
		t.Fatalf("expected a schema with no other_locations_asserting_claim property to fail, got: %v", findings)
	}
}

// TestCheckFB11_MissingProseFails checks that an edit-proposing brief whose prose omits the other-locations-per-edit requirement fails even with a compliant schema.
func TestCheckFB11_MissingProseFails(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, dir, "out.schema.json", fb11Schema)
	b := &Brief{Path: "a.md", Dir: dir,
		Body: "This agent proposes edits to a document.",
		Frontmatter: Frontmatter{Name: "a", Contract: Contract{
			EditProposing: true, OutputSchema: "out.schema.json",
		}}}
	findings := checkFB11(b, Options{})
	if !hasRule(findings, "FB11-MISSING-PROSE") {
		t.Fatalf("expected missing prose requirement to fail even though the schema is correct, got: %v", findings)
	}
}

// TestCheckFB11_SatisfiedInBothProseAndSchemaPasses checks that a brief stating the requirement in both its prose and its referenced schema passes.
func TestCheckFB11_SatisfiedInBothProseAndSchemaPasses(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, dir, "out.schema.json", fb11Schema)
	b := &Brief{Path: "a.md", Dir: dir,
		Body: "Each proposed edit names the other locations asserting the same claim, with an explicit none when there are no others.",
		Frontmatter: Frontmatter{Name: "a", Contract: Contract{
			EditProposing: true, OutputSchema: "out.schema.json",
		}}}
	if findings := checkFB11(b, Options{}); len(findings) != 0 {
		t.Fatalf("expected the field satisfied in both prose and schema to pass, got: %v", findings)
	}
}

// TestCheckFB11_NotApplicableWhenNotEditProposing checks that the FB11 check is a no-op for a brief that never declares edit_proposing.
func TestCheckFB11_NotApplicableWhenNotEditProposing(t *testing.T) {
	b := &Brief{Path: "a.md", Frontmatter: Frontmatter{Name: "a", Contract: Contract{EditProposing: false}}}
	if findings := checkFB11(b, Options{}); len(findings) != 0 {
		t.Fatalf("expected checkFB11 to be a no-op when edit_proposing is false, got: %v", findings)
	}
}
