package toolchain

// Adversarial coverage for resolveCargoLock (T8 criterion 6, K8-R1-AUDIT):
// the ancestor-walk cargo-audit's --file argument depends on.

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
