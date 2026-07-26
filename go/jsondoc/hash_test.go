package jsondoc

import "testing"

func TestContentHash_KeyOrderInvariant(t *testing.T) {
	h1, err := ContentHash(map[string]any{"spec": "x", "tier": 1})
	if err != nil {
		t.Fatalf("h1: %v", err)
	}
	h2, err := ContentHash(map[string]any{"tier": 1, "spec": "x"})
	if err != nil {
		t.Fatalf("h2: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("key order changed hash: %s vs %s", h1, h2)
	}
}

func TestContentHash_TierOnlyChangeDoesNotAlterHash(t *testing.T) {
	// Simulate a caller that hashes only spec-bearing fields, excluding a
	// priority tier that changes independently.
	type doc struct {
		Spec map[string]any `json:"spec"`
		Tier int            `json:"-"`
	}
	before := doc{Spec: map[string]any{"cpu": 2, "mem": "4Gi"}, Tier: 1}
	after := doc{Spec: map[string]any{"cpu": 2, "mem": "4Gi"}, Tier: 5}

	// fields is the caller's own choice: only Spec, never Tier.
	hBefore, err := ContentHash(map[string]any{"spec": before.Spec})
	if err != nil {
		t.Fatalf("hBefore: %v", err)
	}
	hAfter, err := ContentHash(map[string]any{"spec": after.Spec})
	if err != nil {
		t.Fatalf("hAfter: %v", err)
	}
	if hBefore != hAfter {
		t.Fatalf("tier-only change altered spec-only hash: %s vs %s", hBefore, hAfter)
	}
}

func TestContentHash_SpecChangeAltersHash(t *testing.T) {
	h1, err := ContentHash(map[string]any{"spec": map[string]any{"cpu": 2}})
	if err != nil {
		t.Fatalf("h1: %v", err)
	}
	h2, err := ContentHash(map[string]any{"spec": map[string]any{"cpu": 4}})
	if err != nil {
		t.Fatalf("h2: %v", err)
	}
	if h1 == h2 {
		t.Fatalf("spec change did not alter hash: both %s", h1)
	}
}

func TestContentHash_WhitespaceAndFloatFormatInvariant(t *testing.T) {
	h1, err := ContentHash(map[string]any{"x": 1.500})
	if err != nil {
		t.Fatalf("h1: %v", err)
	}
	h2, err := ContentHash(map[string]any{"x": 1.5})
	if err != nil {
		t.Fatalf("h2: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("float formatting difference altered hash: %s vs %s", h1, h2)
	}
}

func TestContentHash_Deterministic(t *testing.T) {
	fields := map[string]any{"a": 1, "b": []any{1, 2, 3}, "c": map[string]any{"d": true}}
	h1, err := ContentHash(fields)
	if err != nil {
		t.Fatalf("h1: %v", err)
	}
	h2, err := ContentHash(fields)
	if err != nil {
		t.Fatalf("h2: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("identical input produced different hashes across calls: %s vs %s", h1, h2)
	}
}

func TestContentHash_ErrorPropagates(t *testing.T) {
	_, err := ContentHash(map[string]any{"x": make(chan int)})
	if err == nil {
		t.Fatal("expected error for unmarshalable field value, got nil")
	}
}

func TestContentHash_HexFormat(t *testing.T) {
	h, err := ContentHash(map[string]any{"a": 1})
	if err != nil {
		t.Fatalf("ContentHash: %v", err)
	}
	if len(h) != 64 {
		t.Fatalf("expected 64 hex chars (sha256), got %d: %s", len(h), h)
	}
	for _, r := range h {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			t.Fatalf("hash contains non-lowercase-hex char %q in %s", r, h)
		}
	}
}
