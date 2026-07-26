package agentcontract

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestCheckFB11_SchemaFieldNotRequiredFails checks that a schema declaring the
// other_locations_asserting_claim property but not listing it under "required" fails distinctly
// from the field-missing case.
func TestCheckFB11_SchemaFieldNotRequiredFails(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, dir, "out.schema.json", `{
	  "type": "object",
	  "properties": {
	    "edits": {
	      "type": "array",
	      "items": {
	        "type": "object",
	        "properties": {
	          "other_locations_asserting_claim": {
	            "type": "array",
	            "description": "none means no other locations"
	          }
	        }
	      }
	    }
	  }
	}`)
	b := &Brief{Path: "a.md", Dir: dir,
		Body: "Each proposed edit names the other locations asserting the same claim, or none.",
		Frontmatter: Frontmatter{Name: "a", Contract: Contract{
			EditProposing: true, OutputSchema: "out.schema.json",
		}}}
	findings := checkFB11(b, Options{})
	if !hasRule(findings, "FB11-SCHEMA-FIELD-NOT-REQUIRED") {
		t.Fatalf("expected a declared-but-not-required field to fail distinctly, got: %v", findings)
	}
	if hasRule(findings, "FB11-SCHEMA-FIELD-MISSING") {
		t.Fatalf("a declared property must not also be reported as missing, got: %v", findings)
	}
}

// TestCheckFB11_SchemaFieldNoNoneFails checks that a required, declared field whose definition
// never documents an explicit "none" reading fails.
func TestCheckFB11_SchemaFieldNoNoneFails(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, dir, "out.schema.json", `{
	  "type": "object",
	  "properties": {
	    "edits": {
	      "type": "array",
	      "items": {
	        "type": "object",
	        "properties": {
	          "other_locations_asserting_claim": {"type": "array", "items": {"type": "string"}}
	        },
	        "required": ["other_locations_asserting_claim"]
	      }
	    }
	  }
	}`)
	b := &Brief{Path: "a.md", Dir: dir,
		Body: "Each proposed edit names the other locations asserting the same claim, or none.",
		Frontmatter: Frontmatter{Name: "a", Contract: Contract{
			EditProposing: true, OutputSchema: "out.schema.json",
		}}}
	findings := checkFB11(b, Options{})
	if !hasRule(findings, "FB11-SCHEMA-FIELD-NO-NONE") {
		t.Fatalf("expected a field with no documented none reading to fail, got: %v", findings)
	}
}

// TestCheckFB11_UnreadableSchemaFails checks that a referenced schema file that is not valid
// JSON is reported distinctly rather than silently skipped.
func TestCheckFB11_UnreadableSchemaFails(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, dir, "out.schema.json", `{not valid json`)
	b := &Brief{Path: "a.md", Dir: dir,
		Body: "Each proposed edit names the other locations asserting the same claim, or none.",
		Frontmatter: Frontmatter{Name: "a", Contract: Contract{
			EditProposing: true, OutputSchema: "out.schema.json",
		}}}
	findings := checkFB11(b, Options{})
	if !hasRule(findings, "FB11-SCHEMA-UNREADABLE") {
		t.Fatalf("expected an unparseable schema to fail distinctly, got: %v", findings)
	}
}

// TestCheckFB11_UnresolvableSchemaDoesNotDoubleReport checks that when the schema path itself
// cannot be resolved, FB11 defers to checkOutputSchema's finding instead of also emitting a
// schema-shape finding under a different rule name.
func TestCheckFB11_UnresolvableSchemaDoesNotDoubleReport(t *testing.T) {
	b := &Brief{Path: "a.md", Dir: t.TempDir(),
		Body: "Each proposed edit names the other locations asserting the same claim, or none.",
		Frontmatter: Frontmatter{Name: "a", Contract: Contract{
			EditProposing: true, OutputSchema: "missing.schema.json",
		}}}
	findings := checkFB11(b, Options{})
	for _, f := range findings {
		if strings.HasPrefix(f.Rule, "FB11-SCHEMA") {
			t.Fatalf("expected no FB11-SCHEMA-* finding when the path itself is unresolvable, got: %v", findings)
		}
	}
}

// TestDiscoverRosters_CaseSensitiveDirName checks that a directory named "Agents" (wrong case)
// is not treated as a roster — roster-ness is a literal filesystem-name match, not a
// case-insensitive convention.
func TestDiscoverRosters_CaseSensitiveDirName(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "plugin", "Agents", "alpha.md"), alphaBrief)

	rosters, err := DiscoverRosters(root)
	if err != nil {
		t.Fatalf("DiscoverRosters: %v", err)
	}
	if len(rosters) != 0 {
		t.Fatalf("expected a differently-cased directory name to form no roster, got: %+v", rosters)
	}
}

// TestDiscoverRosters_MultipleRostersAreIndependent checks that two separate "agents"
// directories under one root each form their own closed roster — a brief in one is never a
// sibling of a brief in the other.
func TestDiscoverRosters_MultipleRostersAreIndependent(t *testing.T) {
	root := t.TempDir()
	writeValidRoster(t, filepath.Join(root, "pluginA", "agents"))
	writeFile(t, filepath.Join(root, "pluginB", "agents", "solo.md"), `---
name: solo
contract:
  output_schema: solo.schema.json
---
Body.
`)
	writeFile(t, filepath.Join(root, "pluginB", "agents", "solo.schema.json"), `{"type":"object"}`)

	report, err := Lint(root, Options{})
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if report.RostersChecked != 2 || report.BriefsChecked != 3 {
		t.Fatalf("expected 2 independent rosters totaling 3 briefs, got %d/%d", report.RostersChecked, report.BriefsChecked)
	}
	if !report.Pass() {
		t.Fatalf("expected both independently-closed rosters to pass on their own terms, got: %v", report.Findings)
	}
}

// TestCheckMatrix_NonFuzzyCellWithNoTieBreakPasses checks that a non-fuzzy cell is not required
// to carry a tie_break — the requirement is conditional on Fuzzy, not universal.
func TestCheckMatrix_NonFuzzyCellWithNoTieBreakPasses(t *testing.T) {
	a := brief("a", map[string]Discriminator{
		"b": {Relation: RelationDiscriminator, Reason: "different scope", Fuzzy: false},
	})
	b := brief("b", map[string]Discriminator{
		"a": {Relation: RelationNotConfusable, Reason: "different scope"},
	})
	r := Roster{Dir: "agents", Briefs: []*Brief{a, b}}

	if findings := CheckMatrix(r); len(findings) != 0 {
		t.Fatalf("expected a non-fuzzy cell with no tie_break to pass, got: %v", findings)
	}
}

// TestLooksLikePath rejects prose and accepts genuine schema-file references, including ones
// with directory components.
func TestLooksLikePath(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"schema.json", true},
		{"schemas/edit-proposal.schema.json", true},
		{"a JSON object describing the result", false},
		{"schema without an extension", false},
		{"", false},
		{"schema.txt", false},
		{"schema.JSON", false}, // extension match is case-sensitive; documents current behavior
	}
	for _, c := range cases {
		if got := looksLikePath(c.in); got != c.want {
			t.Errorf("looksLikePath(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestResolveSchemaPath_AbsolutePath checks that an absolute output_schema path is resolved
// directly rather than joined against the brief's directory.
func TestResolveSchemaPath_AbsolutePath(t *testing.T) {
	dir := t.TempDir()
	abs := writeSchema(t, dir, "abs.schema.json", `{"type":"object"}`)
	b := &Brief{Path: "a.md", Dir: t.TempDir(), Frontmatter: Frontmatter{
		Contract: Contract{OutputSchema: abs},
	}}
	got, ok := resolveSchemaPath(b, nil)
	if !ok || got != abs {
		t.Fatalf("expected absolute path to resolve directly, got %q, %v", got, ok)
	}
}

// TestReport_ReviewerCheckedStatedOnFailureToo checks that the honest completeness-not-quality
// limit is present even on a report with findings, never only on a clean pass.
func TestReport_ReviewerCheckedStatedOnFailureToo(t *testing.T) {
	r := NewReport(1, 2, []Finding{{Rule: "MATRIX-MISSING-CELL", Brief: "a.md", Message: "x"}})
	if len(r.ReviewerChecked) == 0 {
		t.Fatalf("expected the reviewer-checked limit to be present on a failing report")
	}
	var sb strings.Builder
	if err := r.Render(&sb); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := sb.String()
	if !strings.Contains(out, "FAIL") {
		t.Fatalf("expected rendered output to state FAIL, got: %s", out)
	}
	if !strings.Contains(out, "reviewer-checked") && !strings.Contains(out, "Reviewer-checked") {
		t.Fatalf("expected the rendered failing report to still state the reviewer-checked limit, got: %s", out)
	}
}

// TestCLI_ExitCodes builds the agentcontract-lint binary and exercises its documented exit
// codes end to end: 0 clean, 1 findings present, 2 discovery error.
func TestCLI_ExitCodes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix exit-code assumptions")
	}
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "agentcontract-lint")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/agentcontract-lint")
	build.Dir = mustGetwd(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building agentcontract-lint: %v\n%s", err, out)
	}

	t.Run("clean roster exits 0", func(t *testing.T) {
		root := t.TempDir()
		writeValidRoster(t, filepath.Join(root, "plugin", "agents"))
		cmd := exec.Command(binPath, root)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("expected exit 0 for a clean roster, got err=%v output=%s", err, out)
		}
		if !strings.Contains(string(out), "PASS") {
			t.Fatalf("expected PASS in output, got: %s", out)
		}
	})

	t.Run("roster with findings exits 1", func(t *testing.T) {
		root := t.TempDir()
		agentsDir := filepath.Join(root, "plugin", "agents")
		writeFile(t, filepath.Join(agentsDir, "alpha.md"), `---
name: alpha
contract:
  output_schema: alpha.schema.json
---
Body.
`)
		writeFile(t, filepath.Join(agentsDir, "beta.md"), betaBrief)
		writeFile(t, filepath.Join(agentsDir, "alpha.schema.json"), `{"type":"object"}`)
		writeFile(t, filepath.Join(agentsDir, "beta.schema.json"), `{"type":"object"}`)

		cmd := exec.Command(binPath, root)
		out, err := cmd.CombinedOutput()
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 1 {
			t.Fatalf("expected exit 1 for a roster with findings, got err=%v output=%s", err, out)
		}
		if !strings.Contains(string(out), "FAIL") {
			t.Fatalf("expected FAIL in output, got: %s", out)
		}
	})

	t.Run("nonexistent root exits 2", func(t *testing.T) {
		cmd := exec.Command(binPath, filepath.Join(t.TempDir(), "does-not-exist"))
		_, err := cmd.CombinedOutput()
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 2 {
			t.Fatalf("expected exit 2 for a nonexistent root, got err=%v", err)
		}
	})
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	return wd
}
