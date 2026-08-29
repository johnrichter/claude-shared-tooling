package toolchain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file unit-tests shell.go's pure functions — the parsers and the
// shebang sniffer — and the adapter's dispatch table (Tool/Route/Command),
// none of which need a real shfmt, shellcheck, checkbashisms, semgrep or bats
// on PATH. shell_probe_test.go carries the real-tool, real-tree coverage;
// this file is what runs everywhere else, mirroring python_sanity_test.go's
// split between pure-parser tests and its own probe suite.

// TestShellAdapterRegistered checks the shell adapter self-registers at
// init, the same contract every other adapter documents for its own key.
func TestShellAdapterRegistered(t *testing.T) {
	if _, ok := lookup(LanguageShell); !ok {
		t.Fatalf("no adapter registered for %q", LanguageShell)
	}
}

// TestShellAdapterRouteIsInProcessForEveryCheck checks Route answers
// in-process for all five MATRIX checks (shellAdapter's own doc: every pair
// needs Target.Dir, which Command's signature can never see) and for build
// and vet too, since Route carries no error return and must still answer
// something for a check ResolveCheck already refuses before Run ever calls
// it.
func TestShellAdapterRouteIsInProcessForEveryCheck(t *testing.T) {
	a := shellAdapter{}
	for _, c := range []Check{CheckFormat, CheckLint, CheckSecurity, CheckTest, CheckBuild, CheckVet} {
		if got := a.Route(c); got != RouteInProcess {
			t.Errorf("Route(%s) = %q, want %q", c, got, RouteInProcess)
		}
	}
}

// TestShellAdapterToolNamesEachPair checks Tool answers the fixed label each
// of the five pairs carries into RunResult.Tool, and falls back to the bare
// language name for build/vet, which have no tool at all (OD3).
func TestShellAdapterToolNamesEachPair(t *testing.T) {
	a := shellAdapter{}
	cases := map[Check]string{
		CheckFormat:   shfmtTool,
		CheckLint:     shellLintResultTool,
		CheckSecurity: semgrepTool,
		CheckTest:     shellTestResultTool,
		CheckBuild:    LanguageShell,
		CheckVet:      LanguageShell,
	}
	for check, want := range cases {
		if got := a.Tool(check); got != want {
			t.Errorf("Tool(%s) = %q, want %q", check, got, want)
		}
	}
}

// TestShellAdapterCommandAlwaysUnsupported checks Command answers
// ErrUnsupportedCheck for every check — every shell pair routes in-process,
// so Run's subprocess path never calls it, but the Adapter interface still
// requires an implementation.
func TestShellAdapterCommandAlwaysUnsupported(t *testing.T) {
	a := shellAdapter{}
	for _, c := range []Check{CheckFormat, CheckLint, CheckSecurity, CheckTest, CheckBuild, CheckVet} {
		_, err := a.Command(c)
		if !errors.Is(err, ErrUnsupportedCheck) {
			t.Errorf("Command(%s): errors.Is(err, ErrUnsupportedCheck) = false; err = %v", c, err)
		}
	}
}

// TestShellAdapterParseIsUnreachableNoop checks Parse — never called on the
// in-process route — returns a harmless zero value rather than panicking if
// some caller reaches it anyway.
func TestShellAdapterParseIsUnreachableNoop(t *testing.T) {
	diags, err := shellAdapter{}.Parse(0, nil, nil)
	if diags != nil || err != nil {
		t.Errorf("Parse = %+v, %v; want nil, nil", diags, err)
	}
}

// TestShellAdapterRunInProcessRejectsBuildAndVet checks build and vet fall
// to RunInProcess's default case with ErrUnsupportedCheck — the adapter's
// own defense in depth for a caller that bypasses ResolveCheck (AC2).
func TestShellAdapterRunInProcessRejectsBuildAndVet(t *testing.T) {
	a := shellAdapter{}
	for _, c := range []Check{CheckBuild, CheckVet} {
		_, err := a.RunInProcess(context.Background(), Target{Check: c, Dir: t.TempDir()})
		if !errors.Is(err, ErrUnsupportedCheck) {
			t.Errorf("RunInProcess(%s): errors.Is(err, ErrUnsupportedCheck) = false; err = %v", c, err)
		}
	}
}

// TestShellAdapterRunTestRejectsUnrecognizedKind checks the test pair's
// subcommand dispatch (runTest) rejects a target.Test outside
// TestUnit/TestE2E — including the zero value and benchmark, which MATRIX
// names for Rust alone.
func TestShellAdapterRunTestRejectsUnrecognizedKind(t *testing.T) {
	a := shellAdapter{}
	for _, kind := range []TestKind{"", TestBenchmark, TestKind("bogus")} {
		_, err := a.RunInProcess(context.Background(), Target{Check: CheckTest, Test: kind, Dir: t.TempDir()})
		if !errors.Is(err, ErrUnsupportedCheck) {
			t.Errorf("RunInProcess(test, kind=%q): errors.Is(err, ErrUnsupportedCheck) = false; err = %v", kind, err)
		}
	}
}

// TestParseShfmtListOnePerLine checks parseShfmtList turns each non-blank
// line of shfmt -l's output into one error diagnostic naming that path, and
// that blank lines (including a trailing newline) contribute nothing.
func TestParseShfmtListOnePerLine(t *testing.T) {
	out := []byte("bin/a.sh\nbin/b.sh\n\n")
	diags := parseShfmtList(out)
	if len(diags) != 2 {
		t.Fatalf("parseShfmtList = %+v, want 2 diagnostics", diags)
	}
	if diags[0].File != "bin/a.sh" || diags[0].Severity != SeverityError {
		t.Errorf("diags[0] = %+v, want File bin/a.sh, severity error", diags[0])
	}
	if diags[1].File != "bin/b.sh" {
		t.Errorf("diags[1] = %+v, want File bin/b.sh", diags[1])
	}
}

// TestParseShfmtListEmptyIsClean checks empty and whitespace-only output —
// shfmt -l's own shape for a fully formatted tree — parses to no
// diagnostics.
func TestParseShfmtListEmptyIsClean(t *testing.T) {
	for _, out := range [][]byte{nil, []byte(""), []byte("\n"), []byte("  \n")} {
		if diags := parseShfmtList(out); len(diags) != 0 {
			t.Errorf("parseShfmtList(%q) = %+v, want no diagnostics", out, diags)
		}
	}
}

// TestParseShellcheckJSON1SeverityAndCode checks error/warning levels
// classify as errors, info/style classify as warnings, and each diagnostic
// carries its SC code and file/line.
func TestParseShellcheckJSON1SeverityAndCode(t *testing.T) {
	report := `{"comments":[
		{"file":"bin/a.sh","line":3,"level":"error","code":2086,"message":"double quote to prevent globbing"},
		{"file":"bin/b.sh","line":7,"level":"style","code":2006,"message":"use $(...) instead of legacy backticks"}
	]}`
	diags := parseShellcheckJSON1([]byte(report))
	if len(diags) != 2 {
		t.Fatalf("parseShellcheckJSON1 = %+v, want 2 diagnostics", diags)
	}
	if diags[0].Code != "SC2086" || diags[0].Severity != SeverityError || diags[0].File != "bin/a.sh" || diags[0].Line != 3 {
		t.Errorf("diags[0] = %+v, want SC2086/error/bin/a.sh:3", diags[0])
	}
	if diags[1].Code != "SC2006" || diags[1].Severity != SeverityWarning {
		t.Errorf("diags[1] = %+v, want SC2006/warning", diags[1])
	}
	if !strings.Contains(diags[0].Message, shellcheckTool) {
		t.Errorf("diags[0].Message = %q, want it to name %s", diags[0].Message, shellcheckTool)
	}
}

// TestParseShellcheckJSON1MalformedReturnsNil checks unparseable JSON parses
// to nil rather than erroring — the caller's exit-code fallback covers it.
func TestParseShellcheckJSON1MalformedReturnsNil(t *testing.T) {
	if diags := parseShellcheckJSON1([]byte("not json")); diags != nil {
		t.Errorf("parseShellcheckJSON1(malformed) = %+v, want nil", diags)
	}
}

// TestParseCheckbashismsCapturesFileLineReason checks the finding-header
// regex pulls the file, line and reason out of checkbashisms's own output
// shape, from both stdout and stderr, and ignores a non-matching line (e.g.
// the offending-line echo checkbashisms prints under each header).
func TestParseCheckbashismsCapturesFileLineReason(t *testing.T) {
	stdout := []byte("possible bashism in bin/a.sh line 4 (should be 'x = y'):\n  [ \"$x\" == \"y\" ]\n")
	stderr := []byte("possible bashism in bin/b.sh line 9 ('local' is undefined):\n")
	diags := parseCheckbashisms(stdout, stderr)
	if len(diags) != 2 {
		t.Fatalf("parseCheckbashisms = %+v, want 2 diagnostics", diags)
	}
	if diags[0].File != "bin/a.sh" || diags[0].Line != 4 || diags[0].Severity != SeverityError {
		t.Errorf("diags[0] = %+v, want bin/a.sh:4, error", diags[0])
	}
	if !strings.Contains(diags[0].Message, "should be") {
		t.Errorf("diags[0].Message = %q, want it to carry the reason", diags[0].Message)
	}
	if diags[1].File != "bin/b.sh" || diags[1].Line != 9 {
		t.Errorf("diags[1] = %+v, want bin/b.sh:9", diags[1])
	}
}

// TestParseCheckbashismsCleanIsEmpty checks output with no finding header
// (checkbashisms's own clean-run shape prints nothing at all) parses to no
// diagnostics.
func TestParseCheckbashismsCleanIsEmpty(t *testing.T) {
	if diags := parseCheckbashisms(nil, nil); len(diags) != 0 {
		t.Errorf("parseCheckbashisms(clean) = %+v, want no diagnostics", diags)
	}
}

// TestParseSemgrepJSONSeverityAndCheckID checks ERROR/WARNING results
// classify as errors, INFO classifies as a warning, and each diagnostic
// carries its rule ID as Code.
func TestParseSemgrepJSONSeverityAndCheckID(t *testing.T) {
	report := `{"results":[
		{"check_id":"bash.curl.security.curl-eval","path":"bin/a.sh","start":{"line":2},"extra":{"message":"piping curl to a shell","severity":"ERROR"}},
		{"check_id":"bash.lint.correctness.useless-cat","path":"bin/b.sh","start":{"line":5},"extra":{"message":"useless use of cat","severity":"INFO"}}
	]}`
	diags := parseSemgrepJSON([]byte(report))
	if len(diags) != 2 {
		t.Fatalf("parseSemgrepJSON = %+v, want 2 diagnostics", diags)
	}
	if diags[0].Code != "bash.curl.security.curl-eval" || diags[0].Severity != SeverityError || diags[0].File != "bin/a.sh" || diags[0].Line != 2 {
		t.Errorf("diags[0] = %+v, want the curl-eval rule at bin/a.sh:2, error", diags[0])
	}
	if diags[1].Severity != SeverityWarning {
		t.Errorf("diags[1] = %+v, want warning for an INFO result", diags[1])
	}
}

// TestParseSemgrepJSONMalformedReturnsNil checks unparseable JSON parses to
// nil, mirroring parseShellcheckJSON1's own safe-degradation.
func TestParseSemgrepJSONMalformedReturnsNil(t *testing.T) {
	if diags := parseSemgrepJSON([]byte("{")); diags != nil {
		t.Errorf("parseSemgrepJSON(malformed) = %+v, want nil", diags)
	}
}

// TestParseBatsFailuresRecognizesNotOkLines checks bats's "not ok <N> <name>"
// lines turn into one error diagnostic per failure, from both stdout and
// stderr, tagged with the tool name the caller passes.
func TestParseBatsFailuresRecognizesNotOkLines(t *testing.T) {
	stdout := []byte("ok 1 first test\nnot ok 2 addition fails\n")
	diags := parseBatsFailures(stdout, nil, batsTool)
	if len(diags) != 1 {
		t.Fatalf("parseBatsFailures = %+v, want 1 diagnostic", diags)
	}
	if diags[0].Severity != SeverityError || !strings.Contains(diags[0].Message, "addition fails") {
		t.Errorf("diags[0] = %+v, want an error naming addition fails", diags[0])
	}
	if !strings.Contains(diags[0].Message, batsTool) {
		t.Errorf("diags[0].Message = %q, want it to name %s", diags[0].Message, batsTool)
	}
}

// TestParseBatsFailuresAllPassingIsEmpty checks a run with no "not ok" line
// parses to no diagnostics.
func TestParseBatsFailuresAllPassingIsEmpty(t *testing.T) {
	if diags := parseBatsFailures([]byte("ok 1 first test\nok 2 second test\n"), nil, batsTool); len(diags) != 0 {
		t.Errorf("parseBatsFailures(all passing) = %+v, want no diagnostics", diags)
	}
}

// writeShellTestFile writes content to a fresh file under t.TempDir() and
// returns its path.
func writeShellTestFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// TestIsShellShebangRecognizesEveryF49Shape checks every extensionless
// shebang shape F49 found in the fleet: a bare interpreter path, a
// /usr/bin/env indirection, and each of the four POSIX-family interpreters
// OD54's discovery rule names.
func TestIsShellShebangRecognizesEveryF49Shape(t *testing.T) {
	shapes := []string{
		"#!/bin/sh\necho hi\n",
		"#!/bin/bash\necho hi\n",
		"#!/usr/bin/env bash\necho hi\n",
		"#!/usr/bin/env sh\necho hi\n",
		"#!/bin/dash\necho hi\n",
		"#!/bin/ksh\necho hi\n",
	}
	for _, shape := range shapes {
		path := writeShellTestFile(t, "script", shape)
		if !isShellShebang(path) {
			t.Errorf("isShellShebang(%q) = false, want true", shape)
		}
	}
}

// TestIsShellShebangRejectsNonShellAndMissing checks a non-shell shebang, a
// file with no shebang at all, and a nonexistent path all report false
// rather than a false positive or a panic.
func TestIsShellShebangRejectsNonShellAndMissing(t *testing.T) {
	pythonScript := writeShellTestFile(t, "script", "#!/usr/bin/env python3\nprint('hi')\n")
	if isShellShebang(pythonScript) {
		t.Errorf("isShellShebang(python shebang) = true, want false")
	}
	plainText := writeShellTestFile(t, "notes.txt", "just some notes\n")
	if isShellShebang(plainText) {
		t.Errorf("isShellShebang(no shebang) = true, want false")
	}
	if isShellShebang(filepath.Join(t.TempDir(), "does-not-exist")) {
		t.Errorf("isShellShebang(missing file) = true, want false")
	}
}

// TestDiscoverShellFilesSkipsDotGit checks the walk excludes .git entirely,
// even though .githooks/ (a sibling, not a match) is walked like any other
// directory (OD54).
func TestDiscoverShellFilesSkipsDotGit(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("bin/a.sh", "#!/bin/sh\ntrue\n")
	write(".git/hooks/pre-commit.sample", "#!/bin/sh\ntrue\n")
	write(".githooks/pre-commit", "#!/bin/sh\ntrue\n")

	files, err := discoverShellFiles(dir)
	if err != nil {
		t.Fatalf("discoverShellFiles: %v", err)
	}
	for _, f := range files {
		if strings.Contains(f, string(filepath.Separator)+".git"+string(filepath.Separator)) {
			t.Errorf("discoverShellFiles included %s, want .git/ skipped entirely", f)
		}
	}
	found := map[string]bool{}
	for _, f := range files {
		found[filepath.ToSlash(strings.TrimPrefix(f, dir+string(filepath.Separator)))] = true
	}
	if !found["bin/a.sh"] || !found[".githooks/pre-commit"] {
		t.Errorf("discoverShellFiles = %v, want bin/a.sh and .githooks/pre-commit both in scope", files)
	}
}
