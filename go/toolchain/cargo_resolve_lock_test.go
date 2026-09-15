package toolchain

// Adversarial coverage for cargo.go's two ancestor walks (T8 criterion 6,
// K8-R1-AUDIT): resolveCargoLock, which cargo-audit's --file argument
// depends on, and resolveCargoDenyConfig, which decides whether
// runSecurity runs cargo-deny's full four-group check or scopes it to bans
// and sources.

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveCargoLockFindsAncestor: a workspace-member dir (no Cargo.lock of
// its own) resolves to the workspace-root lock two levels up — the exact
// shape a cargo workspace member has (member/Cargo.toml, no member lockfile;
// root/Cargo.lock is the one cargo maintains).
func TestResolveCargoLockFindsAncestor(t *testing.T) {
	root := t.TempDir()
	lock := filepath.Join(root, "Cargo.lock")
	if err := os.WriteFile(lock, []byte("# lock"), 0o644); err != nil {
		t.Fatalf("write Cargo.lock: %v", err)
	}
	member := filepath.Join(root, "members", "bm25")
	if err := os.MkdirAll(member, 0o755); err != nil {
		t.Fatalf("mkdir member: %v", err)
	}
	got := resolveCargoLock(member)
	want, _ := filepath.Abs(lock)
	if got != want {
		t.Fatalf("resolveCargoLock(%q) = %q, want %q", member, got, want)
	}
}

// TestResolveCargoLockPrefersNearest: a lock at the immediate dir wins over
// one further up — resolveCargoLock must not skip past the nearest one.
func TestResolveCargoLockPrefersNearest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Cargo.lock"), []byte("# root"), 0o644); err != nil {
		t.Fatalf("write root Cargo.lock: %v", err)
	}
	member := filepath.Join(root, "member")
	if err := os.MkdirAll(member, 0o755); err != nil {
		t.Fatalf("mkdir member: %v", err)
	}
	nearLock := filepath.Join(member, "Cargo.lock")
	if err := os.WriteFile(nearLock, []byte("# member"), 0o644); err != nil {
		t.Fatalf("write member Cargo.lock: %v", err)
	}
	got := resolveCargoLock(member)
	want, _ := filepath.Abs(nearLock)
	if got != want {
		t.Fatalf("resolveCargoLock(%q) = %q, want the nearer %q, not an ancestor", member, got, want)
	}
}

// TestResolveCargoLockNoneFoundReturnsEmpty: no Cargo.lock anywhere up to the
// filesystem root returns "" rather than panicking or looping forever — the
// runSecurity caller treats "" as "omit --file", never a hard error.
func TestResolveCargoLockNoneFoundReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	if got := resolveCargoLock(dir); got != "" {
		t.Fatalf("resolveCargoLock(%q) = %q, want \"\" (no Cargo.lock exists under a fresh temp tree)", dir, got)
	}
}

// TestResolveCargoLockRejectsDirectoryNamedCargoLock: a directory literally
// named Cargo.lock (pathological, but os.Stat alone can't tell it from a
// file) must not be returned as if it were the lockfile.
func TestResolveCargoLockRejectsDirectoryNamedCargoLock(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Cargo.lock"), 0o755); err != nil {
		t.Fatalf("mkdir Cargo.lock: %v", err)
	}
	member := filepath.Join(root, "member")
	if err := os.MkdirAll(member, 0o755); err != nil {
		t.Fatalf("mkdir member: %v", err)
	}
	if got := resolveCargoLock(member); got != "" {
		t.Fatalf("resolveCargoLock(%q) = %q, want \"\" (Cargo.lock is a directory, not a lockfile)", member, got)
	}
}

// TestResolveCargoDenyConfigFindsAncestor: a workspace-member dir with no
// deny.toml of its own resolves to the one at the workspace root, matching
// cargo-deny's own config resolution (measured against cargo-deny 0.20.2,
// which finds an ancestor deny.toml from a member or standalone crate dir
// several levels down). A mismatch here would run the full four-group check
// against a crate cargo-deny then judges under its default,
// deny-every-license policy — the false failure the scoping exists to
// avoid.
func TestResolveCargoDenyConfigFindsAncestor(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "deny.toml")
	if err := os.WriteFile(cfg, []byte("[licenses]\nallow = []\n"), 0o644); err != nil {
		t.Fatalf("write deny.toml: %v", err)
	}
	member := filepath.Join(root, "members", "bm25")
	if err := os.MkdirAll(member, 0o755); err != nil {
		t.Fatalf("mkdir member: %v", err)
	}
	got := resolveCargoDenyConfig(member)
	want, _ := filepath.Abs(cfg)
	if got != want {
		t.Fatalf("resolveCargoDenyConfig(%q) = %q, want %q", member, got, want)
	}
}

// TestResolveCargoDenyConfigNoneFoundReturnsEmpty: no deny.toml anywhere up
// to the filesystem root returns "" rather than looping forever — the
// runSecurity caller reads "" as "this crate opted into no policy", and
// scopes cargo-deny to bans and sources.
func TestResolveCargoDenyConfigNoneFoundReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	if got := resolveCargoDenyConfig(dir); got != "" {
		t.Fatalf("resolveCargoDenyConfig(%q) = %q, want \"\" (no deny.toml exists under a fresh temp tree)", dir, got)
	}
}

// TestResolveCargoDenyConfigRejectsDirectoryNamedDenyToml: a directory
// literally named deny.toml must not be read as an opted-in policy, or the
// full four-group check runs against a crate with no license allow-list at
// all.
func TestResolveCargoDenyConfigRejectsDirectoryNamedDenyToml(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "deny.toml"), 0o755); err != nil {
		t.Fatalf("mkdir deny.toml: %v", err)
	}
	member := filepath.Join(root, "member")
	if err := os.MkdirAll(member, 0o755); err != nil {
		t.Fatalf("mkdir member: %v", err)
	}
	if got := resolveCargoDenyConfig(member); got != "" {
		t.Fatalf("resolveCargoDenyConfig(%q) = %q, want \"\" (deny.toml is a directory, not a config file)", member, got)
	}
}
