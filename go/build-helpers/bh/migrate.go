package bh

import (
	"fmt"
	"regexp"
	"strings"
)

// This file implements `migrate-project` (SC14): upgrading an in-flight v1 project's parsed
// plan.json + execution.json to the v2 harness shapes this project introduced, in place, with an
// exact change report. Like the rest of package bh it is pure — it mutates in-memory values and
// returns a report; the CLI (package main) owns reading, gating the write on --dry-run, and the
// atomic write-back.
//
// The v1->v2 deltas this tool maps, enumerated from the schema diff this project made:
//   - execution.json schema_version stamp (M2.P2.T1) — absent on every pre-v2 file; stamped to
//     CurrentExecSchemaVersion via the canonical, tested MigrateExec upgrade path (never re-derived
//     here). A version newer than this build supports is a hard error, not a silent field drop.
//   - id + name on every entity (SC15) — v1 tasks carry no `name`; v2 requires a short human name
//     distinct from summary. Backfilled deterministically from the summary. Milestones/phases
//     already carried a name in v1; an empty one (a truly old artifact) is backfilled from its id.
//     ExecState.Name (the doc name) is backfilled if empty. ExecTask has no name field in v2, so
//     there is no per-row exec name to add.
//   - accounting / attribution / true_usage fields — added on the run-config and per-task rows this
//     project (SC2). All are nil-safe pointers / omitempty: an absent field on load is the tolerated
//     empty case (Accounting.migrateLegacyLedger handles a nil ledger). They are NEVER fabricated on
//     migrate — synthesising cost/usage data a v1 run never measured would violate losslessness.
//   - plan-schema enum deltas (model / effort / deliverable_kind) — v2 changed these ADDITIVELY
//     (new ids added; none renamed or removed — see plan-schema.json and the Model/Effort sets in
//     types.go). Additive enum changes need no data conversion: every v1 value stays valid. A value
//     outside the current known set is FLAGGED for operator review, never silently rewritten, since
//     no authoritative rename map exists; legacyModelRenames is the single place a future rename
//     would be mapped.
//
// Losslessness contract: only additive fields (entity names) and the execution schema_version stamp
// are ever written. Task status / done / commit-SHA / cost / tokens / test+review verdicts / deps
// and the execution log are NEVER touched — a migrated project resumes with byte-true completed
// work. Idempotency: an already-v2 project yields zero changes (AlreadyV2=true); the CLI then
// rewrites nothing, so a re-run is a true no-op.

// legacyModelRenames maps a retired model id to its v2 replacement so a rename migrates existing
// plan/execution data in one place rather than as scattered special-cases. v2 introduced only
// additive model-enum changes (new ids added; none renamed) so it is currently empty — keep it in
// sync with the Model enum in types.go per rule model-id-enum-set-sync when a rename ever lands.
var legacyModelRenames = map[Model]Model{}

// nameMaxLen bounds a synthesized task name so it stays a short label, not a second summary.
const nameMaxLen = 72

// taskIDPattern extracts a task id (M<n>.P<n>.T<n>) referenced in free text.
var taskIDPattern = regexp.MustCompile(`M[0-9]+\.P[0-9]+\.T[0-9]+`)

// doNotDispatchPattern matches the literal "do not dispatch" hand-note convention (SCe) — the
// exact phrasing the old M3.P1.T1/M7.P1.T1 stopgap used to keep a task off subagent dispatch,
// allowing hyphen/space between words. A forces_pause risk that merely *names* a task by id
// without this phrase is an ordinary build-hazard risk (e.g. a regression or empirical-test risk
// that references the task), NOT a dispatch refusal — matching those would wrongly turn normal
// implementation tasks permanently-undispatchable.
var doNotDispatchPattern = regexp.MustCompile(`(?i)do[\s-]+not[\s-]+dispatch`)

// MigrateChange is one field-level upgrade the migration applied (or, in dry-run, would apply).
// It is the exact, forwardable audit record of what the tool touched.
type MigrateChange struct {
	Target string `json:"target"`         // "plan" | "execution"
	ID     string `json:"id,omitempty"`   // entity id when the change is entity-scoped
	Field  string `json:"field"`          // the field upgraded
	From   string `json:"from"`           // prior value ("" or "absent")
	To     string `json:"to"`             // new value
	Note   string `json:"note,omitempty"` // why the change was made
}

// MigrateReport is the full result of a migrate-project run: whether the project was already v2,
// whether this was a preview, the exact change list, and any operator-review warnings.
type MigrateReport struct {
	AlreadyV2 bool            `json:"already_v2"`
	DryRun    bool            `json:"dry_run"`
	Changes   []MigrateChange `json:"changes"`
	Warnings  []string        `json:"warnings"`
}

// MigrateProject upgrades a parsed plan + execution pair to the v2 harness shapes IN PLACE and
// returns the exact change report. dryRun only records intent on the report (the CLI refuses to
// persist when set); the in-memory values are upgraded either way, so a real run and its dry-run
// preview produce an identical change list. See the file header for the losslessness/idempotency
// contract. Returns an error only when the execution file is a newer schema than this build supports.
func MigrateProject(p *Plan, ex *ExecState, dryRun bool) (MigrateReport, error) {
	rep := MigrateReport{DryRun: dryRun, Changes: []MigrateChange{}, Warnings: []string{}}
	migratePlanInPlace(p, &rep)
	if err := migrateExecInPlace(ex, &rep); err != nil {
		return rep, err
	}
	rep.AlreadyV2 = len(rep.Changes) == 0
	return rep, nil
}

// migratePlanInPlace applies the plan-side v2 deltas: backfill every entity name (SC15) and map any
// retired model id (currently none). Nothing else in the plan is touched — the plan is otherwise an
// immutable build spec.
func migratePlanInPlace(p *Plan, rep *MigrateReport) {
	for mi := range p.Milestones {
		m := &p.Milestones[mi]
		if strings.TrimSpace(m.Name) == "" {
			n := "Milestone " + m.ID
			rep.Changes = append(rep.Changes, MigrateChange{Target: "plan", ID: m.ID, Field: "name", From: "", To: n, Note: "SC15 milestone name backfilled from id"})
			m.Name = n
		}
		for pi := range m.Phases {
			ph := &m.Phases[pi]
			if strings.TrimSpace(ph.Name) == "" {
				n := "Phase " + ph.ID
				rep.Changes = append(rep.Changes, MigrateChange{Target: "plan", ID: ph.ID, Field: "name", From: "", To: n, Note: "SC15 phase name backfilled from id"})
				ph.Name = n
			}
			for ti := range ph.Tasks {
				t := &ph.Tasks[ti]
				if strings.TrimSpace(t.Name) == "" {
					n := nameFromSummary(t.Summary, t.ID)
					rep.Changes = append(rep.Changes, MigrateChange{Target: "plan", ID: t.ID, Field: "name", From: "", To: n, Note: "SC15 task name backfilled from summary"})
					t.Name = n
				}
				migrateModel("plan", t.ID, &t.Model, rep)
			}
		}
	}
	migrateOrchestratorOnly(p, rep)
}

// migrateOrchestratorOnly promotes the pre-M13.P3.T3 "do not dispatch" hand-note convention
// (design.md SCe) into the structural orchestrator_only field: before that field existed, the
// only way to keep a measurement/verdict-gate task off subagent dispatch was a plan-level risk
// marked forces_pause whose risk/mitigation text carried the "do not dispatch <task id>" note (the
// M3.P1.T1/M7.P1.T1 class). A task named by such a note and not already orchestrator_only is
// stamped true here — a design-declared refusal the old convention only enforced by human-read
// prose, now made structural. The match is gated on the do-not-dispatch phrase, not on any
// forces_pause risk that merely references a task by id: an ordinary build-hazard risk (regression,
// empirical-test, or frozen-window risk) commonly names the tasks it concerns, and stamping those
// would wrongly make normal implementation tasks permanently-undispatchable. A task with no such
// hand-note is untouched (defaults stay false).
func migrateOrchestratorOnly(p *Plan, rep *MigrateReport) {
	named := map[string]bool{}
	for _, r := range p.Risks {
		if !r.ForcesPause {
			continue
		}
		text := r.Risk + " " + r.Mitigation
		if !doNotDispatchPattern.MatchString(text) {
			continue
		}
		for _, id := range taskIDPattern.FindAllString(text, -1) {
			named[id] = true
		}
	}
	if len(named) == 0 {
		return
	}
	for mi := range p.Milestones {
		for pi := range p.Milestones[mi].Phases {
			for ti := range p.Milestones[mi].Phases[pi].Tasks {
				t := &p.Milestones[mi].Phases[pi].Tasks[ti]
				if named[t.ID] && !t.OrchestratorOnly {
					rep.Changes = append(rep.Changes, MigrateChange{Target: "plan", ID: t.ID, Field: "orchestrator_only", From: "false", To: "true", Note: "SCe: promoted from a forces_pause hand-note naming this task by id (design-declared do-not-dispatch)"})
					t.OrchestratorOnly = true
				}
			}
		}
	}
}

// migrateExecInPlace applies the execution-side v2 deltas: stamp schema_version via the canonical
// MigrateExec, backfill the doc name, and map any retired per-task model id. Accounting / usage
// fields are left exactly as loaded (absent stays absent — never fabricated).
func migrateExecInPlace(ex *ExecState, rep *MigrateReport) error {
	before := ex.SchemaVersion
	if err := MigrateExec(ex); err != nil {
		return err
	}
	if before != ex.SchemaVersion {
		from := "absent"
		if before != 0 {
			from = fmt.Sprintf("%d", before)
		}
		rep.Changes = append(rep.Changes, MigrateChange{Target: "execution", Field: "schema_version", From: from, To: fmt.Sprintf("%d", ex.SchemaVersion), Note: "stamped current execution schema version"})
	}
	if strings.TrimSpace(ex.Name) == "" {
		n := ex.Project + " — Execution"
		rep.Changes = append(rep.Changes, MigrateChange{Target: "execution", Field: "name", From: "", To: n, Note: "SC15 execution name backfilled"})
		ex.Name = n
	}
	for i := range ex.Tasks {
		migrateModel("execution", ex.Tasks[i].ID, &ex.Tasks[i].Model, rep)
	}
	return nil
}

// migrateModel remaps a retired model id to its v2 replacement (reporting the change) or, for an
// unknown id with no mapping, flags it for operator review rather than rewriting it — the additive-
// enum-delta rule from the file header.
func migrateModel(target, id string, m *Model, rep *MigrateReport) {
	if renamed, ok := legacyModelRenames[*m]; ok {
		rep.Changes = append(rep.Changes, MigrateChange{Target: target, ID: id, Field: "model", From: string(*m), To: string(renamed), Note: "retired model id renamed to v2 replacement"})
		*m = renamed
		return
	}
	if *m != "" && !m.Known() {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s task %s: model %q is not in the v2 selectable set and has no rename mapping — left as-is for operator review", target, id, *m))
	}
}

// nameFromSummary derives a short SC15 task name from the summary, deterministically: the first
// sentence, collapsed to a single line and capped at nameMaxLen runes on a word boundary. An empty
// summary falls back to the id so the result is never empty (validate requires a non-empty name).
func nameFromSummary(summary, fallback string) string {
	s := strings.Join(strings.Fields(summary), " ") // collapse all whitespace to single spaces
	if s == "" {
		return fallback
	}
	if i := strings.Index(s, ". "); i >= 0 { // first sentence break
		s = s[:i]
	} else {
		s = strings.TrimSuffix(s, ".")
	}
	r := []rune(s)
	if len(r) > nameMaxLen {
		cut := nameMaxLen
		for cut > 0 && r[cut] != ' ' { // back off to the last word boundary
			cut--
		}
		if cut == 0 {
			cut = nameMaxLen
		}
		s = strings.TrimRight(string(r[:cut]), " ") + "…"
	}
	return s
}
