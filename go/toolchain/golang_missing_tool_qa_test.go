package toolchain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// This file is adversarial QA added during re-verification: it targets a gap
// the existing suite (golang_sanity_test.go, golang_adversarial_qa_test.go,
// golang_e2e_probe_test.go) leaves unexercised — what each multi-tool check
// (lint, vet, security, test unit) does when one of its two tools is not on
// PATH at all, as opposed to present-and-clean or present-and-reporting. Per
// adapter.go's Adapter.RunInProcess contract, that must be a returned error
// (an infrastructure failure), never a Diagnostic and never a silent
// success — a missing tool must not read as a clean run.

// minimalPATH builds a directory on the test's PATH containing symlinks only
// to the named tools (resolved from the real PATH), so a check's other,
// unlisted tool is genuinely absent rather than merely unpopular in this
// process's environment. It skips the calling test if a requested tool
// cannot be found to link in the first place, since this probe must control
// exactly which tools are absent, not accidentally remove one that was never
// present.
func minimalPATH(t *testing.T, keep ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, tool := range keep {
		real, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s not on PATH; cannot build a minimal-PATH fixture that keeps it", tool)
		}
		if err := os.Symlink(real, filepath.Join(dir, tool)); err != nil {
			t.Fatalf("symlink %s: %v", tool, err)
		}
	}
	return dir
}

// TestQAGoAdapterLintErrorsWhenGolangciLintMissing checks runLint returns an
// error, not a Diagnostic, when golangci-lint itself is absent from PATH —
// the first of the two tools it spawns.
func TestQAGoAdapterLintErrorsWhenGolangciLintMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", minimalPATH(t, "goimports"))
	a := NewGoAdapter()
	_, err := a.RunInProcess(context.Background(), Target{Check: CheckLint, Dir: dir})
	if err == nil {
		t.Fatal("RunInProcess(lint) with golangci-lint absent = nil error, want an infrastructure error")
	}
}

// TestQAGoAdapterVetErrorsWhenStaticcheckMissing checks runVet returns an
// error, not a Diagnostic, when staticcheck is absent from PATH — go vet
// itself (the other tool) is kept present and would otherwise run clean.
func TestQAGoAdapterVetErrorsWhenStaticcheckMissing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/missingtool\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	t.Setenv("PATH", minimalPATH(t, "go"))
	a := NewGoAdapter()
	_, err := a.RunInProcess(context.Background(), Target{Check: CheckVet, Dir: dir})
	if err == nil {
		t.Fatal("RunInProcess(vet) with staticcheck absent = nil error, want an infrastructure error")
	}
}

// TestQAGoAdapterSecurityErrorsWhenGovulncheckMissing checks runSecurity
// returns an error, not a Diagnostic, when govulncheck is absent — gosec (run
// first) is kept present so the failure is attributable to the second tool's
// absence, not the first's.
func TestQAGoAdapterSecurityErrorsWhenGovulncheckMissing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/missingtool\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	t.Setenv("PATH", minimalPATH(t, "gosec"))
	a := NewGoAdapter()
	_, err := a.RunInProcess(context.Background(), Target{Check: CheckSecurity, Dir: dir})
	if err == nil {
		t.Fatal("RunInProcess(security) with govulncheck absent = nil error, want an infrastructure error")
	}
}

// TestQAGoAdapterUnitTestErrorsWhenGotestsumMissing checks runUnitTest
// returns an error, not a Diagnostic, when gotestsum is absent — the one tool
// the unit-test pair spawns (OD50's coverage/report wrapper), distinct from
// runE2ETest which spawns plain go test instead and is unaffected.
func TestQAGoAdapterUnitTestErrorsWhenGotestsumMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", minimalPATH(t, "go"))
	a := NewGoAdapter()
	_, err := a.RunInProcess(context.Background(), Target{Check: CheckTest, Test: TestUnit, Dir: dir})
	if err == nil {
		t.Fatal("RunInProcess(test unit) with gotestsum absent = nil error, want an infrastructure error")
	}
}

// TestQAGoAdapterE2ETestUnaffectedByGotestsumAbsence checks runE2ETest spawns
// plain go test (never gotestsum) so the e2e kind still runs — and can still
// fail on its own test content — when gotestsum is entirely absent, unlike
// the unit kind above. This pins the MATRIX-documented asymmetry: gotestsum
// is unit's tool, not e2e's.
func TestQAGoAdapterE2ETestUnaffectedByGotestsumAbsence(t *testing.T) {
	dir := writeGoProbeModule(t, probeCleanMain, "")
	if err := os.WriteFile(filepath.Join(dir, "main_e2e_test.go"),
		[]byte("//go:build e2e\n\npackage main\n\nimport \"testing\"\n\nfunc TestE2ESmoke(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatalf("write e2e test: %v", err)
	}
	t.Setenv("PATH", minimalPATH(t, "go"))
	a := NewGoAdapter()
	diags, err := a.RunInProcess(context.Background(), Target{Check: CheckTest, Test: TestE2E, Dir: dir})
	if err != nil {
		t.Fatalf("RunInProcess(test e2e) with gotestsum absent = unexpected error %v, want it to still run via plain go test", err)
	}
	if len(diags) != 0 {
		t.Errorf("RunInProcess(test e2e) on a clean e2e test = %+v, want no diagnostics", diags)
	}
}
