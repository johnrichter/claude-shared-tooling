package bh

import (
	"cmp"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/roster"
)

var (
	reMilestone = regexp.MustCompile(`^M[0-9]+$`)
	rePhase     = regexp.MustCompile(`^M[0-9]+\.P[0-9]+$`)
	reTask      = regexp.MustCompile(`^M[0-9]+\.P[0-9]+\.T[0-9]+$`)
)

// TaskRef is a task paired with its milestone/phase, yielded in plan order.
type TaskRef struct {
	Task      Task
	Milestone Milestone
	Phase     Phase
}

// WalkTasks returns every task in plan order with its milestone/phase context.
func WalkTasks(p Plan) []TaskRef {
	var refs []TaskRef
	for _, m := range p.Milestones {
		for _, ph := range m.Phases {
			for _, t := range ph.Tasks {
				refs = append(refs, TaskRef{Task: t, Milestone: m, Phase: ph})
			}
		}
	}
	return refs
}

// TaskByID looks up one task by id via WalkTasks, returning its TaskRef and whether it was
// found. The one shared lookup every id-keyed caller uses — the engine pre-done gate's
// verify-surface CLI and the orchestrator's bidirectional re-assertion CLI
// (verify-surface's post-merge union form + check-changed-surface) — instead of each
// hand-rolling the same WalkTasks scan.
func TaskByID(p Plan, id string) (TaskRef, bool) {
	for _, r := range WalkTasks(p) {
		if r.Task.ID == id {
			return r, true
		}
	}
	return TaskRef{}, false
}

// ContentHash is the reconciliation match key: a hash of the spec-bearing fields only
// (summary + deliverable + acceptance), canonicalized so key order and whitespace never
// change it. ID and tier are deliberately excluded — a tier retune is not a spec change and
// must not force a rebuild.
func ContentHash(t Task) string {
	canon, _ := json.Marshal(struct {
		Summary     string   `json:"summary"`
		Deliverable string   `json:"deliverable"`
		Acceptance  []string `json:"acceptance"`
	}{t.Summary, t.Deliverable, append([]string{}, t.Acceptance...)})
	sum := sha256.Sum256(canon)
	return fmt.Sprintf("%x", sum)[:16]
}

// Hashes returns {taskID: contentHash} for provenance.
func Hashes(p Plan) map[string]string {
	out := map[string]string{}
	for _, r := range WalkTasks(p) {
		out[r.Task.ID] = ContentHash(r.Task)
	}
	return out
}

// TopoResult is a dependency-respecting task order plus any unschedulable tasks.
type TopoResult struct {
	Order []string `json:"order"`
	Cycle []string `json:"cycle"` // tasks that never drained: a dep cycle or dangling/forward ref
}

// TopoOrder computes a stable dependency order via Kahn's algorithm. Among ready tasks it
// preserves original plan order. Dangling deps (to non-existent ids) are ignored as edges
// here — ValidatePlan reports them — so a single typo doesn't masquerade as a cycle.
func TopoOrder(p Plan) TopoResult {
	var ids []string
	deps := map[string][]string{}
	idx := map[string]int{}
	for i, r := range WalkTasks(p) {
		id := r.Task.ID
		ids = append(ids, id)
		idx[id] = i
		var d []string
		for _, x := range r.Task.Deps {
			if x != id {
				d = append(d, x)
			}
		}
		deps[id] = d
	}
	idSet := map[string]bool{}
	for _, id := range ids {
		idSet[id] = true
	}
	indeg := map[string]int{}
	fwd := map[string][]string{}
	for _, id := range ids {
		indeg[id] = 0
	}
	for _, id := range ids {
		for _, d := range deps[id] {
			if !idSet[d] {
				continue // dangling — not an edge
			}
			indeg[id]++
			fwd[d] = append(fwd[d], id)
		}
	}
	var ready []string
	for _, id := range ids {
		if indeg[id] == 0 {
			ready = append(ready, id)
		}
	}
	var order []string
	done := map[string]bool{}
	for len(ready) > 0 {
		slices.SortFunc(ready, func(a, b string) int { return cmp.Compare(idx[a], idx[b]) })
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		done[id] = true
		for _, n := range fwd[id] {
			indeg[n]--
			if indeg[n] == 0 {
				ready = append(ready, n)
			}
		}
	}
	var cycle []string
	for _, id := range ids {
		if !done[id] {
			cycle = append(cycle, id)
		}
	}
	return TopoResult{Order: order, Cycle: cycle}
}

// ---- diff: old plan vs new plan, by id + content hash ----

type IDHash struct {
	ID   string `json:"id"`
	Hash string `json:"hash"`
}
type IDChange struct {
	ID      string `json:"id"`
	OldHash string `json:"old_hash"`
	NewHash string `json:"new_hash"`
}
type DiffResult struct {
	Carried []IDHash   `json:"carried"`
	Changed []IDChange `json:"changed"`
	Added   []IDHash   `json:"added"`
	Removed []IDHash   `json:"removed"`
}

// Diff matches tasks by id + content hash: same id+hash carries, same id/different hash is a
// spec change, new id is added, absent id is removed.
func Diff(oldP, newP Plan) DiffResult {
	oldByID := map[string]string{}
	for _, r := range WalkTasks(oldP) {
		oldByID[r.Task.ID] = ContentHash(r.Task)
	}
	res := DiffResult{Carried: []IDHash{}, Changed: []IDChange{}, Added: []IDHash{}, Removed: []IDHash{}}
	newIDs := map[string]bool{}
	for _, r := range WalkTasks(newP) {
		h := ContentHash(r.Task)
		newIDs[r.Task.ID] = true
		old, ok := oldByID[r.Task.ID]
		switch {
		case !ok:
			res.Added = append(res.Added, IDHash{r.Task.ID, h})
		case old == h:
			res.Carried = append(res.Carried, IDHash{r.Task.ID, h})
		default:
			res.Changed = append(res.Changed, IDChange{r.Task.ID, old, h})
		}
	}
	for id := range oldByID {
		if !newIDs[id] {
			res.Removed = append(res.Removed, IDHash{ID: id})
		}
	}
	slices.SortFunc(res.Removed, func(a, b IDHash) int { return cmp.Compare(a.ID, b.ID) })
	return res
}

// ---- tier checks ----

type TierIssue struct {
	ID    string `json:"id"`
	Issue string `json:"issue"`
}
type TierResult struct {
	OK     bool        `json:"ok"`
	Issues []TierIssue `json:"issues"`
}

// modelSelectable reports whether id is a valid plan-pinnable model: the roster's
// selectable=='new-work' projection (ai-shared-lib/go/roster), OR a declared dispatch sentinel
// (e.g. "inherit"), which has no selectable field but is enum-valid by definition. This is
// deliberately NOT the authoring gate's selectable!='retired' allowlist — the two consumers read
// different roster projections by design (SC-MODELROSTER).
func modelSelectable(id string) bool {
	sel, err := roster.Selectable(id)
	if err != nil {
		var sentinelErr *roster.SentinelError
		return errors.As(err, &sentinelErr)
	}
	return sel == roster.SelectableNewWork
}

// effortNames renders a roster effort list for an issue message, so the message always reflects
// the roster's actual answer rather than a hand-maintained claim about which models support which
// tier.
func effortNames(efforts []roster.Effort) string {
	names := make([]string, len(efforts))
	for i, e := range efforts {
		names[i] = string(e)
	}
	return strings.Join(names, ", ")
}

// CheckTiers validates each task's (model, effort) combo entirely from the roster
// (ai-shared-lib/go/roster): model validity is modelSelectable; effort validity is the model's
// effort_available list, with an effort-exempt model or a dispatch sentinel accepting every
// level (roster.EffortAvailable folds both into AllEfforts). A model the roster cannot resolve
// (unrecognized, or a sentinel) skips the effort-availability check — the model issue above
// already covers it, so a stale/sentinel model never also produces a spurious effort complaint.
func CheckTiers(p Plan) TierResult {
	issues := []TierIssue{}
	for _, r := range WalkTasks(p) {
		t := r.Task
		if !modelSelectable(string(t.Model)) {
			issues = append(issues, TierIssue{t.ID, fmt.Sprintf("model '%s' not in the selectable set", t.Model)})
		}
		if !t.Effort.Known() {
			issues = append(issues, TierIssue{t.ID, fmt.Sprintf("effort '%s' not a valid tier", t.Effort)})
			continue
		}
		avail, err := roster.EffortAvailable(string(t.Model))
		if err != nil {
			continue
		}
		if !slices.Contains(avail, roster.Effort(t.Effort)) {
			issues = append(issues, TierIssue{t.ID, fmt.Sprintf("effort '%s' is not available for model '%s' (roster allows: %s)", t.Effort, t.Model, effortNames(avail))})
		}
	}
	return TierResult{OK: len(issues) == 0, Issues: issues}
}

// ---- validate: structural + integrity + tier gate ----

var rootKeys = map[string]bool{
	"goal": true, "success_criteria": true, "assumptions": true, "tradeoffs": true,
	"milestones": true, "risks": true, "open_questions": true, "provenance": true,
}

type ValidationResult struct {
	OK       bool     `json:"ok"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

// ValidatePlanBytes is the single deterministic plan gate. It mirrors plan-schema.json's hard
// constraints (required fields, id patterns, enums, minItems) plus the integrity checks the
// schema cannot express: id uniqueness, id-hierarchy nesting, dep-reference integrity, and
// dependency cycles. Unknown root keys are warnings (not failures).
func ValidatePlanBytes(raw []byte) ValidationResult {
	res := ValidationResult{Errors: []string{}, Warnings: []string{}}
	addE := func(f string, a ...any) { res.Errors = append(res.Errors, fmt.Sprintf(f, a...)) }

	var keyProbe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keyProbe); err != nil {
		res.Errors = append(res.Errors, "plan is not a JSON object: "+err.Error())
		return res
	}
	for k := range keyProbe {
		if !rootKeys[k] {
			res.Warnings = append(res.Warnings, fmt.Sprintf("unknown root key '%s' (not in plan schema)", k))
		}
	}
	var p Plan
	if err := json.Unmarshal(raw, &p); err != nil {
		res.Errors = append(res.Errors, "plan failed to decode: "+err.Error())
		return res
	}

	if strings.TrimSpace(p.Goal) == "" {
		addE("goal: required non-empty string")
	}
	if len(p.SuccessCriteria) < 1 {
		addE("success_criteria: required array of ≥1 strings")
	}
	if len(p.Milestones) < 1 {
		addE("milestones: required array of ≥1")
		res.OK = false
		return res
	}

	taskIDs := map[string]bool{}
	seenM, seenP, seenT := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, m := range p.Milestones {
		if !reMilestone.MatchString(m.ID) {
			addE("milestone id '%s' must match M<n>", m.ID)
		}
		if seenM[m.ID] {
			addE("duplicate milestone id '%s'", m.ID)
		}
		seenM[m.ID] = true
		if strings.TrimSpace(m.Name) == "" {
			addE("milestone %s: name required", m.ID)
		}
		if len(m.Phases) < 1 {
			addE("milestone %s: phases array of ≥1 required", m.ID)
			continue
		}
		for _, ph := range m.Phases {
			if !rePhase.MatchString(ph.ID) {
				addE("phase id '%s' must match M<n>.P<n>", ph.ID)
			} else if !strings.HasPrefix(ph.ID, m.ID+".P") {
				addE("phase %s is nested under %s but its id prefix does not match", ph.ID, m.ID)
			}
			if seenP[ph.ID] {
				addE("duplicate phase id '%s'", ph.ID)
			}
			seenP[ph.ID] = true
			if strings.TrimSpace(ph.Name) == "" {
				addE("phase %s: name required", ph.ID)
			}
			if len(ph.Tasks) < 1 {
				addE("phase %s: tasks array of ≥1 required", ph.ID)
				continue
			}
			for _, t := range ph.Tasks {
				if !reTask.MatchString(t.ID) {
					addE("task id '%s' must match M<n>.P<n>.T<n>", t.ID)
					continue
				}
				if !strings.HasPrefix(t.ID, ph.ID+".T") {
					addE("task %s is nested under %s but its id prefix does not match", t.ID, ph.ID)
				}
				if seenT[t.ID] {
					addE("duplicate task id '%s'", t.ID)
				}
				seenT[t.ID] = true
				taskIDs[t.ID] = true
				if strings.TrimSpace(t.Name) == "" {
					addE("task %s: name required", t.ID)
				}
				if strings.TrimSpace(t.Summary) == "" {
					addE("task %s: summary required", t.ID)
				}
				if strings.TrimSpace(t.Deliverable) == "" {
					addE("task %s: deliverable required", t.ID)
				}
				if t.Kind != "" && !t.Kind.Known() {
					addE("task %s: deliverable_kind '%s' must be 'code' or 'docs' (omit for code)", t.ID, t.Kind)
				}
				if strings.TrimSpace(t.TestStrategy) == "" {
					addE("task %s: test_strategy required", t.ID)
				}
				if len(t.Acceptance) < 1 {
					addE("task %s: acceptance array of ≥1 required", t.ID)
				}
				if len(t.FileSurface) == 0 && t.Kind.Resolve() == KindCode {
					res.Warnings = append(res.Warnings, fmt.Sprintf("task %s: file_surface absent on a code task — parallel overlap checks may not be possible", t.ID))
				}
				for _, fs := range t.FileSurface {
					if strings.TrimSpace(fs.Path) == "" {
						addE("task %s: file_surface entry has an empty path", t.ID)
					}
					if fs.Kind != "" && !fs.Kind.Known() {
						addE("task %s: file_surface entry '%s' has kind '%s' — must be 'file', 'glob', or 'dir' (omit for file)", t.ID, fs.Path, fs.Kind)
					}
				}
				for _, d := range t.Deps {
					if !reTask.MatchString(d) {
						addE("task %s: dep '%s' is not a task id", t.ID, d)
					}
				}
			}
		}
	}
	// dep-reference integrity + self-dep (cycles handled below)
	for _, r := range WalkTasks(p) {
		for _, d := range r.Task.Deps {
			if d == r.Task.ID {
				addE("task %s: depends on itself", r.Task.ID)
			} else if reTask.MatchString(d) && !taskIDs[d] {
				addE("task %s: dep '%s' references a non-existent task", r.Task.ID, d)
			}
		}
	}
	if cyc := TopoOrder(p).Cycle; len(cyc) > 0 {
		addE("dependency cycle / unschedulable tasks: %s", strings.Join(cyc, ", "))
	}
	for _, i := range CheckTiers(p).Issues {
		addE("tier: %s: %s", i.ID, i.Issue)
	}

	res.OK = len(res.Errors) == 0
	return res
}
