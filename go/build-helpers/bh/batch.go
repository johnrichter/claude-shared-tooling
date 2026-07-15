package bh

import (
	"io/fs"
	"os/exec"
	"path"
	"strings"
)

// ---- batch (parallel fan-out) ----

// MaxBatch is the hard ceiling on how many tasks a single fan-out may dispatch at once,
// regardless of the requested --max. It caps blast radius and reviewer/merge load.
const MaxBatch = 8

// BatchTask is one task admitted into a parallel batch — the same identity fields NextTask
// surfaces (including Name, the short operator/engine-visible label), so the dispatcher can
// route each one the same way.
type BatchTask struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Model   Model  `json:"model"`
	Effort  Effort `json:"effort"`
}

// BatchResult is exactly one of: a non-empty batch of independent runnable tasks, an
// orchestrator-only refusal, done, or blocked — mirroring NextResult's four outcomes. Tasks are
// pairwise dependency-independent and file_surface-disjoint, all within the pause boundary, in
// topo/plan order. OrchestratorOnly is set instead of Tasks (never both, and never one of Tasks'
// entries) when the anchor — the same next-eligible task NextTask would pick — declares
// orchestrator_only:true (design.md SCe): Tasks is guaranteed empty in that case, so no caller can
// mistake the refusal for a dispatchable batch. The CLI (`batch`) turns a non-nil OrchestratorOnly
// into a hard exit-1 error.
type BatchResult struct {
	Tasks            []BatchTask `json:"tasks,omitempty"`
	OrchestratorOnly *BatchTask  `json:"orchestrator_only,omitempty"`
	Done             bool        `json:"done,omitempty"`
	Blocked          []string    `json:"blocked,omitempty"`
	Reason           string      `json:"reason,omitempty"`
}

// BatchTasks selects up to clamped-max tasks to run in parallel. The candidate pool is scoped
// by run_config.pause_mode: task → at most the single next eligible task; phase → eligible
// tasks within the current (earliest-unfinished) phase; milestone → eligible tasks within the
// current milestone (may span its phases); none → eligible across the whole plan. From that
// pool it walks in topo/plan order and greedily admits a task only when it is dependency-
// independent of AND file_surface-disjoint from every already-admitted task. A task with
// absent/empty file_surface overlaps everything, so it is returned alone (batch-of-1). The
// admitted count is clamped to min(max, MaxBatch); max ≤ 0 means "no caller limit" (so just
// MaxBatch). done/blocked outcomes match NextTask for the equivalent state. statusOrDefault is
// archive-aware (archiveAwareStatus, execution.go) so a live task depending on an archived task
// resolves that dep as done instead of stalling — same fix NextTask applies.
func BatchTasks(ex ExecState, p Plan, max int) BatchResult {
	statusOrDefault := archiveAwareStatus(ex)
	depsOf := map[string][]string{}
	refByID := map[string]TaskRef{}
	for _, r := range WalkTasks(p) {
		depsOf[r.Task.ID] = r.Task.Deps
		refByID[r.Task.ID] = r
	}
	rowByID := map[string]ExecTask{}
	for _, t := range ex.Tasks {
		rowByID[t.ID] = t
	}
	topo := TopoOrder(p)

	// done / blocked mirror NextTask exactly.
	anyUnfinished := false
	for _, id := range topo.Order {
		if !statusOrDefault(id).Terminal() {
			anyUnfinished = true
			break
		}
	}
	if !anyUnfinished {
		if len(topo.Cycle) > 0 {
			return BatchResult{Blocked: topo.Cycle, Reason: "unschedulable (cycle/dangling deps): " + strings.Join(topo.Cycle, ", ")}
		}
		return BatchResult{Done: true}
	}

	eligible := func(id string) bool {
		if statusOrDefault(id).Terminal() {
			return false
		}
		for _, d := range depsOf[id] {
			if statusOrDefault(d) != StatusDone {
				return false
			}
		}
		return true
	}

	// First eligible task in topo order — the same task NextTask would pick, and the anchor
	// whose milestone/phase defines the pause boundary for phase/milestone modes.
	anchor := ""
	for _, id := range topo.Order {
		if eligible(id) {
			anchor = id
			break
		}
	}
	if anchor == "" {
		// Non-terminal tasks remain but none are eligible: a stall or cycle. Match NextTask.
		var unfinished []string
		for _, id := range topo.Order {
			if !statusOrDefault(id).Terminal() {
				unfinished = append(unfinished, id)
			}
		}
		reason := "no task has all deps done — dependency stall"
		if len(topo.Cycle) > 0 {
			reason += " (cycle: " + strings.Join(topo.Cycle, ", ") + ")"
		}
		return BatchResult{Blocked: unfinished, Reason: reason}
	}

	// Structural refusal (design.md SCe): the anchor — the same task NextTask would pick — is
	// orchestrator_only. Refuse the whole fan-out rather than silently reordering around it; the
	// orchestrator must run it inline before the loop continues. Tasks stays empty.
	if row := rowByID[anchor]; refByID[anchor].Task.OrchestratorOnly {
		return BatchResult{OrchestratorOnly: &BatchTask{ID: anchor, Name: refByID[anchor].Task.Name, Summary: row.Summary, Model: row.Model, Effort: row.Effort}}
	}

	// Scope the candidate pool to the pause boundary.
	mode := strings.TrimSpace(ex.RunConfig.PauseMode)
	anchorRef := refByID[anchor]
	inBoundary := func(id string) bool {
		switch mode {
		case "task":
			return id == anchor
		case "phase":
			return refByID[id].Phase.ID == anchorRef.Phase.ID
		case "milestone":
			return refByID[id].Milestone.ID == anchorRef.Milestone.ID
		default: // "none" (and any unrecognized value): span the whole plan
			return true
		}
	}

	// Clamp the admitted count. max ≤ 0 means "no caller limit"; the hard cap still applies.
	cap := MaxBatch
	if max > 0 && max < cap {
		cap = max
	}

	// Greedy admission in topo/plan order: a candidate joins only if it is dependency-
	// independent of AND file_surface-disjoint from every already-admitted task. The
	// file_surface-disjoint check is two layers (M13.P3.T4, FB19): fileSurfaceOverlap (kind-blind
	// path/glob intersection) plus sharedPackageSymbolRisk (kind-aware — a dir-kind entry flags a
	// same-package symbol-collision risk with any admitted task's file inside it, which the
	// literal path text alone cannot see). Neither layer can see a collision between two BRAND-NEW
	// symbols added in different, individually-declared files — that residual gap is why the
	// post-merge compile/build gate (SKILL.md, this task) is the always-on backstop.
	var admitted []string
	depClosure := map[string]map[string]bool{}
	closureFor := func(id string) map[string]bool { return transitiveDeps(id, depsOf, &depClosure) }
	for _, id := range topo.Order {
		if len(admitted) >= cap {
			break
		}
		if !inBoundary(id) || !eligible(id) {
			continue
		}
		if refByID[id].Task.OrchestratorOnly {
			// Defensive, belt-and-suspenders: an orchestrator_only task is never admitted into a
			// dispatchable batch even when it isn't the anchor (SCe) — it stays pending until it
			// becomes the anchor, at which point the refusal above fires.
			continue
		}
		ok := true
		idSurf := refByID[id].Task.FileSurface
		for _, a := range admitted {
			aSurf := refByID[a].Task.FileSurface
			if depLinked(id, a, closureFor) || fileSurfaceOverlap(surfacePaths(idSurf), surfacePaths(aSurf)) || sharedPackageSymbolRisk(idSurf, aSurf) {
				ok = false
				break
			}
		}
		if ok {
			admitted = append(admitted, id)
		}
	}

	tasks := make([]BatchTask, 0, len(admitted))
	for _, id := range admitted {
		row := rowByID[id]
		tasks = append(tasks, BatchTask{ID: id, Name: refByID[id].Task.Name, Summary: row.Summary, Model: row.Model, Effort: row.Effort})
	}
	return BatchResult{Tasks: tasks}
}

// depLinked reports whether a and b are dependency-linked: one is in the other's transitive
// dependency closure. Such a pair can never run in parallel.
func depLinked(a, b string, closureFor func(string) map[string]bool) bool {
	return closureFor(a)[b] || closureFor(b)[a]
}

// transitiveDeps returns (and memoizes) the set of all tasks id transitively depends on.
func transitiveDeps(id string, depsOf map[string][]string, memo *map[string]map[string]bool) map[string]bool {
	if c, ok := (*memo)[id]; ok {
		return c
	}
	out := map[string]bool{}
	(*memo)[id] = out // guard against cycles: present (empty) before recursion
	var visit func(string)
	visit = func(cur string) {
		for _, d := range depsOf[cur] {
			if d == id || out[d] {
				continue
			}
			out[d] = true
			visit(d)
		}
	}
	visit(id)
	return out
}

// surfacePaths extracts the bare path/pattern strings from a typed file_surface, kind-agnostic —
// fileSurfaceOverlap's glob-intersection math cares only about the path text, not whether an
// entry is a file/glob/dir or Required (that match-semantics enforcement is VerifyFileSurface's
// job, surface.go). A nil/empty entries slice yields a nil/empty slice, preserving the "absent
// surface overlaps everything" behavior fileSurfaceOverlap already implements.
func surfacePaths(entries []FileSurfaceEntry) []string {
	if len(entries) == 0 {
		return nil
	}
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Path
	}
	return out
}

// fileSurfaceOverlap is the conservative disjointness predicate: it reports true when two
// file_surface glob sets might touch the same file. An absent/empty surface on either side
// means the surface is unknown, which overlaps everything (forcing a batch-of-1). Two globs
// overlap when one matches the other's literal prefix or their patterns could intersect; the
// predicate biases toward 'overlap' whenever an intersection cannot be disproven.
func fileSurfaceOverlap(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return true // unknown surface overlaps everything
	}
	for _, ga := range a {
		ca := path.Clean(strings.TrimSpace(ga))
		for _, gb := range b {
			cb := path.Clean(strings.TrimSpace(gb))
			if globsMayIntersect(ca, cb) {
				return true
			}
		}
	}
	return false
}

// globsMayIntersect reports whether two cleaned glob patterns could match a common path. It is
// deliberately conservative: it returns true unless it can prove the two patterns are disjoint.
func globsMayIntersect(a, b string) bool {
	if a == b {
		return true
	}
	// Either pattern matching the other literally (when the other has no meta) is an overlap.
	if !hasGlobMeta(b) {
		if ok, err := path.Match(a, b); err != nil || ok {
			return true
		}
	}
	if !hasGlobMeta(a) {
		if ok, err := path.Match(b, a); err != nil || ok {
			return true
		}
	}
	// Compare segment by segment; if every shared segment can match, the patterns may intersect.
	as := strings.Split(a, "/")
	bs := strings.Split(b, "/")
	n := len(as)
	if len(bs) < n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		sa, sb := as[i], bs[i]
		// A "**" segment swallows any remaining path on that side — cannot disprove overlap.
		if sa == "**" || sb == "**" {
			return true
		}
		if !segmentsMayMatch(sa, sb) {
			return false // a shared segment is provably disjoint -> globs cannot intersect
		}
	}
	// Differing depth with no "**" to bridge it: the shorter pattern matches a prefix only, so
	// they describe different paths and cannot intersect.
	if len(as) != len(bs) {
		return false
	}
	return true
}

// segmentsMayMatch reports whether two single path segments (no separators) could match a
// common literal. Conservative: any meta character that cannot be disproven yields true.
func segmentsMayMatch(a, b string) bool {
	if a == b {
		return true
	}
	am, bm := hasGlobMeta(a), hasGlobMeta(b)
	if !am && !bm {
		return a == b // both literal and already unequal
	}
	if !am { // b is the pattern, a is literal
		if ok, err := path.Match(b, a); err != nil || ok {
			return true
		}
		return false
	}
	if !bm { // a is the pattern, b is literal
		if ok, err := path.Match(a, b); err != nil || ok {
			return true
		}
		return false
	}
	// Both contain meta — cannot cheaply prove disjointness; bias to overlap.
	return true
}

// hasGlobMeta reports whether s contains glob metacharacters.
func hasGlobMeta(s string) bool { return strings.ContainsAny(s, "*?[") }

// ---- SCa bidirectional re-assertion (M13.P3.T2, sca-gate-semantics.md) ----
//
// VerifyFileSurface (surface.go, M13.P3.T1) is the FORWARD direction: every declared entry
// present on disk. This section adds the REVERSE direction (every changed path is covered by
// SOME declared entry — an off-surface write, FB1's literal failure) and the post-merge form of
// the forward direction (the union of every merged task's surface against the integrated tree —
// the path a merge itself can drop, downstream of every per-task pre-commit). Both close FB1;
// neither alone does (spike: "a pre-merge-only or forward-only gate lets one through").

// ChangedSetResult is the reverse-direction check's verdict: every path in a git-derived
// changed-set must be covered by some declared file_surface entry — required or optional either
// counts as coverage (the reverse direction cares only whether a path was declared at all, not
// whether the declaration was mandatory).
type ChangedSetResult struct {
	OK         bool     `json:"ok"`
	OffSurface []string `json:"off_surface,omitempty"`
}

// VerifyChangedSetSubsetOfSurface checks changed (bare paths, e.g. from `git status --porcelain`
// with its 2-char status + separator stripped) against entries: an entry covers a changed path
// per its kind — file requires an exact cleaned-path match, glob matches via the same pattern
// semantics fs.Glob uses (path.Match), dir covers the directory itself or anything nested under
// it. A changed path no entry covers is an off-surface write. Empty/whitespace-only lines are
// ignored (tolerates a trailing blank line from line-splitting the caller's input).
func VerifyChangedSetSubsetOfSurface(changed []string, entries []FileSurfaceEntry) ChangedSetResult {
	res := ChangedSetResult{OK: true}
	for _, c := range changed {
		cc := path.Clean(strings.TrimSpace(c))
		if cc == "" || cc == "." {
			continue
		}
		covered := false
		for _, e := range entries {
			if surfaceCovers(e, cc) {
				covered = true
				break
			}
		}
		if !covered {
			res.OK = false
			res.OffSurface = append(res.OffSurface, cc)
		}
	}
	return res
}

// surfaceCovers reports whether one declared file_surface entry covers changedPath, per kind.
func surfaceCovers(e FileSurfaceEntry, changedPath string) bool {
	p := path.Clean(strings.TrimSpace(e.Path))
	switch e.Kind.Resolve() {
	case FSGlob:
		ok, err := path.Match(p, changedPath)
		return err == nil && ok
	case FSDir:
		return changedPath == p || strings.HasPrefix(changedPath, p+"/")
	default: // FSFile
		return changedPath == p
	}
}

// VerifyMergedSurface is the post-merge form of the forward direction (site 3): after an octopus
// merge integrates N per-task branches into the build-branch tip, the union of every merged
// task's declared file_surface must still be satisfied in the merged tree — a path present at
// every task's own pre-commit check but dropped by the merge itself (FB1) is invisible to any
// check that ran before the merge, so this is the one assertion downstream of it. Concatenating
// every task's entries and calling VerifyFileSurface once is equivalent to checking each task's
// entries individually against the same tree — no new match semantics, just the union input.
func VerifyMergedSurface(fsys fs.FS, perTaskSurfaces [][]FileSurfaceEntry) FileSurfaceResult {
	var union []FileSurfaceEntry
	for _, s := range perTaskSurfaces {
		union = append(union, s...)
	}
	return VerifyFileSurface(fsys, union)
}

// ---- FB19: symbol-level disjointness screen + post-merge build gate (M13.P3.T4) ----
//
// Git's text-level merge and fileSurfaceOverlap (kind-blind, literal path/glob text only) are
// both blind to a same-package symbol collision: two file_surface-disjoint tasks each add an
// identically-named package-level symbol in a DIFFERENT file — the merge is clean (no text
// conflict, different files) and the predicate calls the two literal paths disjoint (different
// filenames, no glob overlap), yet the package no longer compiles. Two complementary layers:
//   - sharedPackageSymbolRisk (below) is the STATICALLY DERIVABLE half — when one side declares
//     kind=dir (a whole-package target), any other candidate's path inside that directory is a
//     same-package risk by construction, provable from Path+Kind alone, no file content needed.
//     It deliberately does NOT flag two plain file-kind entries in the same directory (e.g.
//     "pkg/a.go" vs "pkg/b.go" with neither side kind=dir) — that pair is the genuinely
//     undecidable case (the new symbol names don't exist yet at schedule time), which this layer
//     correctly leaves to the second layer rather than guessing.
//   - RunPostMergeBuildGate (below) is the always-on backstop for exactly that undecidable case:
//     run immediately after every octopus merge (SKILL.md), it catches any residual duplicate-
//     symbol collision the predicate could not decide, by actually compiling the merged tree.

// sharedPackageSymbolRisk reports whether a and b might collide on a package-level symbol because
// one side's declared surface covers the WHOLE package directory (kind=dir) that the other side's
// entry falls inside. This is BatchTasks's second disjointness layer, additive to
// fileSurfaceOverlap — it only ever adds an overlap verdict, never removes one already found.
func sharedPackageSymbolRisk(a, b []FileSurfaceEntry) bool {
	return dirEntryCoversOther(a, b) || dirEntryCoversOther(b, a)
}

// dirEntryCoversOther reports whether any kind=dir entry in dirSide names a directory that some
// entry in otherSide falls inside (same path, or nested under it) — the one-directional half of
// sharedPackageSymbolRisk's symmetric check.
func dirEntryCoversOther(dirSide, otherSide []FileSurfaceEntry) bool {
	for _, e := range dirSide {
		if e.Kind.Resolve() != FSDir {
			continue
		}
		dir := path.Clean(strings.TrimSpace(e.Path))
		if dir == "" || dir == "." {
			continue
		}
		for _, o := range otherSide {
			op := path.Clean(strings.TrimSpace(o.Path))
			if op == dir || strings.HasPrefix(op, dir+"/") {
				return true
			}
		}
	}
	return false
}

// PostMergeBuildGateResult is the FB19 post-merge compile/build gate's verdict.
type PostMergeBuildGateResult struct {
	OK     bool   `json:"ok"`
	Output string `json:"output,omitempty"`
}

// RunPostMergeBuildGate runs cmd (argv, e.g. ["go","build","./..."]) rooted at dir and reports
// whether it succeeded — the immediate, per-batch, always-on check SKILL.md runs right after every
// octopus merge (never only at the milestone/phase-boundary full-suite gate), so a same-package
// duplicate-symbol collision fails fast per-batch instead of surviving to the next boundary. An
// empty cmd (no build toolchain detected for the target repo) is a deliberate no-op — OK=true —
// so an undetectable toolchain never blocks a merge; the SKILL.md step resolves cmd once per build
// worktree (compute-once) from the repo's own toolchain marker. Kept to a single external command
// with no target-language awareness of its own: the compile step is the simple, always-on half of
// FB19, complementing (not replacing) sharedPackageSymbolRisk's best-effort static screen above.
func RunPostMergeBuildGate(dir string, cmd []string) PostMergeBuildGateResult {
	if len(cmd) == 0 {
		return PostMergeBuildGateResult{OK: true}
	}
	c := exec.Command(cmd[0], cmd[1:]...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	return PostMergeBuildGateResult{OK: err == nil, Output: string(out)}
}
