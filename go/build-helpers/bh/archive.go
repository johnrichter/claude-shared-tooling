package bh

import (
	"cmp"
	"fmt"
	"strings"
)

// ---- archive.json (preserved, retrievable store for completed milestones) ----
//
// Design: single in-project archive.json (full fidelity) + execution.json's compact
// Archived[] tombstone index (the load-bearing done/SHA/cost subset the resume hot path reads).

// ArchiveSchema marks archive.json's shape so the doc-shape sniffer (isArchiveDoc, main.go) routes
// it before the plan/exec fork — archive.json also carries a top-level "milestones" key (like
// Plan) since it embeds each task's plan-slice, so "schema" is the unambiguous discriminator.
const ArchiveSchema = "archive/v1"

// ArchiveDoc is the full-fidelity preserved record the explicit `archive` op writes/extends at
// $PROJ/archive.json. It is loaded only in-process (by the archive op itself and, per the design,
// a future retrieval/reconcile read) and never becomes model-resident.
type ArchiveDoc struct {
	Schema     string              `json:"schema"`
	Milestones []ArchivedMilestone `json:"milestones,omitempty"`
}

// ArchivedMilestone preserves one archived milestone's id/name plus every child task's full
// record, grouped by phase so a future retrieval read can still project outline/milestone/phase/
// task levels identically to the live doc.
type ArchivedMilestone struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Phases []ArchivedPhase `json:"phases"`
}

type ArchivedPhase struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Tasks []ArchivedTask `json:"tasks"`
}

// ArchivedTask is one archived task's full preserved record: the union of its immutable
// plan-slice (Task) and frozen exec-slice (ExecTask). Every field either slice carries is kept
// here for audit/retrieval; only the load-bearing subset also rides the Tombstone below.
type ArchivedTask struct {
	// plan-slice
	Summary      string             `json:"summary"`
	Deliverable  string             `json:"deliverable"`
	Kind         DeliverableKind    `json:"deliverable_kind,omitempty"`
	Model        Model              `json:"model"`
	Effort       Effort             `json:"effort"`
	Thinking     string             `json:"thinking,omitempty"`
	TestStrategy string             `json:"test_strategy"`
	Deps         []string           `json:"deps,omitempty"`
	Acceptance   []string           `json:"acceptance"`
	FileSurface  []FileSurfaceEntry `json:"file_surface,omitempty"`
	// exec-slice
	ID        string  `json:"id"`
	Status    Status  `json:"status"`
	Test      string  `json:"test,omitempty"`
	Review    string  `json:"review,omitempty"`
	Commit    string  `json:"commit,omitempty"`
	CostUSD   float64 `json:"cost_usd"`
	TokensOut int64   `json:"tokens_out"`
	Usage     *Usage  `json:"usage,omitempty"`
	Updated   string  `json:"updated"`
	Notes     string  `json:"notes,omitempty"`
}

// Tombstone is the compact per-archived-task index execution.json.Archived carries — exactly the
// fields the live scheduler (dependency-done resolution) and accounting (whole-project cost
// recompute) read on the resume hot path. Full fidelity for the same task lives in archive.json.
type Tombstone struct {
	ID        string  `json:"id"`
	Summary   string  `json:"summary"`
	Status    Status  `json:"status"`
	Commit    string  `json:"commit,omitempty"`
	CostUSD   float64 `json:"cost_usd"`
	TokensOut int64   `json:"tokens_out"`
	Usage     *Usage  `json:"usage,omitempty"`
}

// ArchiveOptions is the explicit, operator-supplied request: which milestone ids to move out of
// the live docs. There is no "archive everything done" default — the operator names ids, per the
// design's explicit-only hard rule (SC11).
type ArchiveOptions struct {
	MilestoneIDs []string
}

// ArchiveOutcome is what the archive op replaces plan.json/execution.json/archive.json with, plus
// which requested ids actually moved (Archived) vs. were already archived (Skipped — the
// idempotent no-op case: absent from the live plan, already present in the archive doc).
type ArchiveOutcome struct {
	Plan     Plan
	Exec     ExecState
	Archive  ArchiveDoc
	Archived []string
	Skipped  []string
}

// Archive moves every task under each named, wholly-terminal milestone out of the live plan/exec
// docs into the preserved archive doc — the explicit, resume-safe-by-construction operation
// archival-design.md §4 specifies (SC11). It is pure: callers own reading the three docs and
// atomically writing the three results back (main.go's runArchive).
//
// Preconditions, refused with no partial result on violation:
//  1. Terminal-milestone-only. Every task under a named live milestone must be Status.Terminal()
//     (done|superseded); a milestone with any in-progress/blocked/failed/not-started task refuses
//     the WHOLE call (not just that milestone), so the operator fixes the real cause rather than
//     retrying blind into a partially-applied archive.
//  2. No-active-loop. Nothing in ExecState marks a build loop in flight — this is an operational
//     discipline, not a field this function can check. It is enforced by never wiring `archive`
//     into next/batch/the SKILL.md build loop, which never call it (archival-design.md §4).
//
// Re-naming an id already present in the archive doc (and therefore already absent from the live
// plan) is a no-op for that id (Skipped), not an error — archiving is idempotent by construction.
// Naming an id that is neither live nor already archived is a hard error (unknown milestone).
//
// Cost/token truth: the moved tasks' cost_usd/tokens_out are mirrored into the returned exec's
// Archived tombstones, and RunConfig.SpentUSD/TokensOut are recomputed across live Tasks +
// Archived so the whole-project total is unchanged by archiving (recomputeTotals, execution.go).
func Archive(p Plan, ex ExecState, existing ArchiveDoc, opt ArchiveOptions, at string) (ArchiveOutcome, error) {
	if len(opt.MilestoneIDs) == 0 {
		return ArchiveOutcome{}, fmt.Errorf("archive: at least one --milestone id is required (explicit-only; no implicit 'archive everything done')")
	}

	alreadyArchived := map[string]bool{}
	for _, m := range existing.Milestones {
		alreadyArchived[m.ID] = true
	}
	rowByID := map[string]ExecTask{}
	for _, t := range ex.Tasks {
		rowByID[t.ID] = t
	}
	// alreadyTombstoned: task ids already in the tombstone index. Non-empty only on a recovery
	// retry after a crash whose execution.json write landed but whose plan.json write did not —
	// the frozen tombstone is authoritative for those tasks.
	alreadyTombstoned := map[string]Status{}
	for _, x := range ex.Archived {
		alreadyTombstoned[x.ID] = x.Status
	}

	var toArchive []Milestone
	var skipped []string
	requested := map[string]bool{}
	for _, id := range opt.MilestoneIDs {
		if requested[id] {
			continue // de-dupe a repeated --milestone id
		}
		requested[id] = true
		switch mi := findMilestoneIndex(p, id); {
		case mi >= 0:
			toArchive = append(toArchive, p.Milestones[mi])
		case alreadyArchived[id]:
			skipped = append(skipped, id)
		default:
			return ArchiveOutcome{}, fmt.Errorf("archive: milestone %q is not in the live plan and not already archived", id)
		}
	}

	// Precondition 1: every task under every to-be-archived milestone must be terminal. Refuse
	// the whole call (no partial write) on the first violation.
	var blocking []string
	for _, m := range toArchive {
		for _, ph := range m.Phases {
			for _, t := range ph.Tasks {
				st := StatusNotStarted
				if row, ok := rowByID[t.ID]; ok {
					st = row.Status
				} else if ts, ok := alreadyTombstoned[t.ID]; ok {
					st = ts // recovery retry: row already moved to the tombstone index by a prior crashed run
				}
				if !st.Terminal() {
					blocking = append(blocking, fmt.Sprintf("%s (%s)", t.ID, st))
				}
			}
		}
	}
	if len(blocking) > 0 {
		return ArchiveOutcome{}, fmt.Errorf("archive: refused — milestone not wholly terminal, non-terminal task(s): %s", strings.Join(blocking, ", "))
	}

	if len(toArchive) == 0 {
		// Every requested id was already archived: idempotent no-op.
		return ArchiveOutcome{Plan: p, Exec: ex, Archive: existing, Skipped: skipped}, nil
	}

	archiveIDs := map[string]bool{}
	taskIDs := map[string]bool{}
	var archivedIDs []string
	var newGroups []ArchivedMilestone
	var newTombstones []Tombstone
	for _, m := range toArchive {
		archiveIDs[m.ID] = true
		archivedIDs = append(archivedIDs, m.ID)
		// Recovery retry (crash after archive.json landed, before plan.json): this group is already
		// in the archive doc. Still drop it from the live docs (archiveIDs/taskIDs below), but do NOT
		// re-append it — that would duplicate the immutable record. Same idea per-task for tombstones.
		groupAlreadyStored := alreadyArchived[m.ID]
		var phases []ArchivedPhase
		for _, ph := range m.Phases {
			var tasks []ArchivedTask
			for _, t := range ph.Tasks {
				taskIDs[t.ID] = true
				row := rowByID[t.ID] // present + terminal on the normal path (precondition above)
				if !groupAlreadyStored {
					tasks = append(tasks, ArchivedTask{
						Summary: t.Summary, Deliverable: t.Deliverable, Kind: t.Kind.Resolve(),
						Model: t.Model, Effort: t.Effort, Thinking: t.Thinking, TestStrategy: t.TestStrategy,
						Deps: t.Deps, Acceptance: t.Acceptance, FileSurface: t.FileSurface,
						ID: t.ID, Status: row.Status, Test: row.Test, Review: row.Review, Commit: row.Commit,
						CostUSD: row.CostUSD, TokensOut: row.TokensOut, Usage: row.Usage, Updated: row.Updated, Notes: row.Notes,
					})
				}
				if _, tombExists := alreadyTombstoned[t.ID]; !tombExists {
					newTombstones = append(newTombstones, Tombstone{
						ID: t.ID, Summary: row.Summary, Status: row.Status, Commit: row.Commit,
						CostUSD: row.CostUSD, TokensOut: row.TokensOut, Usage: row.Usage,
					})
				}
			}
			if !groupAlreadyStored {
				phases = append(phases, ArchivedPhase{ID: ph.ID, Name: ph.Name, Tasks: tasks})
			}
		}
		if !groupAlreadyStored {
			newGroups = append(newGroups, ArchivedMilestone{ID: m.ID, Name: m.Name, Phases: phases})
		}
	}

	newPlan := p
	newPlan.Milestones = nil
	for _, m := range p.Milestones {
		if !archiveIDs[m.ID] {
			newPlan.Milestones = append(newPlan.Milestones, m)
		}
	}

	newEx := ex
	newEx.Tasks = nil
	for _, t := range ex.Tasks {
		if !taskIDs[t.ID] {
			newEx.Tasks = append(newEx.Tasks, t)
		}
	}
	newEx.Archived = append(append([]Tombstone{}, ex.Archived...), newTombstones...)
	newEx.Updated = at
	recomputeTotals(&newEx)
	// Fresh backing array (not append(ex.Log, ...)) — ex.Log may have spare cap, and writing into
	// it would mutate the caller's shared array even though newEx.Log's own header stays distinct,
	// breaking Archive's documented purity (bh/archive_purity_test.go).
	newEx.Log = append(append([]string{}, ex.Log...), fmt.Sprintf("%s archive — moved %s to archive.json (%d task(s)); live docs shrink to active+pending",
		at, strings.Join(archivedIDs, ", "), len(newTombstones)))

	newArchive := ArchiveDoc{
		Schema:     cmp.Or(existing.Schema, ArchiveSchema),
		Milestones: append(append([]ArchivedMilestone{}, existing.Milestones...), newGroups...),
	}

	return ArchiveOutcome{Plan: newPlan, Exec: newEx, Archive: newArchive, Archived: archivedIDs, Skipped: skipped}, nil
}

func findMilestoneIndex(p Plan, id string) int {
	for i, m := range p.Milestones {
		if m.ID == id {
			return i
		}
	}
	return -1
}
