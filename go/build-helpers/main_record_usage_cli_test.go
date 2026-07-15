package main

// Black-box CLI tests for `record-usage` (ACC1, M13.P2.T1). These build the real binary and exec it
// exactly as the orchestrator skill does (SKILL.md's `record-usage … --final` at finish), so the
// assertions pin the actual exit codes and stdout/stderr contract — not an in-process shortcut that
// could pass while the wired-together CLI behaves differently.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildRecordUsageCLI compiles the build-helpers binary once into a t.TempDir and returns its path. Skipped
// (not failed) when the go toolchain can't build — keeps this test from blocking on an environment
// that can't compile, matching the package's "best-effort tooling" convention elsewhere.
func buildRecordUsageCLI(t *testing.T) string {
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

// specsFilePath resolves anthropic-specifications.json the same way schema_sync_test.go does
// (co-located two directories up from build-helpers/), skipping if absent.
func specsFilePath(t *testing.T) string {
	t.Helper()
	p := filepath.Join("..", "anthropic-specifications.json")
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Skipf("anthropic-specifications.json not found at %s (skipping CLI test): %v", abs, err)
	}
	return abs
}

const oneTurnTranscript = `{"type":"assistant","message":{"model":"claude-sonnet-5-20260101","usage":{"input_tokens":100,"output_tokens":50,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
`

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runRecordUsageCLI(t *testing.T, bin string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run %v: %v", args, err)
		}
	}
	return outBuf.String(), errBuf.String(), code
}

// TestRecordUsageFinal_ResolvedTranscript_RecordsO pins the finish-time --final path (ACC1
// criterion 1+3): a resolvable main transcript yields exit 0, cost_status absent (resolved), and
// orchestrator-only O persisted into execution.json's run_config.accounting.
func TestRecordUsageFinal_ResolvedTranscript_RecordsO(t *testing.T) {
	bin := buildRecordUsageCLI(t)
	specs := specsFilePath(t)
	dir := t.TempDir()
	exPath := filepath.Join(dir, "execution.json")
	transcript := filepath.Join(dir, "main.jsonl")
	writeFile(t, exPath, "{}")
	writeFile(t, transcript, oneTurnTranscript)

	stdout, stderr, code := runRecordUsageCLI(t, bin, "record-usage", exPath, "--transcript", transcript, "--specs", specs, "--final", "--at", "2026-07-05T00:00:00Z")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr)
	}
	var ex execDoc
	if err := json.Unmarshal([]byte(stdout), &ex); err != nil {
		t.Fatalf("record-usage stdout not valid JSON: %v\n%s", err, stdout)
	}
	acc := ex.RunConfig.Accounting
	if acc == nil {
		t.Fatal("run_config.accounting missing")
	}
	if acc.CostStatus != "" {
		t.Fatalf("cost_status = %q, want \"\" on a resolved run", acc.CostStatus)
	}
	if acc.Orchestrator == nil || acc.Orchestrator.CostUSD <= 0 {
		t.Fatalf("orchestrator-only O not persisted: %+v", acc.Orchestrator)
	}
}

// TestRecordUsageFinal_IsIdempotent pins ACC1 criterion 1 (resume-safe/idempotent): running --final
// twice in a row over the SAME unchanged transcript must persist byte-identical accounting, proving
// the finish-time snapshot self-heals rather than accumulating on top of itself on a re-run (e.g. a
// retried finish step).
func TestRecordUsageFinal_IsIdempotent(t *testing.T) {
	bin := buildRecordUsageCLI(t)
	specs := specsFilePath(t)
	dir := t.TempDir()
	exPath := filepath.Join(dir, "execution.json")
	transcript := filepath.Join(dir, "main.jsonl")
	writeFile(t, exPath, "{}")
	writeFile(t, transcript, oneTurnTranscript)

	stdout1, _, code1 := runRecordUsageCLI(t, bin, "record-usage", exPath, "--transcript", transcript, "--specs", specs, "--final", "--at", "2026-07-05T00:00:00Z")
	if code1 != 0 {
		t.Fatalf("first --final exit = %d", code1)
	}
	writeFile(t, exPath, stdout1) // simulate the skill's `> tmp && mv tmp execution.json`

	stdout2, _, code2 := runRecordUsageCLI(t, bin, "record-usage", exPath, "--transcript", transcript, "--specs", specs, "--final", "--at", "2026-07-05T00:00:00Z")
	if code2 != 0 {
		t.Fatalf("second --final exit = %d", code2)
	}
	a := normalizeExecJSON(t, stdout1)
	b := normalizeExecJSON(t, stdout2)
	if a != b {
		t.Fatalf("non-idempotent --final rerun:\nfirst:  %s\nsecond: %s", a, b)
	}
}

// TestRecordUsageFinal_UnresolvedTranscript_NonFatalMarker pins ACC1 criterion 2: a main transcript
// that cannot be read is non-fatal by default — exit 0, cost_status:unresolved written and loud
// (logged), never silently dropped.
func TestRecordUsageFinal_UnresolvedTranscript_NonFatalMarker(t *testing.T) {
	bin := buildRecordUsageCLI(t)
	specs := specsFilePath(t)
	dir := t.TempDir()
	exPath := filepath.Join(dir, "execution.json")
	missing := filepath.Join(dir, "does-not-exist.jsonl")
	writeFile(t, exPath, "{}")

	stdout, stderr, code := runRecordUsageCLI(t, bin, "record-usage", exPath, "--transcript", missing, "--specs", specs, "--final", "--at", "2026-07-05T01:00:00Z")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (non-fatal); stderr=%s", code, stderr)
	}
	var ex execDoc
	if err := json.Unmarshal([]byte(stdout), &ex); err != nil {
		t.Fatalf("stdout not valid JSON: %v\n%s", err, stdout)
	}
	if ex.RunConfig.Accounting == nil || ex.RunConfig.Accounting.CostStatus != "unresolved" {
		t.Fatalf("cost_status = %+v, want \"unresolved\"", ex.RunConfig.Accounting)
	}
	found := false
	for _, l := range ex.Log {
		if strings.Contains(l, "cost_status:unresolved") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a logged cost_status:unresolved note, got: %v", ex.Log)
	}
}

// TestRecordUsageBaselineCapture_UnresolvedTranscript_Fatal pins ACC1 criterion 2's other half: the
// SAME unresolved condition fails a --baseline-capture run (nonzero exit, no execution.json emitted
// to stdout) instead of writing the non-fatal marker — a baseline-capture measurement cannot tolerate
// a silently-degraded O.
func TestRecordUsageBaselineCapture_UnresolvedTranscript_Fatal(t *testing.T) {
	bin := buildRecordUsageCLI(t)
	specs := specsFilePath(t)
	dir := t.TempDir()
	exPath := filepath.Join(dir, "execution.json")
	missing := filepath.Join(dir, "does-not-exist.jsonl")
	writeFile(t, exPath, "{}")

	stdout, stderr, code := runRecordUsageCLI(t, bin, "record-usage", exPath, "--transcript", missing, "--specs", specs, "--final", "--baseline-capture", "--at", "2026-07-05T02:00:00Z")
	if code == 0 {
		t.Fatalf("exit = 0, want nonzero for a baseline-capture run with an unresolved transcript")
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("expected no stdout emission on a fatal baseline-capture run, got: %q", stdout)
	}
	if !strings.Contains(stderr, "baseline-capture") {
		t.Fatalf("stderr does not explain the baseline-capture failure: %q", stderr)
	}
}

// TestRecordUsageBaselineCapture_ResolvedTranscript_Succeeds pins that --baseline-capture is ONLY
// stricter about the unresolved condition — a resolvable transcript behaves identically to a normal
// run (exit 0, O persisted), so baseline-capture never over-fires on the happy path.
func TestRecordUsageBaselineCapture_ResolvedTranscript_Succeeds(t *testing.T) {
	bin := buildRecordUsageCLI(t)
	specs := specsFilePath(t)
	dir := t.TempDir()
	exPath := filepath.Join(dir, "execution.json")
	transcript := filepath.Join(dir, "main.jsonl")
	writeFile(t, exPath, "{}")
	writeFile(t, transcript, oneTurnTranscript)

	stdout, stderr, code := runRecordUsageCLI(t, bin, "record-usage", exPath, "--transcript", transcript, "--specs", specs, "--final", "--baseline-capture", "--at", "2026-07-05T00:00:00Z")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr)
	}
	var ex execDoc
	if err := json.Unmarshal([]byte(stdout), &ex); err != nil {
		t.Fatalf("stdout not valid JSON: %v\n%s", err, stdout)
	}
	if ex.RunConfig.Accounting == nil || ex.RunConfig.Accounting.Orchestrator == nil {
		t.Fatal("baseline-capture happy path did not persist O")
	}
}

// TestRecordUsage_OIsolatedFromSameModelSubagent pins ACC1 criterion 3's isolation guarantee at the
// CLI boundary: a subagent transcript sharing the main transcript's model must never leak into O —
// O must equal the main-file-only cost, strictly less than the whole-session total.
func TestRecordUsage_OIsolatedFromSameModelSubagent(t *testing.T) {
	bin := buildRecordUsageCLI(t)
	specs := specsFilePath(t)
	dir := t.TempDir()
	exPath := filepath.Join(dir, "execution.json")
	transcript := filepath.Join(dir, "main.jsonl")
	subDir := filepath.Join(dir, "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, exPath, "{}")
	writeFile(t, transcript, oneTurnTranscript)
	writeFile(t, filepath.Join(subDir, "agent-a1.jsonl"),
		`{"type":"assistant","message":{"model":"claude-sonnet-5-20260101","usage":{"input_tokens":9000,"output_tokens":9000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
`)

	stdout, stderr, code := runRecordUsageCLI(t, bin, "record-usage", exPath, "--transcript", transcript, "--specs", specs, "--final", "--at", "2026-07-05T00:00:00Z")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr)
	}
	var ex execDoc
	if err := json.Unmarshal([]byte(stdout), &ex); err != nil {
		t.Fatalf("stdout not valid JSON: %v\n%s", err, stdout)
	}
	acc := ex.RunConfig.Accounting
	if acc == nil || acc.Orchestrator == nil {
		t.Fatal("O not persisted")
	}
	if acc.Orchestrator.CostUSD >= acc.CostUSD {
		t.Fatalf("O ($%v) not isolated from session total ($%v) — same-model subagent leaked into O", acc.Orchestrator.CostUSD, acc.CostUSD)
	}
	if acc.Orchestrator.CostUSD <= 0 {
		t.Fatalf("O.CostUSD = %v, want > 0", acc.Orchestrator.CostUSD)
	}
}

// TestUsageCommand_UnresolvedTranscript_StillHardFails is the regression guard for the separate
// `usage` command (readUsage): ACC1 changes ONLY record-usage's tolerance for an unresolved main
// transcript. `usage` must keep its prior die-on-missing behavior (exit 2), proving the two paths
// were not accidentally merged.
func TestUsageCommand_UnresolvedTranscript_StillHardFails(t *testing.T) {
	bin := buildRecordUsageCLI(t)
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.jsonl")

	_, stderr, code := runRecordUsageCLI(t, bin, "usage", missing)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (usage command must still hard-fail on an unresolved transcript); stderr=%s", code, stderr)
	}
}

// TestRecordUsageFinal_PopulatesSpecsAsOfAndBuildHelpersSHA pins M13.P2.T4 acceptance criteria 1+2
// at the real CLI boundary: the compiled binary, run against the real anthropic-specifications.json
// used elsewhere in this suite, must stamp both provenance fields with non-empty real values (not
// just "doesn't crash") — specs_as_of from the specs file's own `_as_of`, build_helpers_sha as a
// 64-hex-char sha256 of the binary that ran.
func TestRecordUsageFinal_PopulatesSpecsAsOfAndBuildHelpersSHA(t *testing.T) {
	bin := buildRecordUsageCLI(t)
	specs := specsFilePath(t)
	specsRaw, err := os.ReadFile(specs)
	if err != nil {
		t.Fatal(err)
	}
	var specsDoc struct {
		AsOf string `json:"_as_of"`
	}
	if err := json.Unmarshal(specsRaw, &specsDoc); err != nil {
		t.Fatal(err)
	}
	if specsDoc.AsOf == "" {
		t.Skip("anthropic-specifications.json has no _as_of key in this checkout; nothing to pin against")
	}

	dir := t.TempDir()
	exPath := filepath.Join(dir, "execution.json")
	transcript := filepath.Join(dir, "main.jsonl")
	writeFile(t, exPath, "{}")
	writeFile(t, transcript, oneTurnTranscript)

	stdout, stderr, code := runRecordUsageCLI(t, bin, "record-usage", exPath, "--transcript", transcript, "--specs", specs, "--final", "--at", "2026-07-05T00:00:00Z")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr)
	}
	var ex execDoc
	if err := json.Unmarshal([]byte(stdout), &ex); err != nil {
		t.Fatalf("record-usage stdout not valid JSON: %v\n%s", err, stdout)
	}
	acc := ex.RunConfig.Accounting
	if acc == nil {
		t.Fatal("run_config.accounting missing")
	}
	if acc.SpecsAsOf != specsDoc.AsOf {
		t.Errorf("specs_as_of = %q, want %q (from %s)", acc.SpecsAsOf, specsDoc.AsOf, specs)
	}
	if len(acc.BuildHelpersSHA) != 64 {
		t.Errorf("build_helpers_sha = %q, want a 64-hex-char sha256 digest", acc.BuildHelpersSHA)
	}
	for _, c := range acc.BuildHelpersSHA {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			t.Fatalf("build_helpers_sha %q is not lowercase-hex", acc.BuildHelpersSHA)
		}
	}

	// Round-trip through the real execution.json file on disk, not just stdout — this is what
	// the orchestrator skill actually does (`record-usage ... > tmp && mv tmp execution.json`),
	// so it's the seam acceptance criterion 3 names.
	writeFile(t, exPath, stdout)
	raw, err := os.ReadFile(exPath)
	if err != nil {
		t.Fatal(err)
	}
	var reread execDoc
	if err := json.Unmarshal(raw, &reread); err != nil {
		t.Fatalf("execution.json on disk failed to round-trip: %v\n%s", err, raw)
	}
	if reread.RunConfig.Accounting == nil {
		t.Fatal("round-tripped execution.json dropped run_config.accounting")
	}
	if reread.RunConfig.Accounting.SpecsAsOf != specsDoc.AsOf {
		t.Errorf("round-tripped specs_as_of = %q, want %q", reread.RunConfig.Accounting.SpecsAsOf, specsDoc.AsOf)
	}
	if reread.RunConfig.Accounting.BuildHelpersSHA != acc.BuildHelpersSHA {
		t.Errorf("round-tripped build_helpers_sha = %q, want %q", reread.RunConfig.Accounting.BuildHelpersSHA, acc.BuildHelpersSHA)
	}
}

// TestRecordUsageFinal_MissingSpecsFile_LeavesSpecsAsOfEmptyButSucceeds is the adversarial edge
// case for criterion 2's "best-effort" contract: a --specs path that does not resolve must not
// crash record-usage (accounting never blocks a run over a provenance field) and must leave
// specs_as_of empty rather than fabricating a value, while build_helpers_sha still populates.
func TestRecordUsageFinal_MissingSpecsFile_LeavesSpecsAsOfEmptyButSucceeds(t *testing.T) {
	bin := buildRecordUsageCLI(t)
	dir := t.TempDir()
	exPath := filepath.Join(dir, "execution.json")
	transcript := filepath.Join(dir, "main.jsonl")
	missingSpecs := filepath.Join(dir, "does-not-exist-specs.json")
	writeFile(t, exPath, "{}")
	writeFile(t, transcript, oneTurnTranscript)

	stdout, stderr, code := runRecordUsageCLI(t, bin, "record-usage", exPath, "--transcript", transcript, "--specs", missingSpecs, "--final", "--at", "2026-07-05T00:00:00Z")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (missing specs file is best-effort, non-fatal); stderr=%s", code, stderr)
	}
	var ex execDoc
	if err := json.Unmarshal([]byte(stdout), &ex); err != nil {
		t.Fatalf("stdout not valid JSON: %v\n%s", err, stdout)
	}
	acc := ex.RunConfig.Accounting
	if acc == nil {
		t.Fatal("run_config.accounting missing")
	}
	if acc.SpecsAsOf != "" {
		t.Errorf("specs_as_of = %q, want \"\" when the specs file is unresolvable", acc.SpecsAsOf)
	}
	if len(acc.BuildHelpersSHA) != 64 {
		t.Errorf("build_helpers_sha = %q, want a 64-hex-char sha256 digest even when specs is missing", acc.BuildHelpersSHA)
	}
}

// ---- minimal decode shapes (avoid importing package bh's unexported test helpers from package main) ----

type execDoc struct {
	RunConfig struct {
		Accounting *struct {
			CostStatus      string  `json:"cost_status"`
			CostUSD         float64 `json:"cost_usd"`
			SpecsAsOf       string  `json:"specs_as_of"`
			BuildHelpersSHA string  `json:"build_helpers_sha"`
			Orchestrator    *struct {
				CostUSD float64 `json:"cost_usd"`
			} `json:"orchestrator"`
		} `json:"accounting"`
	} `json:"run_config"`
	Log []string `json:"log"`
}

// normalizeExecJSON strips the ever-appended log array (which legitimately grows on every run) so
// idempotency comparisons focus on the accounting state itself.
func normalizeExecJSON(t *testing.T, raw string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, raw)
	}
	delete(m, "log")
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
