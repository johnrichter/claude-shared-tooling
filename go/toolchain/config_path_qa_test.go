package toolchain

// This file verifies Target.ConfigPath and its threading into
// golangci-lint's, ruff's, mypy's and shellcheck's argv construction. It
// covers, per tool: (1) argv carries the caller-supplied path when set, (2)
// argv and discovery are unchanged from the no-field baseline when unset,
// (3) for mypy and shellcheck specifically, the supplied path wins over this
// package's own hardcoded constant. golangci-lint and shellcheck route
// in-process and build their argv inside runLint rather than through
// Command, so their cases spawn a fake binary that captures its own argv to
// a file — the only way to observe an in-process adapter's constructed
// command line without mocking exec.Command itself. ruff and mypy route
// through ConfigPathCommand, so their cases call it directly.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeArgvCaptureScript writes an executable at dir/name that appends its
// own argv (one arg per line, terminated by a blank line) to captureFile and
// prints stdoutBody to stdout before exiting 0 — enough for runLint's own
// Parse step to see a well-formed, empty report rather than falling back to
// a synthetic diagnostic that would obscure whether argv was even captured.
func writeArgvCaptureScript(t *testing.T, dir, name, captureFile, stdoutBody string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("argv-capture fixture is a POSIX shell script")
	}
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do printf '%s\\n' \"$a\" >> " + shellQuote(captureFile) + "; done\n" +
		"printf '\\n' >> " + shellQuote(captureFile) + "\n" +
		"cat <<'EOF'\n" + stdoutBody + "\nEOF\n"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
}

// shellQuote wraps s in single quotes for embedding in the generated fixture
// script, escaping any single quote s itself contains. Every path this file
// feeds it is a t.TempDir() path, never attacker-controlled, but the escape
// keeps the fixture correct regardless.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// readCapturedArgvBlocks reads captureFile and splits it into one []string
// per tool invocation, in call order — one blank-line-terminated block per
// writeArgvCaptureScript run.
func readCapturedArgvBlocks(t *testing.T, captureFile string) [][]string {
	t.Helper()
	data, err := os.ReadFile(captureFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read capture file: %v", err)
	}
	var blocks [][]string
	var cur []string
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			if cur != nil {
				blocks = append(blocks, cur)
				cur = nil
			}
			continue
		}
		cur = append(cur, line)
	}
	return blocks
}

// containsArg reports whether argv holds want at all.
func containsArg(argv []string, want string) bool {
	for _, a := range argv {
		if a == want {
			return true
		}
	}
	return false
}

// argAfter returns the element immediately following the first occurrence of
// flag in argv, or "" if flag is absent or is argv's last element.
func argAfter(argv []string, flag string) string {
	for i, a := range argv {
		if a == flag && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

// fakeToolPATH prepends dir (holding one or more fake executables) to PATH,
// keeping the rest of PATH behind it so every tool writeArgvCaptureScript
// did not fake — goimports for the Go lint pair, checkbashisms for the shell
// lint pair unless also faked — still resolves to the real binary.
func fakeToolPATH(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// --- golangci-lint (Go lint, in-process route) ---------------------------

// TestConfigPathGolangciLintArgvCarriesConfigWhenSet checks runLint (via
// RunInProcess) passes target.ConfigPath to golangci-lint as `--config
// <path>` when set.
func TestConfigPathGolangciLintArgvCarriesConfigWhenSet(t *testing.T) {
	fakeDir := t.TempDir()
	capture := filepath.Join(t.TempDir(), "capture.txt")
	writeArgvCaptureScript(t, fakeDir, "golangci-lint", capture, `{"Issues":[]}`)
	fakeToolPATH(t, fakeDir)
	if _, err := exec.LookPath("goimports"); err != nil {
		t.Skip("goimports not on PATH; needed for the second half of runLint")
	}

	target := Target{Language: LanguageGo, Check: CheckLint, Dir: t.TempDir(), ConfigPath: "/custom/golangci.yml"}
	a := NewGoAdapter()
	if _, err := a.RunInProcess(context.Background(), target); err != nil {
		t.Fatalf("RunInProcess(lint): %v", err)
	}
	blocks := readCapturedArgvBlocks(t, capture)
	if len(blocks) != 1 {
		t.Fatalf("golangci-lint invoked %d times, want 1", len(blocks))
	}
	if got := argAfter(blocks[0], "--config"); got != target.ConfigPath {
		t.Fatalf("golangci-lint argv = %v, want --config %q present", blocks[0], target.ConfigPath)
	}
}

// TestConfigPathGolangciLintArgvUnchangedWhenUnset checks the unset-field
// case: with target.ConfigPath empty, golangci-lint's argv carries no
// --config flag at all — the prior discovery behavior, preserved
// element-for-element rather than by inspection.
func TestConfigPathGolangciLintArgvUnchangedWhenUnset(t *testing.T) {
	fakeDir := t.TempDir()
	capture := filepath.Join(t.TempDir(), "capture.txt")
	writeArgvCaptureScript(t, fakeDir, "golangci-lint", capture, `{"Issues":[]}`)
	fakeToolPATH(t, fakeDir)
	if _, err := exec.LookPath("goimports"); err != nil {
		t.Skip("goimports not on PATH; needed for the second half of runLint")
	}

	target := Target{Language: LanguageGo, Check: CheckLint, Dir: t.TempDir()}
	a := NewGoAdapter()
	if _, err := a.RunInProcess(context.Background(), target); err != nil {
		t.Fatalf("RunInProcess(lint): %v", err)
	}
	blocks := readCapturedArgvBlocks(t, capture)
	if len(blocks) != 1 {
		t.Fatalf("golangci-lint invoked %d times, want 1", len(blocks))
	}
	if containsArg(blocks[0], "--config") {
		t.Fatalf("golangci-lint argv = %v, want no --config flag when ConfigPath is unset", blocks[0])
	}
	want := []string{"run", "--output.json.path", "stdout", "--output.text.path", "stderr"}
	if len(blocks[0]) != len(want) {
		t.Fatalf("golangci-lint argv = %v, want exactly %v (unchanged baseline)", blocks[0], want)
	}
	for i, w := range want {
		if blocks[0][i] != w {
			t.Fatalf("golangci-lint argv[%d] = %q, want %q (unchanged baseline)", i, blocks[0][i], w)
		}
	}
}

// --- shellcheck (shell lint, in-process route) ----------------------------

// TestConfigPathShellcheckArgvCarriesConfigWhenSetAndWinsOverConstant checks
// runLint (via RunInProcess) passes target.ConfigPath to shellcheck as
// `--rcfile <path>` when set, winning over the package's own
// shellcheckConfigPath constant.
func TestConfigPathShellcheckArgvCarriesConfigWhenSetAndWinsOverConstant(t *testing.T) {
	fakeDir := t.TempDir()
	capture := filepath.Join(t.TempDir(), "capture.txt")
	writeArgvCaptureScript(t, fakeDir, "shellcheck", capture, `{"comments":[]}`)
	writeArgvCaptureScript(t, fakeDir, "checkbashisms", filepath.Join(t.TempDir(), "cb-capture.txt"), "")
	fakeToolPATH(t, fakeDir)

	scriptDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(scriptDir, "a.sh"), []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatalf("write a.sh: %v", err)
	}
	customCfg := filepath.Join(t.TempDir(), "custom.shellcheckrc")
	if err := os.WriteFile(customCfg, []byte("disable=SC2148\n"), 0o644); err != nil {
		t.Fatalf("write custom config: %v", err)
	}

	target := Target{Language: LanguageShell, Check: CheckLint, Dir: scriptDir, ConfigPath: customCfg}
	a := shellAdapter{}
	if _, err := a.RunInProcess(context.Background(), target); err != nil {
		t.Fatalf("RunInProcess(lint): %v", err)
	}
	blocks := readCapturedArgvBlocks(t, capture)
	if len(blocks) != 1 {
		t.Fatalf("shellcheck invoked %d times, want 1", len(blocks))
	}
	if got := argAfter(blocks[0], "--rcfile"); got != customCfg {
		t.Fatalf("shellcheck argv = %v, want --rcfile %q (the caller-supplied path, not the language-tools constant)", blocks[0], customCfg)
	}
}

// TestConfigPathShellcheckArgvUnchangedWhenUnset checks the unset-field case:
// with target.ConfigPath empty, shellcheck's --rcfile still points at the
// language-tools constant file (shellcheckConfigPath), with content matching
// shellcheckDefaultConfig.
func TestConfigPathShellcheckArgvUnchangedWhenUnset(t *testing.T) {
	fakeDir := t.TempDir()
	capture := filepath.Join(t.TempDir(), "capture.txt")
	writeArgvCaptureScript(t, fakeDir, "shellcheck", capture, `{"comments":[]}`)
	writeArgvCaptureScript(t, fakeDir, "checkbashisms", filepath.Join(t.TempDir(), "cb-capture.txt"), "")
	fakeToolPATH(t, fakeDir)

	scriptDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(scriptDir, "a.sh"), []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatalf("write a.sh: %v", err)
	}

	target := Target{Language: LanguageShell, Check: CheckLint, Dir: scriptDir}
	a := shellAdapter{}
	if _, err := a.RunInProcess(context.Background(), target); err != nil {
		t.Fatalf("RunInProcess(lint): %v", err)
	}
	blocks := readCapturedArgvBlocks(t, capture)
	if len(blocks) != 1 {
		t.Fatalf("shellcheck invoked %d times, want 1", len(blocks))
	}
	rcfile := argAfter(blocks[0], "--rcfile")
	if rcfile == "" {
		t.Fatalf("shellcheck argv = %v, want an --rcfile flag even when ConfigPath is unset", blocks[0])
	}
	if filepath.Base(rcfile) != shellcheckConfigBase {
		t.Fatalf("--rcfile = %q, want the language-tools constant file %q", rcfile, shellcheckConfigBase)
	}
	got, err := os.ReadFile(rcfile)
	if err != nil {
		t.Fatalf("read resolved rcfile %s: %v", rcfile, err)
	}
	if string(got) != shellcheckDefaultConfig {
		t.Fatalf("resolved rcfile content = %q, want %q (unchanged baseline)", got, shellcheckDefaultConfig)
	}
}

// --- ruff (Python lint/format, subprocess route via ConfigPathCommand) ---

// TestConfigPathRuffLintArgvCarriesConfigWhenSet checks
// CommandWithConfigPath adds `--config <path>` to ruff's lint argv when set.
func TestConfigPathRuffLintArgvCarriesConfigWhenSet(t *testing.T) {
	a := pythonAdapter{}
	argv, err := a.CommandWithConfigPath(CheckLint, "/custom/ruff.toml")
	if err != nil {
		t.Fatalf("CommandWithConfigPath(lint): %v", err)
	}
	if got := argAfter(argv, "--config"); got != "/custom/ruff.toml" {
		t.Fatalf("ruff lint argv = %v, want --config /custom/ruff.toml", argv)
	}
}

// TestConfigPathRuffFormatArgvCarriesConfigWhenSet checks the same for
// ruff's format check, and that --check (never rewriting the tree) survives
// the addition.
func TestConfigPathRuffFormatArgvCarriesConfigWhenSet(t *testing.T) {
	a := pythonAdapter{}
	argv, err := a.CommandWithConfigPath(CheckFormat, "/custom/ruff.toml")
	if err != nil {
		t.Fatalf("CommandWithConfigPath(format): %v", err)
	}
	if !containsArg(argv, "--check") {
		t.Fatalf("ruff format argv = %v, want --check preserved", argv)
	}
	if got := argAfter(argv, "--config"); got != "/custom/ruff.toml" {
		t.Fatalf("ruff format argv = %v, want --config /custom/ruff.toml", argv)
	}
}

// TestConfigPathRuffArgvUnchangedWhenUnset checks CommandWithConfigPath with
// an empty path answers exactly what Command does — ruff's own upward
// discovery, untouched — for both lint and format.
func TestConfigPathRuffArgvUnchangedWhenUnset(t *testing.T) {
	a := pythonAdapter{}
	for _, check := range []Check{CheckLint, CheckFormat} {
		withPath, err := a.CommandWithConfigPath(check, "")
		if err != nil {
			t.Fatalf("CommandWithConfigPath(%s, \"\"): %v", check, err)
		}
		plain, err := a.Command(check)
		if err != nil {
			t.Fatalf("Command(%s): %v", check, err)
		}
		if !equalStrings(withPath, plain) {
			t.Fatalf("%s: CommandWithConfigPath(\"\") = %v, want identical to Command() = %v", check, withPath, plain)
		}
		if containsArg(withPath, "--config") {
			t.Fatalf("%s: argv = %v, want no --config flag when unset", check, withPath)
		}
	}
}

// --- mypy (Python vet, subprocess route via ConfigPathCommand) -----------

// TestConfigPathMypyArgvCarriesConfigWhenSetAndWinsOverConstant checks
// CommandWithConfigPath points mypy's --config-file at the caller-supplied
// path when set, rather than the package's own mypyConfigPath constant file.
func TestConfigPathMypyArgvCarriesConfigWhenSetAndWinsOverConstant(t *testing.T) {
	a := pythonAdapter{}
	argv, err := a.CommandWithConfigPath(CheckVet, "/custom/mypy.ini")
	if err != nil {
		t.Fatalf("CommandWithConfigPath(vet): %v", err)
	}
	if got := argAfter(argv, "--config-file"); got != "/custom/mypy.ini" {
		t.Fatalf("mypy vet argv = %v, want --config-file /custom/mypy.ini (not the language-tools constant)", argv)
	}
}

// TestConfigPathMypyArgvUnchangedWhenUnset checks CommandWithConfigPath with
// an empty path answers exactly what Command does — the language-tools
// constant file mypyConfigPath writes, untouched.
func TestConfigPathMypyArgvUnchangedWhenUnset(t *testing.T) {
	a := pythonAdapter{}
	withPath, err := a.CommandWithConfigPath(CheckVet, "")
	if err != nil {
		t.Fatalf("CommandWithConfigPath(vet, \"\"): %v", err)
	}
	plain, err := a.Command(CheckVet)
	if err != nil {
		t.Fatalf("Command(vet): %v", err)
	}
	if !equalStrings(withPath, plain) {
		t.Fatalf("CommandWithConfigPath(vet, \"\") = %v, want identical to Command(vet) = %v", withPath, plain)
	}
	cfgPath := argAfter(plain, "--config-file")
	if filepath.Base(cfgPath) != mypyConfigBase {
		t.Fatalf("--config-file = %q, want the language-tools constant file %q", cfgPath, mypyConfigBase)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read resolved config-file %s: %v", cfgPath, err)
	}
	if string(data) != mypyDefaultConfig {
		t.Fatalf("resolved config-file content = %q, want %q (unchanged baseline)", data, mypyDefaultConfig)
	}
}

// TestConfigPathBuildCommandUnaffectedByConfigPath checks a check with no
// config to point anywhere (Python build) falls through CommandWithConfigPath
// to Command unchanged, regardless of ConfigPath.
func TestConfigPathBuildCommandUnaffectedByConfigPath(t *testing.T) {
	a := pythonAdapter{}
	withPath, err := a.CommandWithConfigPath(CheckBuild, "/should/be/ignored")
	if err != nil {
		t.Fatalf("CommandWithConfigPath(build): %v", err)
	}
	plain, err := a.Command(CheckBuild)
	if err != nil {
		t.Fatalf("Command(build): %v", err)
	}
	if !equalStrings(withPath, plain) {
		t.Fatalf("CommandWithConfigPath(build, path) = %v, want identical to Command(build) = %v (build has no config seam)", withPath, plain)
	}
}

// --- run.go dispatch: ConfigPathCommand wiring and runID -------------------

// TestConfigPathRunThreadsIntoExecutedCommand checks Run itself — not just
// the adapter method — dispatches through ConfigPathCommand and carries the
// resulting argv onto RunResult.Command when the underlying tool is
// genuinely invoked, proving the flag actually reaches the spawned process
// rather than only the argv-construction helper.
func TestConfigPathRunThreadsIntoExecutedCommand(t *testing.T) {
	if _, err := exec.LookPath("ruff"); err != nil {
		t.Skip("ruff not on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "clean.py"), []byte("x = 1\n"), 0o644); err != nil {
		t.Fatalf("write clean.py: %v", err)
	}
	cfg := filepath.Join(t.TempDir(), "ruff.toml")
	if err := os.WriteFile(cfg, []byte(""), 0o644); err != nil {
		t.Fatalf("write ruff.toml: %v", err)
	}
	target := Target{Language: LanguagePython, Check: CheckLint, Dir: dir, ConfigPath: cfg}
	res, err := Run(context.Background(), target, Options{LogDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := argAfter(res.Command, "--config"); got != cfg {
		t.Fatalf("RunResult.Command = %v, want --config %q threaded through by Run", res.Command, cfg)
	}
}

// TestConfigPathRunIDUnsetMatchesPreConfigPathIdentity checks a target that
// leaves ConfigPath unset hashes to the same run identity regardless of
// whether the field is explicitly zeroed or simply never touched — runID's
// documented contract that an unset field never enters the identity key.
func TestConfigPathRunIDUnsetMatchesPreConfigPathIdentity(t *testing.T) {
	target := Target{Language: LanguageGo, Check: CheckLint, Dir: "/some/dir", Args: []string{"x"}}
	withField, err := runID(target)
	if err != nil {
		t.Fatalf("runID: %v", err)
	}
	target2 := target
	target2.ConfigPath = ""
	withFieldAgain, err := runID(target2)
	if err != nil {
		t.Fatalf("runID: %v", err)
	}
	if withField != withFieldAgain {
		t.Fatalf("runID with ConfigPath unset is not stable: %q vs %q", withField, withFieldAgain)
	}
}

// TestConfigPathAloneChangesRunIdentity checks two targets differing only in
// ConfigPath get distinct run identities, mirroring the same guarantee Args
// already has, so a caller-supplied config never collides with a
// discovery-based run on one cache entry or log file.
func TestConfigPathAloneChangesRunIdentity(t *testing.T) {
	base := Target{Language: LanguageGo, Check: CheckLint, Dir: "/some/dir"}
	withCfg := base
	withCfg.ConfigPath = "/custom/config.yml"

	baseID, err := runID(base)
	if err != nil {
		t.Fatalf("runID(base): %v", err)
	}
	cfgID, err := runID(withCfg)
	if err != nil {
		t.Fatalf("runID(withCfg): %v", err)
	}
	if baseID == cfgID {
		t.Fatalf("runID = %q for both targets, want distinct IDs when only ConfigPath differs", baseID)
	}
}

// TestConfigPathCargoAdapterDoesNotImplementConfigPathCommand checks the
// rust adapter has no CommandWithConfigPath method, so Run's type assertion
// in run.go falls through to plain Command for it exactly as before —
// clippy's CLIPPY_CONF_DIR route (an inherited environment variable) is the
// only config-path mechanism it keeps.
func TestConfigPathCargoAdapterDoesNotImplementConfigPathCommand(t *testing.T) {
	adapter, ok := lookup(LanguageRust)
	if !ok {
		t.Fatal("no rust adapter registered")
	}
	if _, implements := adapter.(ConfigPathCommand); implements {
		t.Fatal("rust adapter unexpectedly implements ConfigPathCommand; cargo.go is expected untouched by the config-path seam")
	}
}

// equalStrings reports whether a and b hold the same elements in the same
// order.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
