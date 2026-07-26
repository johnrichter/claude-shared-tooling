package ledger

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSmoke_AddListPartitionPersist is a quick end-to-end pass over Add, List, Partition, and
// the JSON+Markdown pairing. The adversarial suite is the test-engineer's stage; this just
// confirms the happy path wires together before hand-off.
func TestSmoke_AddListPartitionPersist(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "ledger.json")
	mdPath := filepath.Join(dir, "ledger.md")

	l, err := Open(jsonPath, mdPath, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	low, err := l.Add("minor cosmetic issue", 1, 2)
	if err != nil {
		t.Fatalf("Add low: %v", err)
	}
	if low.ID == "" || low.Criticality != 2 {
		t.Fatalf("low entry not derived correctly: %+v", low)
	}

	high, err := l.Add("data loss on restart", 5, 5)
	if err != nil {
		t.Fatalf("Add high: %v", err)
	}
	if high.Criticality != 25 {
		t.Fatalf("expected multiplicative criticality 25, got %d", high.Criticality)
	}
	if high.ID == low.ID {
		t.Fatalf("ids must be unique, both entries got %q", high.ID)
	}

	if _, err := os.Stat(jsonPath); err != nil {
		t.Fatalf("canonical JSON not written: %v", err)
	}
	if _, err := os.Stat(mdPath); err != nil {
		t.Fatalf("Markdown mirror not written: %v", err)
	}

	ranked := l.List()
	if len(ranked) != 2 || ranked[0].ID != high.ID {
		t.Fatalf("List not ranked by descending criticality: %+v", ranked)
	}

	filtered := l.List(MinCriticality(10))
	if len(filtered) != 1 || filtered[0].ID != high.ID {
		t.Fatalf("MinCriticality filter did not compose correctly: %+v", filtered)
	}

	actNow, deferred := Partition(l.List(), 10)
	if len(actNow)+len(deferred) != len(l.List()) {
		t.Fatalf("partition not total: actNow=%d deferred=%d total=%d", len(actNow), len(deferred), len(l.List()))
	}
	if len(actNow) != 1 || actNow[0].ID != high.ID {
		t.Fatalf("expected only the high-criticality entry act-now, got %+v", actNow)
	}
	if len(deferred) != 1 || deferred[0].ID != low.ID {
		t.Fatalf("expected the low-criticality entry deferred, got %+v", deferred)
	}

	reopened, err := Open(jsonPath, mdPath, 0)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if len(reopened.List()) != 2 {
		t.Fatalf("reopened ledger lost entries: %+v", reopened.List())
	}
}

// TestSmoke_AddRejectsInvalidInput confirms Add validates before deriving or writing anything.
func TestSmoke_AddRejectsInvalidInput(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(filepath.Join(dir, "ledger.json"), filepath.Join(dir, "ledger.md"), 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	cases := []struct {
		name            string
		statement       string
		impact, urgency int
	}{
		{"empty statement", "", 3, 3},
		{"impact too low", "x", 0, 3},
		{"impact too high", "x", 6, 3},
		{"urgency too low", "x", 3, 0},
		{"urgency too high", "x", 3, 6},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := l.Add(c.statement, c.impact, c.urgency); err == nil {
				t.Fatalf("expected validation error for %s", c.name)
			}
		})
	}
	if len(l.List()) != 0 {
		t.Fatalf("invalid Add calls must not append anything, got %+v", l.List())
	}
}
