package main

// Black-box CLI coverage for M17.P1.T1 (SCf): resolve-transcript's deterministic path resolution
// and self-check's session-id identity guard, exercised through the ACTUAL compiled binary (the
// bh package unit tests cover the pure functions; this file proves main.go's flag wiring and
// exit-code contract match the spec end-to-end).

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hermeticCLIEnv strips the self-check subcommand's live-session env fallbacks (ANTHROPIC_MODEL,
// CLAUDE_EFFORT) from the CLI subprocess's environment. Without this, a case built around a fixed
// transcript+band and asserting on something else entirely (e.g. session-id matching) would
// silently ride whatever model/effort the test RUNNER's own ambient session happens to have —
// making the case's pass/fail depend on who/what invokes `go test`, not on the code under test.
func hermeticCLIEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "ANTHROPIC_MODEL=") || strings.HasPrefix(kv, "CLAUDE_EFFORT=") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

const (
	cliOurs  = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	cliOther = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
)

// runCLIExpect runs bin with args and asserts the exact exit code, returning combined output.
func runCLIExpect(t *testing.T, bin string, wantExit int, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = hermeticCLIEnv()
	out, err := cmd.CombinedOutput()
	gotExit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			gotExit = ee.ExitCode()
		} else {
			t.Fatalf("cmd %v failed to run: %v\n%s", args, err, out)
		}
	}
	if gotExit != wantExit {
		t.Fatalf("cmd %v: exit=%d, want %d\noutput:\n%s", args, gotExit, wantExit, out)
	}
	return string(out)
}

// TestCLI_ResolveTranscript_NeverPicksNewerOtherSessionTranscript is the CLI-level twin of
// bh.TestResolveTranscriptPath_NeverPicksNewerOtherSessionTranscript: proves the SUBCOMMAND (not
// just the underlying pure function) never selects a concurrently-newer other-session transcript
// sitting in the same cwd-derived directory.
func TestCLI_ResolveTranscript_NeverPicksNewerOtherSessionTranscript(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	slugDir := filepath.Join(dir, "-home-user-my-app")
	if err := os.MkdirAll(slugDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ourPath := filepath.Join(slugDir, cliOurs+".jsonl")
	otherPath := filepath.Join(slugDir, cliOther+".jsonl")
	if err := os.WriteFile(ourPath, []byte(`{"sessionId":"`+cliOurs+`"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherPath, []byte(`{"sessionId":"`+cliOther+`"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := os.Chtimes(ourPath, now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(otherPath, now, now); err != nil {
		t.Fatal(err)
	}

	out2 := runCLIExpect(t, bin, 0, "resolve-transcript",
		"--scratchpad-path", filepath.Join(dir, "-home-user-my-app", cliOurs, "scratchpad", "note.txt"),
		"--projects-dir", dir)
	got := strings.TrimSpace(out2)
	if got != ourPath {
		t.Fatalf("resolve-transcript picked %q, want our own transcript %q (never the newer other-session file)", got, ourPath)
	}
}

// TestCLI_ResolveTranscript_MissingTranscriptExitsNonZero covers the "transcript file absent"
// edge case at the CLI boundary.
func TestCLI_ResolveTranscript_MissingTranscriptExitsNonZero(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	out := runCLIExpect(t, bin, 2, "resolve-transcript",
		"--scratchpad-path", filepath.Join(dir, "-slug", cliOurs, "scratchpad"),
		"--projects-dir", dir)
	if !strings.Contains(out, "not found") {
		t.Fatalf("expected a clear \"not found\" message, got: %s", out)
	}
}

// TestCLI_ResolveTranscript_MalformedSessionIDExitsNonZero covers "session id absent/unparseable".
func TestCLI_ResolveTranscript_MalformedSessionIDExitsNonZero(t *testing.T) {
	bin := buildCLI(t)
	runCLIExpect(t, bin, 2, "resolve-transcript", "--session-id", "not-a-uuid")
}

// selfCheckBand returns the required band flags as a slice, factored out so every self-check CLI
// case below shares the exact same passing band.
func selfCheckBand() []string {
	return []string{
		"--floor-model", "claude-sonnet-5", "--floor-effort", "medium",
		"--ceiling-model", "claude-sonnet-5", "--ceiling-effort", "high",
	}
}

// TestCLI_SelfCheck_SessionIDMatch_ExitsZero proves the identity guard passes (exit 0) when the
// transcript's trailing line names the caller's own session id.
func TestCLI_SelfCheck_SessionIDMatch_ExitsZero(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	transcript := filepath.Join(dir, "t.jsonl")
	if err := os.WriteFile(transcript, []byte(`{"sessionId":"`+cliOurs+`","message":{"model":"claude-sonnet-5"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"self-check"}, selfCheckBand()...)
	args = append(args, "--transcript", transcript, "--session-id", cliOurs)
	out := runCLIExpect(t, bin, 0, args...)
	if !strings.Contains(out, `"session_id_match": true`) {
		t.Fatalf("expected session_id_match true, got: %s", out)
	}
}

// TestCLI_SelfCheck_SessionIDMismatch_HardAborts is the SCf acceptance case: a transcript naming a
// DIFFERENT session's id must hard-abort (exit 1, abort:true) -- never a warning.
func TestCLI_SelfCheck_SessionIDMismatch_HardAborts(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	transcript := filepath.Join(dir, "t.jsonl")
	if err := os.WriteFile(transcript, []byte(`{"sessionId":"`+cliOther+`","message":{"model":"claude-sonnet-5"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"self-check"}, selfCheckBand()...)
	args = append(args, "--transcript", transcript, "--session-id", cliOurs)
	out := runCLIExpect(t, bin, 1, args...)
	if !strings.Contains(out, `"abort": true`) {
		t.Fatalf("expected abort true on session-id mismatch, got: %s", out)
	}
}

// TestCLI_SelfCheck_EmptyTranscriptWithIdentityGuard_ExitsNonZero and its siblings cover the
// remaining SCf edge cases at the CLI boundary: empty transcript, malformed-only transcript,
// transcript naming no sessionId -- each a defined nonzero exit, never a silent pass.
func TestCLI_SelfCheck_EmptyTranscriptWithIdentityGuard_ExitsNonZero(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	transcript := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(transcript, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"self-check"}, selfCheckBand()...)
	args = append(args, "--transcript", transcript, "--session-id", cliOurs)
	runCLIExpect(t, bin, 2, args...)
}

func TestCLI_SelfCheck_MalformedTranscriptWithIdentityGuard_ExitsNonZero(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	transcript := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(transcript, []byte("not json\n{{{broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"self-check"}, selfCheckBand()...)
	args = append(args, "--transcript", transcript, "--session-id", cliOurs)
	runCLIExpect(t, bin, 2, args...)
}

func TestCLI_SelfCheck_TranscriptNamesNoSessionID_ExitsNonZero(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	transcript := filepath.Join(dir, "nosid.jsonl")
	if err := os.WriteFile(transcript, []byte(`{"message":{"model":"claude-sonnet-5"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"self-check"}, selfCheckBand()...)
	args = append(args, "--transcript", transcript, "--session-id", cliOurs)
	out := runCLIExpect(t, bin, 2, args...)
	if !strings.Contains(out, "names no sessionId") {
		t.Fatalf("expected a clear \"names no sessionId\" message, got: %s", out)
	}
}

// TestCLI_SelfCheck_UnparseableSessionID_ExitsNonZero covers "session id absent/unparseable" at
// the self-check identity-guard boundary (distinct from resolve-transcript's own check).
func TestCLI_SelfCheck_UnparseableSessionID_ExitsNonZero(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	transcript := filepath.Join(dir, "t.jsonl")
	if err := os.WriteFile(transcript, []byte(`{"sessionId":"`+cliOurs+`","message":{"model":"claude-sonnet-5"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"self-check"}, selfCheckBand()...)
	args = append(args, "--transcript", transcript, "--session-id", "not-a-uuid")
	runCLIExpect(t, bin, 2, args...)
}
