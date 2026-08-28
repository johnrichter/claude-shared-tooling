package toolchain

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// This file drives Run() against a real, temporary uv-managed Python project
// for every MATRIX pair pythonAdapter declares, spawning the real tools (uv,
// ruff, mypy, bandit, pytest) rather than exercising only the pure parser
// functions in python.go. Every sub-test skips (never fails) a tool absent
// from PATH — or present only as an unprovisioned version-manager shim — so
// this suite degrades gracefully on a runner missing one binary instead of
// reporting a false negative, mirroring rust_probe_test.go and
// golang_e2e_probe_test.go's own convention. A present tool is never mocked.

// requirePythonTool skips the calling test if tool cannot actually be run:
// absent from PATH, or resolvable only as a shim with no version provisioned
// (the same two-part check golang_e2e_probe_test.go's requireTool performs),
// so an environment gap is never mistaken for a defect in the adapter.
func requirePythonTool(t *testing.T, tool string) {
	t.Helper()
	if _, err := exec.LookPath(tool); err != nil {
		t.Skipf("%s not on PATH; skipping (not a defect in the adapter)", tool)
	}
	out, _ := exec.Command(tool, "--version").CombinedOutput()
	if bytes.Contains(out, []byte("No version is set for shim")) {
		t.Skipf("%s present on PATH as an unprovisioned shim, not runnable here; skipping (not a defect in the adapter)", tool)
	}
}

// pythonProbeDevDeps names every dev dependency the probe project's own
// pyproject.toml declares: pytest is the runner both test pairs share,
// pytest-cov is what runUnitTest's `--cov` flags need present in the
// project's own dependency group (never a toolchain pin, per python.go's own
// doc), and pytest-playwright supplies the `page` fixture an e2e-marked test
// imports — proving the e2e dispatch drives Playwright's own test
// integration rather than a bespoke browser harness this adapter spawns
// itself.
const pythonProbePyprojectTemplate = `[project]
name = "pyprobe"
version = "0.1.0"
requires-python = ">=3.11"

[dependency-groups]
dev = ["pytest", "pytest-cov", "pytest-playwright"]

[tool.pytest.ini_options]
markers = ["e2e: end-to-end tests"]

[build-system]
requires = ["hatchling"]
build-backend = "hatchling.build"
`

// writePythonProbeProject lays out a minimal uv project under a fresh
// t.TempDir(): a src/pyprobe package holding initBody, a unit test
// (tests/test_unit.py, the complement runUnitTest's `-m "not e2e"` selects),
// and an e2e test (tests/test_e2e.py, marked e2e and taking pytest-playwright's
// own `page` fixture, skipping its own body immediately since this sandbox has
// no network to launch a real browser — the dispatch through `uv run pytest -m
// e2e` is what this probe exercises, not a live Chromium session). It then
// runs `ruff format` once to seed every file at a formatter-clean baseline, the
// same seeding role cargo fmt plays in writeRustProbeCrate, so format's own
// clean-input counter-probe is not confounded by import-order or
// whitespace noise from this file's own authoring.
func writePythonProbeProject(t *testing.T, initBody string) string {
	t.Helper()
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv not on PATH")
	}
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

	write("pyproject.toml", pythonProbePyprojectTemplate)
	write("src/pyprobe/__init__.py", initBody)
	write("tests/test_unit.py", "from pyprobe import add\n\n\ndef test_add():\n    assert add(2, 3) == 5\n")
	write("tests/test_e2e.py", "import pytest\n\n\n@pytest.mark.e2e\ndef test_smoke(page):\n    pytest.skip(\"no network for a real browser session in this probe sandbox\")\n")

	sync := exec.Command("uv", "sync", "--all-groups")
	sync.Dir = dir
	if out, err := sync.CombinedOutput(); err != nil {
		t.Skipf("uv sync (no network for the dev dependency group?): %v\n%s", err, out)
	}
	fmtCmd := exec.Command("ruff", "format", ".")
	fmtCmd.Dir = dir
	if out, err := fmtCmd.CombinedOutput(); err == nil || exec.Command("ruff", "--version").Run() != nil {
		_ = out // ruff absent is fine here; the per-check requirePythonTool below still gates format/lint themselves
	}
	return dir
}

const pythonProbeCleanInit = "def add(a: int, b: int) -> int:\n    return a + b\n"

// pythonProbePairs enumerates the seven Python MATRIX pairs this suite
// drives, mirroring committedMatrix's seven Python entries, and the tool set
// each needs actually present to run for real (never mocked).
var pythonProbePairs = []struct {
	name  string
	check Check
	test  TestKind
	tools []string
}{
	{"build", CheckBuild, "", []string{"uv"}},
	{"format", CheckFormat, "", []string{"ruff"}},
	{"lint", CheckLint, "", []string{"ruff"}},
	{"vet", CheckVet, "", []string{"mypy"}},
	{"security", CheckSecurity, "", []string{"bandit"}},
	{"test unit", CheckTest, TestUnit, []string{"uv"}},
	{"test e2e", CheckTest, TestE2E, []string{"uv"}},
}

// TestE2EPythonAdapterSevenPairsCleanInputNeverExit80 runs every declared
// Python check against a real clean project and asserts none resolves to
// EXIT 80 (unsupported) and none resolves to EXIT 90 (internal) — the
// concrete evidence for AC1: Python dispatches its seven MATRIX pairs with
// none returning EXIT 80 and none returning EXIT 90.
func TestE2EPythonAdapterSevenPairsCleanInputNeverExit80(t *testing.T) {
	dir := writePythonProbeProject(t, pythonProbeCleanInit)
	logDir := t.TempDir()

	if len(pythonProbePairs) != 7 {
		t.Fatalf("pythonProbePairs lists %d pairs, want 7", len(pythonProbePairs))
	}

	for _, p := range pythonProbePairs {
		t.Run(p.name, func(t *testing.T) {
			for _, tool := range p.tools {
				requirePythonTool(t, tool)
			}
			res, err := Run(context.Background(), Target{Language: LanguagePython, Check: p.check, Test: p.test, Dir: dir}, Options{LogDir: logDir})
			if err != nil {
				t.Fatalf("Run(%s): unexpected infrastructure error: %v", p.name, err)
			}
			if res.Status.ExitCode() == ExitUnsupported {
				t.Fatalf("Run(%s) resolved to EXIT %d (unsupported) — AC1 violated: %+v", p.name, ExitUnsupported, res)
			}
			if res.Status.ExitCode() == ExitRunFailed {
				t.Fatalf("Run(%s) resolved to EXIT %d (internal) — AC1 violated, a defect in the binary itself: %+v", p.name, ExitRunFailed, res)
			}
			if res.Tool == "" {
				t.Errorf("Run(%s): RunResult.Tool is empty, want a named tool", p.name)
			}
		})
	}
}

// TestE2EPythonAdapterSevenPairsCleanInputExitZero is the test-strategy's
// clean-input counter-probe per pair: on a genuinely clean project, each
// check should classify as success (EXIT 0).
func TestE2EPythonAdapterSevenPairsCleanInputExitZero(t *testing.T) {
	dir := writePythonProbeProject(t, pythonProbeCleanInit)
	logDir := t.TempDir()

	for _, p := range pythonProbePairs {
		t.Run(p.name, func(t *testing.T) {
			for _, tool := range p.tools {
				requirePythonTool(t, tool)
			}
			res, err := Run(context.Background(), Target{Language: LanguagePython, Check: p.check, Test: p.test, Dir: dir}, Options{LogDir: logDir})
			if err != nil {
				t.Fatalf("Run(%s): unexpected infrastructure error: %v", p.name, err)
			}
			if res.Status.ExitCode() != ExitSuccess {
				t.Errorf("Run(%s) on clean input = status %s (exit %d), want success (exit 0); diagnostics=%+v", p.name, res.Status, res.Status.ExitCode(), res.Diagnostics)
			}
		})
	}
}

// TestE2EPythonAdapterVetCatchesTypeDefectRuffMisses is the ground-truth
// probe for vet resolving to mypy specifically (OD29): it introduces a type
// mismatch ruff's own lint/format rules never inspect (ruff checks style and
// import hygiene, not static types), so lint and format both pass clean while
// vet alone fails — proving vet's own tool catches what the other two Python
// checks do not.
func TestE2EPythonAdapterVetCatchesTypeDefectRuffMisses(t *testing.T) {
	requirePythonTool(t, "mypy")
	requirePythonTool(t, "ruff")
	const badTypeInit = "def add(a: int, b: int) -> int:\n    return \"not an int\"\n"
	dir := writePythonProbeProject(t, badTypeInit)
	logDir := t.TempDir()

	lintRes, err := Run(context.Background(), Target{Language: LanguagePython, Check: CheckLint, Dir: dir}, Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("Run(lint): unexpected infrastructure error: %v", err)
	}
	if lintRes.Status.ExitCode() != ExitSuccess {
		t.Fatalf("Run(lint) on a type-only defect = status %s (exit %d), want success (ruff never inspects types); diagnostics=%+v",
			lintRes.Status, lintRes.Status.ExitCode(), lintRes.Diagnostics)
	}

	vetRes, err := Run(context.Background(), Target{Language: LanguagePython, Check: CheckVet, Dir: dir}, Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("Run(vet): unexpected infrastructure error: %v", err)
	}
	if vetRes.Status.ExitCode() != ExitCheckFailed {
		t.Fatalf("Run(vet) on a type mismatch = status %s (exit %d), want gate_negative (exit %d); diagnostics=%+v",
			vetRes.Status, vetRes.Status.ExitCode(), ExitCheckFailed, vetRes.Diagnostics)
	}
	if vetRes.Tool != "mypy" {
		t.Errorf("Run(vet).Tool = %q, want %q — vet resolves to mypy, never ruff", vetRes.Tool, "mypy")
	}
}

// TestE2EPythonAdapterSecurityFindsBanditIssueAndAttributesIt runs security
// against a project carrying a bandit-triggering pattern (a fixed-permission
// insecure write, B108-adjacent hardcoded-tmp pattern via B108) and asserts
// the finding is attributed to bandit and carries its rule ID — AC3's
// "security invokes bandit" attributed the same way Go's gosec and Rust's
// cargo-audit findings are attributed to their own tool.
func TestE2EPythonAdapterSecurityFindsBanditIssueAndAttributesIt(t *testing.T) {
	requirePythonTool(t, "bandit")
	const insecureInit = "import subprocess\n\n\ndef add(a: int, b: int) -> int:\n    subprocess.call(\"echo hi\", shell=True)\n    return a + b\n"
	dir := writePythonProbeProject(t, insecureInit)
	logDir := t.TempDir()

	res, err := Run(context.Background(), Target{Language: LanguagePython, Check: CheckSecurity, Dir: dir}, Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("Run(security): unexpected infrastructure error: %v", err)
	}
	if res.Status.ExitCode() != ExitCheckFailed {
		t.Fatalf("Run(security) on a shell=True subprocess call = status %s (exit %d), want gate_negative (exit %d); diagnostics=%+v",
			res.Status, res.Status.ExitCode(), ExitCheckFailed, res.Diagnostics)
	}
	foundBandit := false
	for _, d := range res.Diagnostics {
		if containsSubstring(d.Message, "bandit") && d.Code != "" {
			foundBandit = true
		}
	}
	if !foundBandit {
		t.Errorf("Run(security) diagnostics = %+v, want at least one bandit finding with a rule code", res.Diagnostics)
	}
}

// TestE2EPythonAdapterBuildProducesWheelAndSdist runs build against a clean
// project and asserts `uv build` lands both a wheel and a source distribution
// in dist/ — AC3's "build produces a wheel and a source distribution per
// MATRIX", not just one of the two.
func TestE2EPythonAdapterBuildProducesWheelAndSdist(t *testing.T) {
	requirePythonTool(t, "uv")
	dir := writePythonProbeProject(t, pythonProbeCleanInit)
	logDir := t.TempDir()

	res, err := Run(context.Background(), Target{Language: LanguagePython, Check: CheckBuild, Dir: dir}, Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("Run(build): unexpected infrastructure error: %v", err)
	}
	if res.Status.ExitCode() != ExitSuccess {
		t.Fatalf("Run(build) on a clean project = status %s (exit %d), want success; diagnostics=%+v", res.Status, res.Status.ExitCode(), res.Diagnostics)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "dist"))
	if err != nil {
		t.Fatalf("read dist/: %v", err)
	}
	var haveWheel, haveSdist bool
	for _, e := range entries {
		switch {
		case filepath.Ext(e.Name()) == ".whl":
			haveWheel = true
		case bytes.HasSuffix([]byte(e.Name()), []byte(".tar.gz")):
			haveSdist = true
		}
	}
	if !haveWheel {
		t.Errorf("dist/ = %v, want at least one .whl", entries)
	}
	if !haveSdist {
		t.Errorf("dist/ = %v, want at least one .tar.gz sdist", entries)
	}
}

// TestE2EPythonAdapterUnitTestProducesCoverageInSameRun runs the unit-test
// pair through the real `uv run pytest` invocation against a project with one
// failing test, asserting (a) the failure surfaces as an attributed
// diagnostic and (b) pythonCoverageFile lands in target.Dir from this one
// run — AC3's "test unit invokes the record's runner with coverage in the
// same run".
func TestE2EPythonAdapterUnitTestProducesCoverageInSameRun(t *testing.T) {
	requirePythonTool(t, "uv")
	dir := writePythonProbeProject(t, pythonProbeCleanInit)
	failingTest := filepath.Join(dir, "tests", "test_unit.py")
	if err := os.WriteFile(failingTest, []byte("from pyprobe import add\n\n\ndef test_add():\n    assert add(2, 3) == 6\n"), 0o644); err != nil {
		t.Fatalf("overwrite test_unit.py: %v", err)
	}
	logDir := t.TempDir()

	res, err := Run(context.Background(), Target{Language: LanguagePython, Check: CheckTest, Test: TestUnit, Dir: dir}, Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("Run(test unit): unexpected infrastructure error: %v", err)
	}
	if res.Status.ExitCode() != ExitCheckFailed {
		t.Fatalf("Run(test unit) on a failing test = status %s (exit %d), want gate_negative (exit %d); diagnostics=%+v",
			res.Status, res.Status.ExitCode(), ExitCheckFailed, res.Diagnostics)
	}
	foundFailure := false
	for _, d := range res.Diagnostics {
		if containsSubstring(d.Message, "test_add") {
			foundFailure = true
		}
	}
	if !foundFailure {
		t.Errorf("Run(test unit) diagnostics = %+v, want a diagnostic naming test_add", res.Diagnostics)
	}
	if _, err := os.Stat(filepath.Join(dir, pythonCoverageFile)); err != nil {
		t.Errorf("coverage file %s not written by test unit's own run: %v", pythonCoverageFile, err)
	}
}

// TestE2EPythonAdapterE2ETestDispatchesPlaywrightWithoutBrowserInstall runs
// the e2e-test pair against a project whose e2e-marked test imports
// pytest-playwright's own `page` fixture, and asserts (a) the check dispatches
// through pytest rather than EXIT 80/90, and (b) the adapter never shells out
// to `playwright install` anywhere in its own Command/RunInProcess path —
// AC3's "no browser install step is required on the Python leg" (OD61:
// Playwright's Python distribution ships its own Chromium). This probe's own
// e2e test skips its body (no network for a live browser session here), so
// the dispatch is exercised for real while the browser session itself is not
// — the same trade writeGoProbeModule's chromedp import makes for Go's e2e
// pair.
func TestE2EPythonAdapterE2ETestDispatchesPlaywrightWithoutBrowserInstall(t *testing.T) {
	requirePythonTool(t, "uv")
	a := pythonAdapter{}
	if a.Route(CheckTest) != RouteInProcess {
		t.Fatalf("Route(test) = %q, want %q", a.Route(CheckTest), RouteInProcess)
	}
	// Command is unreachable for CheckTest (Route sends it in-process); assert
	// that directly, since a subprocess-routed adapter would be the one place
	// a stray "playwright install" argv could hide.
	if _, err := a.Command(CheckTest); err == nil {
		t.Fatalf("Command(test) = no error, want ErrUnsupportedCheck — test never takes the subprocess route")
	}

	dir := writePythonProbeProject(t, pythonProbeCleanInit)
	logDir := t.TempDir()
	res, err := Run(context.Background(), Target{Language: LanguagePython, Check: CheckTest, Test: TestE2E, Dir: dir}, Options{LogDir: logDir})
	if err != nil {
		t.Fatalf("Run(test e2e): unexpected infrastructure error: %v", err)
	}
	if res.Status.ExitCode() == ExitUnsupported || res.Status.ExitCode() == ExitRunFailed {
		t.Fatalf("Run(test e2e) resolved to EXIT %d, want a resolved dispatch through pytest: %+v", res.Status.ExitCode(), res)
	}
	if res.Tool != "pytest" {
		t.Errorf("Run(test e2e).Tool = %q, want %q", res.Tool, "pytest")
	}
}
