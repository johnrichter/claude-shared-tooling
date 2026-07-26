package agentcontract

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

const alphaBrief = `---
name: alpha
description: "Handles ingestion"
contract:
  output_schema: alpha.schema.json
  decisions:
    - name: scope-rule
      statement: "alpha only ever touches files under src/alpha"
  failure_paths:
    - name: malformed-input
      action: "stop and report the parse error to the caller"
  discriminators:
    beta:
      relation: discriminator
      reason: "alpha handles ingestion, beta handles output formatting"
      fuzzy: false
---
# Alpha

See the ` + "`scope-rule`" + ` decision for what alpha owns.
`

const betaBrief = `---
name: beta
description: "Handles output formatting"
contract:
  output_schema: beta.schema.json
  discriminators:
    alpha:
      relation: discriminator
      reason: "beta handles output formatting, alpha handles ingestion"
      fuzzy: false
---
# Beta

Formats output.
`

func writeValidRoster(t *testing.T, agentsDir string) {
	t.Helper()
	writeFile(t, filepath.Join(agentsDir, "alpha.md"), alphaBrief)
	writeFile(t, filepath.Join(agentsDir, "beta.md"), betaBrief)
	writeFile(t, filepath.Join(agentsDir, "alpha.schema.json"), `{"type":"object"}`)
	writeFile(t, filepath.Join(agentsDir, "beta.schema.json"), `{"type":"object"}`)
}

// TestDiscoverRosters_FindsAgentsDirectory checks that every brief directly inside an "agents" directory is grouped into one roster.
func TestDiscoverRosters_FindsAgentsDirectory(t *testing.T) {
	root := t.TempDir()
	agentsDir := filepath.Join(root, "plugin", "agents")
	writeValidRoster(t, agentsDir)

	rosters, err := DiscoverRosters(root)
	if err != nil {
		t.Fatalf("DiscoverRosters: %v", err)
	}
	if len(rosters) != 1 || len(rosters[0].Briefs) != 2 {
		t.Fatalf("expected one roster of two briefs, got: %+v", rosters)
	}
}

// TestDiscoverRosters_NonAgentsDirectoryIgnored checks that a directory not named "agents" forms no roster, however many brief-shaped files it holds.
func TestDiscoverRosters_NonAgentsDirectoryIgnored(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "notes", "alpha.md"), alphaBrief)

	rosters, err := DiscoverRosters(root)
	if err != nil {
		t.Fatalf("DiscoverRosters: %v", err)
	}
	if len(rosters) != 0 {
		t.Fatalf("expected a directory not named agents to form no roster, got: %+v", rosters)
	}
}

// TestDiscoverRosters_BrokenBriefIsHardError checks that a brief failing to parse inside an agents directory is a discovery error, never a silently skipped file.
func TestDiscoverRosters_BrokenBriefIsHardError(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "plugin", "agents", "broken.md"), "# no frontmatter\n")

	if _, err := DiscoverRosters(root); err == nil {
		t.Fatalf("expected a brief that fails to parse to be a hard discovery error, not a silent skip")
	}
}

// TestLint_ValidRosterPasses checks that a fully compliant two-agent roster passes end to end through Lint.
func TestLint_ValidRosterPasses(t *testing.T) {
	root := t.TempDir()
	writeValidRoster(t, filepath.Join(root, "plugin", "agents"))

	report, err := Lint(root, Options{})
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if !report.Pass() {
		t.Fatalf("expected a fully compliant roster to pass, got findings: %v", report.Findings)
	}
	if report.RostersChecked != 1 || report.BriefsChecked != 2 {
		t.Fatalf("expected 1 roster / 2 briefs, got %d/%d", report.RostersChecked, report.BriefsChecked)
	}
}

// TestLint_EmptyTreePassesVacuously checks that a tree with no agents directories passes with zero rosters checked.
func TestLint_EmptyTreePassesVacuously(t *testing.T) {
	root := t.TempDir()
	report, err := Lint(root, Options{})
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if !report.Pass() || report.RostersChecked != 0 {
		t.Fatalf("expected a tree with no agents directories to pass with zero rosters, got: %+v", report)
	}
}

// TestLint_IncompleteRosterFails checks that a roster with one member declaring no discriminator cells fails end to end through Lint.
func TestLint_IncompleteRosterFails(t *testing.T) {
	root := t.TempDir()
	agentsDir := filepath.Join(root, "plugin", "agents")
	// alpha declares no discriminators at all against its one sibling.
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

	report, err := Lint(root, Options{})
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if report.Pass() {
		t.Fatalf("expected alpha's missing cell against beta to fail the lint")
	}
}
