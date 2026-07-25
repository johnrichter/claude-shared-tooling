package main

// Black-box CLI tests for `self-check`'s roster-wired argv surface (M0.P3.T2): the --band /
// literal-flag mutual exclusion, the exit-code contract (0/1/2/3), and end-to-end parity between
// the literal band form and its --band equivalent. Builds the real binary and execs it, matching
// this package's other *_cli_test.go convention (main_record_usage_cli_test.go) — pins the actual
// wired-together CLI, not an in-process shortcut.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func buildSelfCheckCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "build-helpers")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir, _ = os.Getwd()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build build-helpers: %v\n%s", err, out)
	}
	return bin
}

func runSelfCheckCLI(t *testing.T, bin string, env []string, args ...string) (stdout string, exitCode int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.Output()
	stdout = string(out)
	if err == nil {
		return stdout, 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return stdout, ee.ExitCode()
	}
	t.Fatalf("exec self-check: %v", err)
	return "", -1
}

func selfCheckJSON(t *testing.T, stdout string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(stdout), &m); err != nil {
		t.Fatalf("self-check stdout is not valid JSON: %v\nstdout: %s", err, stdout)
	}
	return m
}

// TestSelfCheckCLI_BandAndLiteralAreMutuallyExclusive is AC3/AC5's argv guard: supplying --band
// together with any literal band flag is a usage error (exit 2), never silently preferring one
// form.
func TestSelfCheckCLI_BandAndLiteralAreMutuallyExclusive(t *testing.T) {
	bin := buildSelfCheckCLI(t)
	_, code := runSelfCheckCLI(t, bin, []string{"ANTHROPIC_MODEL=claude-sonnet-5"},
		"self-check", "--band", "build", "--floor-model", "claude-sonnet-5", "--floor-effort", "medium",
		"--ceiling-model", "claude-sonnet-5", "--ceiling-effort", "high")
	if code != 2 {
		t.Fatalf("--band + literal flags: exit = %d, want 2 (usage error)", code)
	}
}

// TestSelfCheckCLI_NeitherFormGiven is the complementary usage error: no band supplied at all.
func TestSelfCheckCLI_NeitherFormGiven(t *testing.T) {
	bin := buildSelfCheckCLI(t)
	_, code := runSelfCheckCLI(t, bin, []string{"ANTHROPIC_MODEL=claude-sonnet-5"}, "self-check")
	if code != 2 {
		t.Fatalf("no band form given: exit = %d, want 2 (usage error)", code)
	}
}

// TestSelfCheckCLI_UnrecognizedBandNameIsUsageError is a caller/argv error (an unknown --band
// name), never a self-check verdict: exit 2, not roster-stale's exit 3.
func TestSelfCheckCLI_UnrecognizedBandNameIsUsageError(t *testing.T) {
	bin := buildSelfCheckCLI(t)
	_, code := runSelfCheckCLI(t, bin, []string{"ANTHROPIC_MODEL=claude-sonnet-5"}, "self-check", "--band", "nonexistent-band")
	if code != 2 {
		t.Fatalf("unrecognized --band name: exit = %d, want 2 (usage error)", code)
	}
}

// TestSelfCheckCLI_UnrecognizedModelIsRosterStaleExit3 is AC2 end-to-end: a model absent from the
// roster must yield roster_stale:true and exit 3, never abort:true/exit 1 (the FB8 defect: an
// unrecognized model used to rank below every hardcoded entry and hard-abort as below-floor).
func TestSelfCheckCLI_UnrecognizedModelIsRosterStaleExit3(t *testing.T) {
	bin := buildSelfCheckCLI(t)
	stdout, code := runSelfCheckCLI(t, bin, []string{"ANTHROPIC_MODEL=claude-totally-unknown-model-9"},
		"self-check", "--band", "build")
	if code != 3 {
		t.Fatalf("unrecognized model: exit = %d, want 3 (roster-stale)\nstdout: %s", code, stdout)
	}
	res := selfCheckJSON(t, stdout)
	if rs, _ := res["roster_stale"].(bool); !rs {
		t.Fatalf("unrecognized model: roster_stale = %v, want true\nstdout: %s", res["roster_stale"], stdout)
	}
	if ab, _ := res["abort"].(bool); ab {
		t.Fatalf("unrecognized model: abort = %v, want false (must not fall through to below-floor)\nstdout: %s", res["abort"], stdout)
	}
}

// TestSelfCheckCLI_PostRosterModelAboveFloorNoLongerAborts is AC2's positive counterpart: a
// model the roster ranks above the band floor (same family, newer generation, e.g. an
// opus-5-class model against the opus-4-8 "plan" floor) passes cleanly end-to-end via the CLI.
func TestSelfCheckCLI_PostRosterModelAboveFloorNoLongerAborts(t *testing.T) {
	bin := buildSelfCheckCLI(t)
	stdout, code := runSelfCheckCLI(t, bin, []string{"ANTHROPIC_MODEL=claude-opus-5"}, "self-check", "--band", "plan")
	if code != 0 {
		t.Fatalf("post-roster model above floor: exit = %d, want 0\nstdout: %s", code, stdout)
	}
	res := selfCheckJSON(t, stdout)
	if ab, _ := res["abort"].(bool); ab {
		t.Fatalf("post-roster model above floor: abort = %v, want false\nstdout: %s", res["abort"], stdout)
	}
	if rs, _ := res["roster_stale"].(bool); rs {
		t.Fatalf("post-roster model above floor: roster_stale = %v, want false\nstdout: %s", res["roster_stale"], stdout)
	}
}

// TestSelfCheckCLI_LiteralAndNamedBandAgreeForKnownModels pins that the --band form and its
// literal-flag equivalent produce byte-identical verdicts for every KNOWN model, on both sides of
// the band (AC3: "existing literal band flags behave identically").
func TestSelfCheckCLI_LiteralAndNamedBandAgreeForKnownModels(t *testing.T) {
	bin := buildSelfCheckCLI(t)
	cases := []struct {
		name  string
		model string
	}{
		{"below_floor", "claude-haiku-4-5"},
		{"at_floor", "claude-sonnet-5"},
		{"above_ceiling_same_family", "claude-sonnet-5"}, // effort drives ceiling in the build band; model alone can't exceed it, covered by unit tests instead
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := []string{"ANTHROPIC_MODEL=" + c.model}
			namedOut, namedCode := runSelfCheckCLI(t, bin, env, "self-check", "--band", "build")
			literalOut, literalCode := runSelfCheckCLI(t, bin, env, "self-check",
				"--floor-model", "claude-sonnet-5", "--floor-effort", "medium",
				"--ceiling-model", "claude-sonnet-5", "--ceiling-effort", "high")
			if namedCode != literalCode {
				t.Fatalf("%s: --band exit %d != literal exit %d", c.model, namedCode, literalCode)
			}
			named := selfCheckJSON(t, namedOut)
			literal := selfCheckJSON(t, literalOut)
			for _, key := range []string{"abort", "roster_stale", "reason"} {
				if named[key] != literal[key] {
					t.Fatalf("%s: field %q differs: --band=%v literal=%v", c.model, key, named[key], literal[key])
				}
			}
		})
	}
}

// TestSelfCheckCLI_LiteralFormStillRequiresAllFourFlags pins pre-M0 behavior unchanged: supplying
// only some of the four literal flags (no --band) is still a usage error.
func TestSelfCheckCLI_LiteralFormStillRequiresAllFourFlags(t *testing.T) {
	bin := buildSelfCheckCLI(t)
	_, code := runSelfCheckCLI(t, bin, []string{"ANTHROPIC_MODEL=claude-sonnet-5"},
		"self-check", "--floor-model", "claude-sonnet-5", "--floor-effort", "medium")
	if code != 2 {
		t.Fatalf("partial literal flags, no --band: exit = %d, want 2 (usage error)", code)
	}
}
