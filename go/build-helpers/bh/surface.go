package bh

import (
	"io/fs"

	"github.com/bmatcuk/doublestar/v4"
)

// This file is the engine's pre-done assertion: before a task is marked done, its
// declared file_surface must be actually present on disk, with the pinned match semantics below.
// VerifyFileSurface stays pure per the package doc comment — fs.FS is an interface, not a
// concrete IO call, so the real check (os.DirFS(worktree)) is supplied by package main and a
// fake (testing/fstest.MapFS) is supplied by this package's own unit tests.

// FileSurfaceViolation is one declared entry that failed its pinned match semantics.
type FileSurfaceViolation struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// FileSurfaceResult is the pre-done gate's verdict.
type FileSurfaceResult struct {
	OK         bool                   `json:"ok"`
	Violations []FileSurfaceViolation `json:"violations,omitempty"`
}

// VerifyFileSurface applies file_surface's pinned match semantics against fsys, rooted at the
// task's worktree:
//   - kind=file (default via FileSurfaceKind.Resolve): Path must exist and be a non-directory.
//   - kind=glob: Path is a glob pattern (doublestar semantics, so `**` recurses across path
//     separators) and must match >=1 entry.
//   - kind=dir: Path must exist, be a directory, and be non-empty (>=1 immediate child).
//   - Required (any kind), additionally: every matched file must be non-trivial — non-zero byte
//     size — so a task cannot fake completion with an empty placeholder. A dir match is not
//     size-checked (directories have no byte size of their own); its non-empty baseline already
//     covers "not trivial" for that kind.
//
// An empty/nil entries slice is vacuously OK — absence of a whole file_surface is a plan-level
// warning (ValidatePlanBytes), not a done-gate failure. Every failing entry is collected (not
// just the first) so one call reports the complete violation set.
func VerifyFileSurface(fsys fs.FS, entries []FileSurfaceEntry) FileSurfaceResult {
	res := FileSurfaceResult{OK: true}
	fail := func(path, reason string) {
		res.OK = false
		res.Violations = append(res.Violations, FileSurfaceViolation{Path: path, Reason: reason})
	}
	for _, e := range entries {
		switch e.Kind.Resolve() {
		case FSGlob:
			verifyGlob(fsys, e, fail)
		case FSDir:
			verifyDir(fsys, e, fail)
		default: // FSFile
			verifyFile(fsys, e, fail)
		}
	}
	return res
}

func verifyGlob(fsys fs.FS, e FileSurfaceEntry, fail func(path, reason string)) {
	matches, err := doublestar.Glob(fsys, e.Path)
	if err != nil {
		fail(e.Path, "malformed glob pattern: "+err.Error())
		return
	}
	if len(matches) == 0 {
		fail(e.Path, "glob matched zero files")
		return
	}
	if !e.Required {
		return
	}
	for _, m := range matches {
		info, err := fs.Stat(fsys, m)
		switch {
		case err != nil:
			fail(e.Path, "matched file "+m+" could not be stat'd: "+err.Error())
		case !info.IsDir() && info.Size() == 0:
			fail(e.Path, "matched file "+m+" is empty (required entry must be non-trivial)")
		}
	}
}

func verifyDir(fsys fs.FS, e FileSurfaceEntry, fail func(path, reason string)) {
	info, err := fs.Stat(fsys, e.Path)
	if err != nil {
		fail(e.Path, "directory does not exist: "+err.Error())
		return
	}
	if !info.IsDir() {
		fail(e.Path, "path exists but is not a directory")
		return
	}
	children, err := fs.ReadDir(fsys, e.Path)
	if err != nil {
		fail(e.Path, "cannot read directory: "+err.Error())
		return
	}
	if len(children) == 0 {
		fail(e.Path, "directory is empty")
	}
}

func verifyFile(fsys fs.FS, e FileSurfaceEntry, fail func(path, reason string)) {
	info, err := fs.Stat(fsys, e.Path)
	if err != nil {
		fail(e.Path, "file does not exist: "+err.Error())
		return
	}
	if info.IsDir() {
		fail(e.Path, "path exists but is a directory, not a file")
		return
	}
	if e.Required && info.Size() == 0 {
		fail(e.Path, "file exists but is empty (required entry must be non-trivial)")
	}
}
