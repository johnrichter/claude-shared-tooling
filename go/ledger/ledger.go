package ledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/johnrichter/claude-shared-tooling/go/docmirror"
)

// DefaultPerm is the file mode Open uses for both the canonical JSON file and its Markdown
// mirror when a caller has no reason to deviate from it.
const DefaultPerm fs.FileMode = 0o644

// Ledger is an open, in-memory view of one canonical JSON file and its paired Markdown
// mirror. It is not safe for concurrent use from multiple goroutines without external
// synchronization — callers needing that should serialize their own Add calls.
type Ledger struct {
	jsonPath string
	mdPath   string
	perm     fs.FileMode
	doc      Document
}

// Open loads the ledger at jsonPath/mdPath, or starts a fresh one if jsonPath does not yet
// exist. perm is the file mode used for both files on every subsequent write; pass 0 to get
// DefaultPerm.
func Open(jsonPath, mdPath string, perm fs.FileMode) (*Ledger, error) {
	if perm == 0 {
		perm = DefaultPerm
	}
	l := &Ledger{
		jsonPath: jsonPath,
		mdPath:   mdPath,
		perm:     perm,
		doc:      Document{Schema: SchemaVersion},
	}

	raw, err := os.ReadFile(jsonPath)
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, &SchemaError{Path: jsonPath, Err: err}
	}

	var doc Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, &SchemaError{Path: jsonPath, Err: fmt.Errorf("not valid JSON: %w", err)}
	}
	if doc.Schema != SchemaVersion {
		return nil, &SchemaError{Path: jsonPath, Err: fmt.Errorf("declares schema %q, this package reads %q", doc.Schema, SchemaVersion)}
	}
	l.doc = doc
	return l, nil
}

// Add validates statement, impact, and urgency, derives an id and a criticality score
// (impact x urgency), appends the resulting entry, and persists the ledger's canonical JSON
// plus its Markdown mirror in one atomic pair. Neither id nor criticality is a parameter here
// — there is no way for a caller to supply either; both are always computed by this function.
//
// On any validation or write failure, the ledger's in-memory state and on-disk files are left
// exactly as they were before the call — nothing is partially appended.
func (l *Ledger) Add(statement string, impact, urgency int) (Entry, error) {
	statement = strings.TrimSpace(statement)
	if statement == "" {
		return Entry{}, &ValidationError{Field: "statement", Message: "must not be empty"}
	}
	if impact < MinScore || impact > MaxScore {
		return Entry{}, &ValidationError{Field: "impact", Message: fmt.Sprintf("must be between %d and %d, got %d", MinScore, MaxScore, impact)}
	}
	if urgency < MinScore || urgency > MaxScore {
		return Entry{}, &ValidationError{Field: "urgency", Message: fmt.Sprintf("must be between %d and %d, got %d", MinScore, MaxScore, urgency)}
	}

	entry := Entry{
		ID:          nextID(l.doc.Entries),
		Statement:   statement,
		Impact:      impact,
		Urgency:     urgency,
		Criticality: impact * urgency,
		Added:       time.Now().UTC().Format(time.RFC3339),
	}

	entries := append(append([]Entry(nil), l.doc.Entries...), entry)
	if err := l.persist(entries); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

// persist writes entries as l's new document state (canonical JSON plus Markdown mirror, one
// atomic pair) and, only on success, makes it l's in-memory state — the same
// validate-then-write-then-commit shape Add already uses, shared here so Resolve, Retract, and
// Recur leave l untouched on any write failure too.
func (l *Ledger) persist(entries []Entry) error {
	next := Document{Schema: SchemaVersion, Entries: entries}
	if err := docmirror.WritePair(l.jsonPath, l.mdPath, next, mirrorTemplate, l.perm); err != nil {
		return err
	}
	l.doc = next
	return nil
}

// nextID derives the next entry id from how many entries already exist. Ids are assigned by
// append position, never reused, and never influenced by caller input.
func nextID(existing []Entry) string {
	return fmt.Sprintf("ENTRY-%04d", len(existing)+1)
}

// indexOf returns the position of the entry with the given id, or -1 if none exists.
func (l *Ledger) indexOf(id string) int {
	for i, e := range l.doc.Entries {
		if e.ID == id {
			return i
		}
	}
	return -1
}

// Resolve marks entry id with resolution and its REQUIRED supporting citation, then persists
// the ledger. resolution must be one of the four non-retract outcomes (Closed, FixedLive,
// Carried, Stopgap); Retract is the dedicated path for the fifth, since retraction always
// carries the extra refuting-evidence/superseded-id relation a bare resolution does not.
//
// citation is validated before anything is written — an unknown kind, an empty value, or a
// prose value is refused, and l's on-disk state is left exactly as it was.
//
// Resolve also refuses an id already Retracted: retraction is terminal and distinct from the
// other four outcomes, so an ordinary Resolve call must not silently promote a refuted entry
// back into active resolution — that would erase the "never held" fact Retract recorded.
// Retract is the only path that can act on an already-retracted entry.
func (l *Ledger) Resolve(id string, resolution Resolution, citation Citation) (Entry, error) {
	if resolution == ResolutionRetracted {
		return Entry{}, &ValidationError{Field: "resolution", Message: "retracted must go through Retract, which also records the refuting evidence and superseded entry id"}
	}
	if !resolution.Known() {
		return Entry{}, &ValidationError{Field: "resolution", Message: fmt.Sprintf("unknown resolution %q", string(resolution))}
	}
	if err := citation.Validate(); err != nil {
		return Entry{}, err
	}

	idx := l.indexOf(id)
	if idx < 0 {
		return Entry{}, &NotFoundError{ID: id}
	}
	if l.doc.Entries[idx].Resolution == ResolutionRetracted {
		return Entry{}, &ValidationError{Field: "id", Message: fmt.Sprintf("entry %q is retracted, cannot resolve — retraction is terminal", id)}
	}

	entries := append([]Entry(nil), l.doc.Entries...)
	entries[idx].Resolution = resolution
	entries[idx].Citation = citation

	if err := l.persist(entries); err != nil {
		return Entry{}, err
	}
	return entries[idx], nil
}

// Retract marks entry id Retracted — a refuted entry that stops ranking among live work.
// refutingEvidence is the REQUIRED citation showing why the entry never held, and
// supersededEntryID is the REQUIRED id of the entry that replaces it; both are validated
// before anything is written and carried on the resulting Retraction relation.
func (l *Ledger) Retract(id string, refutingEvidence Citation, supersededEntryID string) (Entry, error) {
	retraction := Retraction{RefutingEvidence: refutingEvidence, SupersededEntryID: supersededEntryID}
	if err := retraction.Validate(); err != nil {
		return Entry{}, err
	}

	idx := l.indexOf(id)
	if idx < 0 {
		return Entry{}, &NotFoundError{ID: id}
	}

	entries := append([]Entry(nil), l.doc.Entries...)
	entries[idx].Resolution = ResolutionRetracted
	entries[idx].Citation = refutingEvidence
	entries[idx].Retraction = retraction

	if err := l.persist(entries); err != nil {
		return Entry{}, err
	}
	return entries[idx], nil
}

// Recur records that entry id has reached planning cycle cycle still unconsumed (no
// resolution yet), incrementing its recurrence counter — the signal that makes an unspent
// register entry visible rather than merely regrettable. Recur is idempotent per cycle: any
// cycle the entry has already recurred in is a no-op, even one revisited out of order, so
// replaying a planning pass cannot inflate the count. Only a cycle not seen before increments,
// and the counter never decreases.
//
// Recur refuses an id that already carries a resolution — recurrence tracks live exposure,
// not resolved history.
func (l *Ledger) Recur(id, cycle string) (Entry, error) {
	cycle = strings.TrimSpace(cycle)
	if cycle == "" {
		return Entry{}, &ValidationError{Field: "cycle", Message: "must not be empty"}
	}

	idx := l.indexOf(id)
	if idx < 0 {
		return Entry{}, &NotFoundError{ID: id}
	}
	entry := l.doc.Entries[idx]
	if entry.Resolution.Known() {
		return Entry{}, &ValidationError{Field: "id", Message: fmt.Sprintf("entry %q is already resolved (%s), cannot recur", id, entry.Resolution)}
	}
	for _, seen := range entry.RecurCycles {
		if seen == cycle {
			return entry, nil
		}
	}

	entries := append([]Entry(nil), l.doc.Entries...)
	entries[idx].RecurCycles = append(append([]string(nil), entry.RecurCycles...), cycle)
	entries[idx].Recurrence++

	if err := l.persist(entries); err != nil {
		return Entry{}, err
	}
	return entries[idx], nil
}

// Filter narrows a List read; multiple filters compose with AND semantics.
type Filter func(Entry) bool

// MinCriticality keeps entries whose criticality is at least min.
func MinCriticality(min int) Filter {
	return func(e Entry) bool { return e.Criticality >= min }
}

// MinImpact keeps entries whose impact is at least min.
func MinImpact(min int) Filter {
	return func(e Entry) bool { return e.Impact >= min }
}

// MinUrgency keeps entries whose urgency is at least min.
func MinUrgency(min int) Filter {
	return func(e Entry) bool { return e.Urgency >= min }
}

// List returns every entry passing all of filters (AND) whose resolution is not Retracted,
// ranked by descending criticality; entries tied on criticality keep their append order
// (stable sort) so List's output is deterministic across repeated calls against the same
// ledger state. No filters returns every live entry, ranked.
//
// A Retracted entry never appears here — a refuted entry does not rank among live work.
// ListWithRetracted is the explicit opt-in read for a caller that wants it back.
func (l *Ledger) List(filters ...Filter) []Entry {
	return l.list(false, filters)
}

// ListWithRetracted behaves like List but also admits Retracted entries into the ranked
// output. It exists so a refuted entry stays readable on request without ever ranking among
// live work by default.
func (l *Ledger) ListWithRetracted(filters ...Filter) []Entry {
	return l.list(true, filters)
}

func (l *Ledger) list(includeRetracted bool, filters []Filter) []Entry {
	out := make([]Entry, 0, len(l.doc.Entries))
	for _, e := range l.doc.Entries {
		if !includeRetracted && e.Resolution == ResolutionRetracted {
			continue
		}
		keep := true
		for _, f := range filters {
			if !f(e) {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Criticality > out[j].Criticality
	})
	return out
}

// Partition splits entries into actNow (criticality >= threshold) and deferred (criticality <
// threshold). The split is total, lossless, and exactly-once: a single pass, each entry's
// criticality read once, and each entry appended to exactly one of the two returned slices —
// len(actNow)+len(deferred) always equals len(entries).
//
// Partition itself is oblivious to Resolution — a Retracted entry passed to it still lands in
// actNow or deferred by criticality alone. Retracted entries stay out of the act-now partition
// because List, the usual source of entries for this call, already excludes them; see
// (*Ledger).Partition for the ledger-backed call that wires the two together.
func Partition(entries []Entry, threshold int) (actNow, deferred []Entry) {
	for _, e := range entries {
		if e.Criticality >= threshold {
			actNow = append(actNow, e)
		} else {
			deferred = append(deferred, e)
		}
	}
	return actNow, deferred
}

// Partition splits l's live-ranked entries (List's output — Retracted excluded) into actNow
// and deferred at threshold. Retracted entries are never in either bucket here, but are still
// counted in Rollup — the split stays total and lossless across all three buckets combined.
func (l *Ledger) Partition(threshold int) (actNow, deferred []Entry) {
	return Partition(l.List(), threshold)
}

// Rollup is a total, lossless three-way accounting of a ledger's entries: ActNow+Deferred is
// the live (non-retracted) count, and Retracted is counted alongside it rather than dropped —
// so a caller reading only List/Partition's live-ranked view can still confirm every entry the
// ledger ever held is accounted for somewhere. ActNow+Deferred+Retracted always equals Total.
type Rollup struct {
	ActNow    int
	Deferred  int
	Retracted int
	Total     int
}

// Rollup reports how l's entries currently divide across act-now, deferred, and retracted at
// the given criticality threshold — the same threshold Partition would use for the live split.
func (l *Ledger) Rollup(threshold int) Rollup {
	var r Rollup
	for _, e := range l.doc.Entries {
		r.Total++
		switch {
		case e.Resolution == ResolutionRetracted:
			r.Retracted++
		case e.Criticality >= threshold:
			r.ActNow++
		default:
			r.Deferred++
		}
	}
	return r
}
