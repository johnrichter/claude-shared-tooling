package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// wantModulePath is the VCS-qualified module path this repo publishes under.
// External callers resolve it with `go get github.com/.../build-helpers` —
// a bare or relative declaration here would break that resolution silently
// (the local relative-import build still succeeds, so nothing else catches it).
const wantModulePath = "github.com/johnrichter/claude-shared-tooling/go/build-helpers"

var moduleDeclRe = regexp.MustCompile(`(?m)^module\s+(\S+)\s*$`)

// TestGoModDeclaresVCSQualifiedModulePath asserts go.mod's module line is a
// fully host-qualified path, not a bare name or filesystem-relative string.
func TestGoModDeclaresVCSQualifiedModulePath(t *testing.T) {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	m := moduleDeclRe.FindStringSubmatch(string(data))
	if m == nil {
		t.Fatal("go.mod has no `module` declaration")
	}
	path := m[1]

	if strings.HasPrefix(path, ".") || strings.HasPrefix(path, "/") {
		t.Fatalf("module path %q is filesystem-relative, want a VCS-qualified path", path)
	}
	host := strings.SplitN(path, "/", 2)[0]
	if !strings.Contains(host, ".") {
		t.Fatalf("module path %q is bare/unqualified: host segment %q has no domain, so `go get` cannot resolve it externally", path, host)
	}
	if path != wantModulePath {
		t.Fatalf("go.mod module = %q, want %q", path, wantModulePath)
	}
}

// TestBuildResolvesFromModulePath builds a throwaway module that imports this
// package by its VCS-qualified module path — not a relative filesystem
// import — using a local `replace` so no network fetch is required. Go's
// module loader rejects the replace unless the target's own go.mod declares
// exactly the same path, so this fails the same way a bare/unqualified
// `module` declaration would if resolved by an external `go get`.
func TestBuildResolvesFromModulePath(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	srcDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	consumer := t.TempDir()
	goMod := "module resolvecheck\n\ngo 1.26\n\nrequire " + wantModulePath + " v0.0.0-00010101000000-000000000000\n\nreplace " + wantModulePath + " => " + srcDir + "\n"
	if err := os.WriteFile(filepath.Join(consumer, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("write consumer go.mod: %v", err)
	}

	mainGo := `package main

import "` + wantModulePath + `/bh"

func main() {
	_ = bh.EscalationTriggers()
}
`
	if err := os.WriteFile(filepath.Join(consumer, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatalf("write consumer main.go: %v", err)
	}

	cmd := exec.Command("go", "build", "-o", filepath.Join(consumer, "resolvecheck.bin"), "./...")
	cmd.Dir = consumer
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build did not resolve %s by module path: %v\n%s", wantModulePath, err, out)
	}
}

// checkAttrText returns the resolved `text` git attribute for path within dir
// (dir="" uses the test's working directory), parsed the same way the CI step's
// awk does: the value after the trailing ": " of `git check-attr text` output.
func checkAttrText(t *testing.T, dir, path string) string {
	t.Helper()
	cmd := exec.Command("git", "check-attr", "text", "--", path)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git check-attr text -- %s: %v\n%s", path, err, out)
	}
	line := strings.TrimSpace(string(out))
	i := strings.LastIndex(line, ": ")
	if i < 0 {
		t.Fatalf("unparseable git check-attr output: %q", line)
	}
	return line[i+2:]
}

// TestGoModResolvesToTextGitAttribute is the durable negative-fixture guard for
// the SC-RESOLVE CI check. It asserts this repo's go.mod resolves to text=set
// (regression guard for the FB6 .gitattributes correction) and that a go.mod
// forced to a non-text attribute is caught by the same `git check-attr text`
// query the CI step greps with — so the guardrail's detection can't silently rot
// past a future edit to the .gitattributes rules or the check itself.
func TestGoModResolvesToTextGitAttribute(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	if got := checkAttrText(t, "", "go.mod"); got != "set" {
		t.Fatalf("repo go.mod resolves to text=%q, want \"set\" (SC-RESOLVE; FB6 .gitattributes regression)", got)
	}

	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init fixture repo: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("go.mod filter=lfs diff=lfs merge=lfs -text\n"), 0o644); err != nil {
		t.Fatalf("write fixture .gitattributes: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatalf("write fixture go.mod: %v", err)
	}
	if got := checkAttrText(t, dir, "go.mod"); got == "set" {
		t.Fatal("go.mod forced to -text resolved to text=set; the CI SC-RESOLVE check would miss a non-text go.mod")
	}
}
