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

	next := Document{
		Schema:  SchemaVersion,
		Entries: append(append([]Entry(nil), l.doc.Entries...), entry),
	}

	if err := docmirror.WritePair(l.jsonPath, l.mdPath, next, mirrorTemplate, l.perm); err != nil {
		return Entry{}, err
	}
	l.doc = next
	return entry, nil
}

// nextID derives the next entry id from how many entries already exist. Ids are assigned by
// append position, never reused, and never influenced by caller input.
func nextID(existing []Entry) string {
	return fmt.Sprintf("ENTRY-%04d", len(existing)+1)
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

// List returns every entry passing all of filters (AND), ranked by descending criticality;
// entries tied on criticality keep their append order (stable sort) so List's output is
// deterministic across repeated calls against the same ledger state. No filters returns every
// entry, ranked.
func (l *Ledger) List(filters ...Filter) []Entry {
	out := make([]Entry, 0, len(l.doc.Entries))
	for _, e := range l.doc.Entries {
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
