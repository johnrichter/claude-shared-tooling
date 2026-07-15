package bh

import (
	"fmt"
	"reflect"
	"strings"
)

// ---- level-of-detail retrieval (design SC10; T0 spike) ----
//
// Four selectable levels, coarse to fine, over plan.json and execution.json: outline (every
// entity reduced to id/name/status/deps) -> milestone/phase (one group expanded to its child
// tasks at outline granularity) -> task (one task's full record) -> field (one named field of
// one entity). Every level is a pure projection recomputed from the passed-in Plan/ExecState on
// each call — nothing here is cached or accumulated, so a result is always consistent with the
// canonical doc. Retrieval only ever reads; it never decides task eligibility (next/batch own
// that) and callers must not treat a projection as authoritative for scheduling.
//
// plan.json and execution.json are projected honestly per their own schema: plan.json carries
// deps but no live status; execution.json carries status but no deps, and has no independent
// milestone/phase objects (only plan.json does) — milestone/phase views over execution.json are
// synthesized from the hierarchical task-ID prefix plan.json's IDs establish.

// RetrievalLevel selects retrieval granularity. The set is closed so CLI validation is exhaustive.
type RetrievalLevel string

const (
	LevelOutline   RetrievalLevel = "outline"
	LevelMilestone RetrievalLevel = "milestone"
	LevelPhase     RetrievalLevel = "phase"
	LevelTask      RetrievalLevel = "task"
	LevelField     RetrievalLevel = "field"
)

func (l RetrievalLevel) Known() bool {
	switch l {
	case LevelOutline, LevelMilestone, LevelPhase, LevelTask, LevelField:
		return true
	}
	return false
}

// RetrieveInput selects what a retrieval call projects. Selection grammar (T0 spike): outline
// needs no ID; milestone/phase/task require ID; field requires ID + Field.
type RetrieveInput struct {
	Level RetrievalLevel
	ID    string
	Field string
}

// OutlineEntry is the L1 outline projection: one milestone, phase, or task reduced to its
// identifying fields. Status is populated only from execution.json (plan.json carries no
// status); Deps is populated only for tasks in plan.json (the only doc that carries deps).
type OutlineEntry struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Status Status   `json:"status,omitempty"`
	Deps   []string `json:"deps,omitempty"`
}

// GroupView is the L2 milestone/phase projection: the group's own id (+ name, plan.json only)
// plus its descendant tasks flattened to L1 outline entries. A milestone flattens across all its
// phases — the phase level is not repeated as an intermediate row (T0 spike: "expanded to its
// child tasks at L1 granularity").
type GroupView struct {
	ID    string         `json:"id"`
	Name  string         `json:"name,omitempty"`
	Tasks []OutlineEntry `json:"tasks"`
}

// FieldValue is the L4 field projection: exactly one named field of one entity, in a
// self-describing envelope so the caller never has to guess the value's shape.
type FieldValue struct {
	ID    string `json:"id"`
	Field string `json:"field"`
	Value any    `json:"value"`
}

// milestoneFields / phaseFields expose only a milestone/phase's own scalar fields to
// fieldByJSONTag — never its nested Phases/Tasks, which would blow the L4 size budget field
// lookups exist to guarantee (a whole sub-tree is a task/outline/group query, not a field).
type milestoneFields struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type phaseFields struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// taskName is a task's outline label: the short human name (Task.Name, plan.json).
func taskName(t Task) string { return t.Name }

// entityKind classifies id by its hierarchical shape (plan.go's ID regexes): "milestone"
// (M<n>), "phase" (M<n>.P<n>), "task" (M<n>.P<n>.T<n>), or "" if none match.
func entityKind(id string) string {
	switch {
	case reTask.MatchString(id):
		return "task"
	case rePhase.MatchString(id):
		return "phase"
	case reMilestone.MatchString(id):
		return "milestone"
	default:
		return ""
	}
}

// cloneStrings returns an independent copy of s (nil stays nil) so a returned projection never
// aliases the canonical Plan/ExecState's backing array — mutating a retrieved slice must not
// reach back into the source (read-only acceptance criterion).
func cloneStrings(s []string) []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}

// cloneFileSurface deep-copies a typed file_surface slice (FileSurfaceEntry is all value fields,
// so a plain copy per element is a full deep copy — no nested slices/maps to alias).
func cloneFileSurface(s []FileSurfaceEntry) []FileSurfaceEntry {
	if s == nil {
		return nil
	}
	out := make([]FileSurfaceEntry, len(s))
	copy(out, s)
	return out
}

// cloneValue defends the field-level projection's read-only contract for slice/map-typed field
// values: a fresh backing store is returned so a caller mutating the retrieved value cannot
// corrupt the canonical doc. Scalars (strings, enums, numbers, bools) are immutable and pass
// through unchanged. Copies are one level deep — the entities retrieval exposes at field level
// carry only scalar and []string fields, never nested mutable structures.
func cloneValue(v any) any {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice:
		if rv.IsNil() {
			return v
		}
		cp := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
		reflect.Copy(cp, rv)
		return cp.Interface()
	case reflect.Map:
		if rv.IsNil() {
			return v
		}
		cp := reflect.MakeMap(rv.Type())
		for _, k := range rv.MapKeys() {
			cp.SetMapIndex(k, rv.MapIndex(k))
		}
		return cp.Interface()
	default:
		return v
	}
}

// cloneTask returns t with its slice-typed fields deep-copied, so a task-level projection shares
// no backing storage with the canonical Plan (read-only acceptance criterion).
func cloneTask(t Task) Task {
	t.Deps = cloneStrings(t.Deps)
	t.Acceptance = cloneStrings(t.Acceptance)
	t.FileSurface = cloneFileSurface(t.FileSurface)
	return t
}

// fieldByJSONTag returns the value of v's struct field whose `json:"name[,opts]"` tag matches
// field exactly (options ignored, "-" excluded). One generic accessor means each entity's
// retrievable field set is defined once, by its own struct tags — never duplicated in a
// hand-maintained field-name switch that could drift from the schema. Slice/map values are
// copied (cloneValue) so the projection never aliases the canonical doc.
func fieldByJSONTag(v any, field string) (any, bool) {
	rv := reflect.ValueOf(v)
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		sf := rt.Field(i)
		tag := sf.Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			name = sf.Name
		}
		if name == "-" || name != field {
			continue
		}
		return cloneValue(rv.Field(i).Interface()), true
	}
	return nil, false
}

// ---- plan.json ----

func findMilestonePlan(p Plan, id string) (Milestone, bool) {
	for _, m := range p.Milestones {
		if m.ID == id {
			return m, true
		}
	}
	return Milestone{}, false
}

func findPhasePlan(p Plan, id string) (Phase, bool) {
	for _, m := range p.Milestones {
		for _, ph := range m.Phases {
			if ph.ID == id {
				return ph, true
			}
		}
	}
	return Phase{}, false
}

func findTaskPlan(p Plan, id string) (Task, bool) {
	for _, r := range WalkTasks(p) {
		if r.Task.ID == id {
			return r.Task, true
		}
	}
	return Task{}, false
}

// planOutline projects plan.json's L1 outline: every milestone, phase, and task in document
// (traversal) order.
func planOutline(p Plan) []OutlineEntry {
	var out []OutlineEntry
	for _, m := range p.Milestones {
		out = append(out, OutlineEntry{ID: m.ID, Name: m.Name})
		for _, ph := range m.Phases {
			out = append(out, OutlineEntry{ID: ph.ID, Name: ph.Name})
			for _, t := range ph.Tasks {
				out = append(out, OutlineEntry{ID: t.ID, Name: taskName(t), Deps: cloneStrings(t.Deps)})
			}
		}
	}
	return out
}

// planGroup projects plan.json's L2 milestone/phase view. kind pins which shape id must be
// ("milestone" or "phase") so a mismatched id (e.g. a phase id under --level milestone) is a
// clear error rather than a silent miss.
func planGroup(p Plan, id, kind string) (GroupView, error) {
	if id == "" {
		return GroupView{}, fmt.Errorf("retrieve: level %q requires --id", kind)
	}
	if entityKind(id) != kind {
		return GroupView{}, fmt.Errorf("retrieve: %q is not a %s id", id, kind)
	}
	switch kind {
	case "milestone":
		m, ok := findMilestonePlan(p, id)
		if !ok {
			return GroupView{}, fmt.Errorf("retrieve: milestone %q not found in plan", id)
		}
		gv := GroupView{ID: m.ID, Name: m.Name}
		for _, ph := range m.Phases {
			for _, t := range ph.Tasks {
				gv.Tasks = append(gv.Tasks, OutlineEntry{ID: t.ID, Name: taskName(t), Deps: cloneStrings(t.Deps)})
			}
		}
		return gv, nil
	default: // "phase"
		ph, ok := findPhasePlan(p, id)
		if !ok {
			return GroupView{}, fmt.Errorf("retrieve: phase %q not found in plan", id)
		}
		gv := GroupView{ID: ph.ID, Name: ph.Name}
		for _, t := range ph.Tasks {
			gv.Tasks = append(gv.Tasks, OutlineEntry{ID: t.ID, Name: taskName(t), Deps: cloneStrings(t.Deps)})
		}
		return gv, nil
	}
}

// planField projects plan.json's L4 single-field view for a milestone, phase, or task id.
func planField(p Plan, id, field string) (FieldValue, error) {
	if id == "" || field == "" {
		return FieldValue{}, fmt.Errorf("retrieve: level %q requires --id and --field", LevelField)
	}
	var (
		v  any
		ok bool
	)
	switch entityKind(id) {
	case "task":
		t, found := findTaskPlan(p, id)
		if !found {
			return FieldValue{}, fmt.Errorf("retrieve: task %q not found in plan", id)
		}
		v, ok = fieldByJSONTag(t, field)
	case "phase":
		ph, found := findPhasePlan(p, id)
		if !found {
			return FieldValue{}, fmt.Errorf("retrieve: phase %q not found in plan", id)
		}
		v, ok = fieldByJSONTag(phaseFields{ID: ph.ID, Name: ph.Name}, field)
	case "milestone":
		m, found := findMilestonePlan(p, id)
		if !found {
			return FieldValue{}, fmt.Errorf("retrieve: milestone %q not found in plan", id)
		}
		v, ok = fieldByJSONTag(milestoneFields{ID: m.ID, Name: m.Name}, field)
	default:
		return FieldValue{}, fmt.Errorf("retrieve: %q is not a recognized milestone/phase/task id", id)
	}
	if !ok {
		return FieldValue{}, fmt.Errorf("retrieve: %s %q has no field %q", entityKind(id), id, field)
	}
	return FieldValue{ID: id, Field: field, Value: v}, nil
}

// RetrievePlan projects Plan p at the requested level. This — and RetrieveExec — is the whole
// level-of-detail retrieval API: a pure, deterministic read of p, never a decision about task
// eligibility (next/batch own that) and never a mutation of p.
func RetrievePlan(p Plan, in RetrieveInput) (any, error) {
	switch in.Level {
	case LevelOutline:
		if in.ID != "" {
			return nil, fmt.Errorf("retrieve: --id is not used with level %q", LevelOutline)
		}
		return planOutline(p), nil
	case LevelMilestone:
		return planGroup(p, in.ID, "milestone")
	case LevelPhase:
		return planGroup(p, in.ID, "phase")
	case LevelTask:
		if in.ID == "" {
			return nil, fmt.Errorf("retrieve: level %q requires --id", LevelTask)
		}
		t, ok := findTaskPlan(p, in.ID)
		if !ok {
			return nil, fmt.Errorf("retrieve: task %q not found in plan", in.ID)
		}
		return cloneTask(t), nil
	case LevelField:
		return planField(p, in.ID, in.Field)
	default:
		return nil, fmt.Errorf("retrieve: unknown level %q (want %s|%s|%s|%s|%s)", in.Level, LevelOutline, LevelMilestone, LevelPhase, LevelTask, LevelField)
	}
}

// ---- execution.json ----
//
// execution.json has no independent milestone/phase objects or names — only its flat task list.
// Milestone/phase views are synthesized by grouping on the hierarchical task-ID prefix
// (M<n>.P<n>.T<n>) that plan.json's IDs establish.

// execOutline projects execution.json's L1 outline: one entry per live task row, in document
// (execution.json array) order.
func execOutline(ex ExecState) []OutlineEntry {
	out := make([]OutlineEntry, 0, len(ex.Tasks))
	for _, t := range ex.Tasks {
		out = append(out, OutlineEntry{ID: t.ID, Name: t.Summary, Status: t.Status})
	}
	return out
}

// execGroup synthesizes execution.json's L2 milestone/phase view from the flat task list's
// ID-prefix hierarchy. Requires at least one task under the group id, so a typo'd id is a clear
// error rather than a silently empty group.
func execGroup(ex ExecState, id, kind string) (GroupView, error) {
	if id == "" {
		return GroupView{}, fmt.Errorf("retrieve: level %q requires --id", kind)
	}
	if entityKind(id) != kind {
		return GroupView{}, fmt.Errorf("retrieve: %q is not a %s id", id, kind)
	}
	prefix := id + "."
	gv := GroupView{ID: id}
	for _, t := range ex.Tasks {
		if strings.HasPrefix(t.ID, prefix) {
			gv.Tasks = append(gv.Tasks, OutlineEntry{ID: t.ID, Name: t.Summary, Status: t.Status})
		}
	}
	if len(gv.Tasks) == 0 {
		return GroupView{}, fmt.Errorf("retrieve: no tasks found under %s %q in execution state", kind, id)
	}
	return gv, nil
}

// execField projects execution.json's L4 single-field view. Task-only: execution.json carries
// no milestone/phase-scoped data (see execGroup), so a milestone/phase id here is an error
// pointing the caller at plan.json instead.
func execField(ex ExecState, id, field string) (FieldValue, error) {
	if id == "" || field == "" {
		return FieldValue{}, fmt.Errorf("retrieve: level %q requires --id and --field", LevelField)
	}
	if entityKind(id) != "task" {
		return FieldValue{}, fmt.Errorf("retrieve: execution.json has no milestone/phase-scoped fields (id %q) — query plan.json for milestone/phase fields", id)
	}
	for _, t := range ex.Tasks {
		if t.ID == id {
			v, ok := fieldByJSONTag(t, field)
			if !ok {
				return FieldValue{}, fmt.Errorf("retrieve: task %q has no field %q", id, field)
			}
			return FieldValue{ID: id, Field: field, Value: v}, nil
		}
	}
	return FieldValue{}, fmt.Errorf("retrieve: task %q not found in execution state", id)
}

// RetrieveExec projects ExecState ex at the requested level — the execution.json counterpart to
// RetrievePlan, with the same read-only, deterministic, non-eligibility-deciding contract.
func RetrieveExec(ex ExecState, in RetrieveInput) (any, error) {
	switch in.Level {
	case LevelOutline:
		if in.ID != "" {
			return nil, fmt.Errorf("retrieve: --id is not used with level %q", LevelOutline)
		}
		return execOutline(ex), nil
	case LevelMilestone:
		return execGroup(ex, in.ID, "milestone")
	case LevelPhase:
		return execGroup(ex, in.ID, "phase")
	case LevelTask:
		if in.ID == "" {
			return nil, fmt.Errorf("retrieve: level %q requires --id", LevelTask)
		}
		for _, t := range ex.Tasks {
			if t.ID == in.ID {
				return t, nil
			}
		}
		return nil, fmt.Errorf("retrieve: task %q not found in execution state", in.ID)
	case LevelField:
		return execField(ex, in.ID, in.Field)
	default:
		return nil, fmt.Errorf("retrieve: unknown level %q (want %s|%s|%s|%s|%s)", in.Level, LevelOutline, LevelMilestone, LevelPhase, LevelTask, LevelField)
	}
}

// ---- archive.json (archival-design.md §3) ----
//
// archive.json stores, per archived task, the union of its plan-slice and exec-slice in one
// ArchivedTask record (archive.go). So unlike execution.json's synthesized milestone/phase view,
// RetrieveArchive projects all four levels the same way RetrievePlan does — archive.json keeps
// its own milestone/phase grouping (ArchivedMilestone/ArchivedPhase) rather than deriving it from
// an ID prefix — plus a task level that returns the full merged plan+exec record in one struct
// (the T0 spike's L3 shape, already unified at archive time).

func findArchivedMilestone(a ArchiveDoc, id string) (ArchivedMilestone, bool) {
	for _, m := range a.Milestones {
		if m.ID == id {
			return m, true
		}
	}
	return ArchivedMilestone{}, false
}

func findArchivedPhase(a ArchiveDoc, id string) (ArchivedPhase, bool) {
	for _, m := range a.Milestones {
		for _, ph := range m.Phases {
			if ph.ID == id {
				return ph, true
			}
		}
	}
	return ArchivedPhase{}, false
}

func findArchivedTask(a ArchiveDoc, id string) (ArchivedTask, bool) {
	for _, m := range a.Milestones {
		for _, ph := range m.Phases {
			for _, t := range ph.Tasks {
				if t.ID == id {
					return t, true
				}
			}
		}
	}
	return ArchivedTask{}, false
}

// cloneArchivedTask deep-copies t's slice-typed fields so a task-level projection shares no
// backing storage with the canonical ArchiveDoc (same read-only contract as cloneTask).
func cloneArchivedTask(t ArchivedTask) ArchivedTask {
	t.Deps = cloneStrings(t.Deps)
	t.Acceptance = cloneStrings(t.Acceptance)
	t.FileSurface = cloneFileSurface(t.FileSurface)
	return t
}

// archiveOutline projects archive.json's L1 outline: every archived milestone, phase, and task
// in document order, identically shaped to planOutline (status/deps included since archive.json
// carries both).
func archiveOutline(a ArchiveDoc) []OutlineEntry {
	var out []OutlineEntry
	for _, m := range a.Milestones {
		out = append(out, OutlineEntry{ID: m.ID, Name: m.Name})
		for _, ph := range m.Phases {
			out = append(out, OutlineEntry{ID: ph.ID, Name: ph.Name})
			for _, t := range ph.Tasks {
				out = append(out, OutlineEntry{ID: t.ID, Name: t.Summary, Status: t.Status, Deps: cloneStrings(t.Deps)})
			}
		}
	}
	return out
}

// archiveGroup projects archive.json's L2 milestone/phase view, flattening across phases at the
// milestone level exactly like planGroup.
func archiveGroup(a ArchiveDoc, id, kind string) (GroupView, error) {
	if id == "" {
		return GroupView{}, fmt.Errorf("retrieve: level %q requires --id", kind)
	}
	if entityKind(id) != kind {
		return GroupView{}, fmt.Errorf("retrieve: %q is not a %s id", id, kind)
	}
	switch kind {
	case "milestone":
		m, ok := findArchivedMilestone(a, id)
		if !ok {
			return GroupView{}, fmt.Errorf("retrieve: milestone %q not found in archive", id)
		}
		gv := GroupView{ID: m.ID, Name: m.Name}
		for _, ph := range m.Phases {
			for _, t := range ph.Tasks {
				gv.Tasks = append(gv.Tasks, OutlineEntry{ID: t.ID, Name: t.Summary, Status: t.Status, Deps: cloneStrings(t.Deps)})
			}
		}
		return gv, nil
	default: // "phase"
		ph, ok := findArchivedPhase(a, id)
		if !ok {
			return GroupView{}, fmt.Errorf("retrieve: phase %q not found in archive", id)
		}
		gv := GroupView{ID: ph.ID, Name: ph.Name}
		for _, t := range ph.Tasks {
			gv.Tasks = append(gv.Tasks, OutlineEntry{ID: t.ID, Name: t.Summary, Status: t.Status, Deps: cloneStrings(t.Deps)})
		}
		return gv, nil
	}
}

// archiveField projects archive.json's L4 single-field view for a milestone, phase, or task id.
func archiveField(a ArchiveDoc, id, field string) (FieldValue, error) {
	if id == "" || field == "" {
		return FieldValue{}, fmt.Errorf("retrieve: level %q requires --id and --field", LevelField)
	}
	var (
		v  any
		ok bool
	)
	switch entityKind(id) {
	case "task":
		t, found := findArchivedTask(a, id)
		if !found {
			return FieldValue{}, fmt.Errorf("retrieve: task %q not found in archive", id)
		}
		v, ok = fieldByJSONTag(t, field)
	case "phase":
		ph, found := findArchivedPhase(a, id)
		if !found {
			return FieldValue{}, fmt.Errorf("retrieve: phase %q not found in archive", id)
		}
		v, ok = fieldByJSONTag(phaseFields{ID: ph.ID, Name: ph.Name}, field)
	case "milestone":
		m, found := findArchivedMilestone(a, id)
		if !found {
			return FieldValue{}, fmt.Errorf("retrieve: milestone %q not found in archive", id)
		}
		v, ok = fieldByJSONTag(milestoneFields{ID: m.ID, Name: m.Name}, field)
	default:
		return FieldValue{}, fmt.Errorf("retrieve: %q is not a recognized milestone/phase/task id", id)
	}
	if !ok {
		return FieldValue{}, fmt.Errorf("retrieve: %s %q has no field %q", entityKind(id), id, field)
	}
	return FieldValue{ID: id, Field: field, Value: v}, nil
}

// RetrieveArchive projects ArchiveDoc a at the requested level — the archive.json counterpart to
// RetrievePlan/RetrieveExec, same read-only/deterministic/non-eligibility-deciding contract. This
// is the SC10 API's path to archived detail (archival-design.md §3): the caller names archive.json
// explicitly; there is no transparent fall-through from a live-doc retrieve call.
func RetrieveArchive(a ArchiveDoc, in RetrieveInput) (any, error) {
	switch in.Level {
	case LevelOutline:
		if in.ID != "" {
			return nil, fmt.Errorf("retrieve: --id is not used with level %q", LevelOutline)
		}
		return archiveOutline(a), nil
	case LevelMilestone:
		return archiveGroup(a, in.ID, "milestone")
	case LevelPhase:
		return archiveGroup(a, in.ID, "phase")
	case LevelTask:
		if in.ID == "" {
			return nil, fmt.Errorf("retrieve: level %q requires --id", LevelTask)
		}
		t, ok := findArchivedTask(a, in.ID)
		if !ok {
			return nil, fmt.Errorf("retrieve: task %q not found in archive", in.ID)
		}
		return cloneArchivedTask(t), nil
	case LevelField:
		return archiveField(a, in.ID, in.Field)
	default:
		return nil, fmt.Errorf("retrieve: unknown level %q (want %s|%s|%s|%s|%s)", in.Level, LevelOutline, LevelMilestone, LevelPhase, LevelTask, LevelField)
	}
}
