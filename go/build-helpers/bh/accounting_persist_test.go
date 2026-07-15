package bh

import (
	"path/filepath"
	"strings"
	"testing"
)

// These tests pin: SetAccounting/SetAccountingUnresolved persist orchestrator-only
// O and cost_status onto ExecState.RunConfig.Accounting, and PriceFile's per-file isolation is what
// makes O correct regardless of whether every subagent transcript was discovered.

// TestSetAccounting_PersistsOrchestratorOnlyO pins that SetAccounting derives O from ONLY the main
// transcript's own ledger entry (never the session-wide per-model total, which would also lump in
// same-model subagents) and persists it onto ExecState.RunConfig.Accounting.Orchestrator.
func TestSetAccounting_PersistsOrchestratorOnlyO(t *testing.T) {
	rates := loadTestRates(t)
	mainPath := filepath.Join(accountingDir, "orchestrator.jsonl")
	sources, handles := discoverFixtureSources(t, mainPath)
	defer closeHandles(handles)
	acct, err := Account(nil, sources, rates, "t0")
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	// mainPath is the exact FileID discoverFixtureSources folded into the ledger (openSource sets
	// FileID = the path handed in, unmodified) — production keys the ledger by absClean(transcript)
	// instead, but the isolation contract under test (PriceFile keyed by whatever FileID the caller
	// used to discover+open the main transcript) is identical either way.
	var ex ExecState
	SetAccounting(&ex, acct, mainPath, rates, true, "2026-07-05T00:00:00Z", "2026-07-03", "deadbeef")

	if ex.RunConfig.Accounting == nil || ex.RunConfig.Accounting.Orchestrator == nil {
		t.Fatal("SetAccounting did not persist Orchestrator (O) onto RunConfig.Accounting")
	}
	o := ex.RunConfig.Accounting.Orchestrator
	// O must equal the main-file-only isolation, strictly less than the whole-session grand total
	// (the fixture set includes 4 subagent transcripts contributing additional cost).
	if o.CostUSD <= 0 {
		t.Fatalf("O.CostUSD = %v, want > 0", o.CostUSD)
	}
	if o.CostUSD >= ex.RunConfig.Accounting.CostUSD {
		t.Fatalf("O.CostUSD (%v) must be strictly less than session CostUSD (%v) — a fixture set with subagents present but O not isolated per-file", o.CostUSD, ex.RunConfig.Accounting.CostUSD)
	}
	if ex.RunConfig.Accounting.CostStatus != "" {
		t.Fatalf("cost_status = %q, want \"\" (resolved) on a successful SetAccounting", ex.RunConfig.Accounting.CostStatus)
	}
	if ex.RunConfig.TrueUsage == nil {
		t.Fatal("SetAccounting did not refresh RunConfig.TrueUsage")
	}
}

// TestSetAccounting_IdempotentOnRerun pins resume-safety: calling SetAccounting twice with the
// identical Account() result (as --final's full re-parse would produce on an unchanged transcript)
// must persist byte-identical Accounting state, not accumulate or drift.
func TestSetAccounting_IdempotentOnRerun(t *testing.T) {
	rates := loadTestRates(t)
	mainPath := filepath.Join(accountingDir, "orchestrator.jsonl")

	run := func() ExecState {
		sources, handles := discoverFixtureSources(t, mainPath)
		defer closeHandles(handles)
		acct, err := Account(nil, sources, rates, "t0")
		if err != nil {
			t.Fatalf("Account: %v", err)
		}
		var ex ExecState
		SetAccounting(&ex, acct, mainPath, rates, true, "2026-07-05T00:00:00Z", "2026-07-03", "deadbeef")
		return ex
	}
	a := run()
	b := run()
	if a.RunConfig.Accounting.CostUSD != b.RunConfig.Accounting.CostUSD {
		t.Fatalf("non-idempotent --final rerun: %v vs %v", a.RunConfig.Accounting.CostUSD, b.RunConfig.Accounting.CostUSD)
	}
	if a.RunConfig.Accounting.Orchestrator.CostUSD != b.RunConfig.Accounting.Orchestrator.CostUSD {
		t.Fatalf("non-idempotent O rerun: %v vs %v", a.RunConfig.Accounting.Orchestrator.CostUSD, b.RunConfig.Accounting.Orchestrator.CostUSD)
	}
}

// TestSetAccountingUnresolved_NonFatalMarkerLeavesPriorAccountingUntouched pins the unresolved
// path: the marker is loud (cost_status set, logged) but never erases or "recovers" the last
// known-good O/CostUSD — a transient read failure must only flag that THIS run's snapshot didn't
// update, never silently fall back to a fabricated or zeroed value.
func TestSetAccountingUnresolved_NonFatalMarkerLeavesPriorAccountingUntouched(t *testing.T) {
	rates := loadTestRates(t)
	mainPath := filepath.Join(accountingDir, "orchestrator.jsonl")
	sources, handles := discoverFixtureSources(t, mainPath)
	defer closeHandles(handles)
	acct, err := Account(nil, sources, rates, "t0")
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	var ex ExecState
	SetAccounting(&ex, acct, mainPath, rates, true, "2026-07-05T00:00:00Z", "2026-07-03", "deadbeef")
	priorCost := ex.RunConfig.Accounting.CostUSD
	priorO := ex.RunConfig.Accounting.Orchestrator.CostUSD
	if priorCost <= 0 || priorO <= 0 {
		t.Fatal("fixture setup produced a zero prior — test would not detect silent recovery")
	}

	SetAccountingUnresolved(&ex, "/nonexistent/transcript.jsonl", "2026-07-05T01:00:00Z")

	if ex.RunConfig.Accounting.CostStatus != "unresolved" {
		t.Fatalf("cost_status = %q, want %q", ex.RunConfig.Accounting.CostStatus, "unresolved")
	}
	if ex.RunConfig.Accounting.CostUSD != priorCost {
		t.Fatalf("prior CostUSD silently changed: %v -> %v", priorCost, ex.RunConfig.Accounting.CostUSD)
	}
	if ex.RunConfig.Accounting.Orchestrator == nil || ex.RunConfig.Accounting.Orchestrator.CostUSD != priorO {
		t.Fatalf("prior Orchestrator (O) silently changed or dropped: want %v, got %v", priorO, ex.RunConfig.Accounting.Orchestrator)
	}
	found := false
	for _, l := range ex.Log {
		if strings.Contains(l, "cost_status:unresolved") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a logged cost_status:unresolved note, got log: %v", ex.Log)
	}
}

// TestSetAccountingUnresolved_FreshExecStateNeverFabricatesO pins the from-scratch case (no prior
// Accounting at all — e.g. the very first record-usage --final of a run whose main transcript is
// unreadable): the marker must set cost_status:unresolved WITHOUT fabricating a zero-priced O, so a
// reader can never mistake "we never resolved a transcript" for "the orchestrator genuinely cost $0".
func TestSetAccountingUnresolved_FreshExecStateNeverFabricatesO(t *testing.T) {
	var ex ExecState
	SetAccountingUnresolved(&ex, "/nonexistent/transcript.jsonl", "2026-07-05T00:00:00Z")
	if ex.RunConfig.Accounting == nil {
		t.Fatal("expected Accounting to be allocated so cost_status is observable")
	}
	if ex.RunConfig.Accounting.CostStatus != "unresolved" {
		t.Fatalf("cost_status = %q, want %q", ex.RunConfig.Accounting.CostStatus, "unresolved")
	}
	if ex.RunConfig.Accounting.Orchestrator != nil {
		t.Fatalf("O must stay nil (never resolved), got %+v", ex.RunConfig.Accounting.Orchestrator)
	}
}

// TestPriceFile_UnknownFileIDNotOK pins PriceFile's contract: a fileID with no ledger entry (the
// main transcript could not be read this run) reports ok=false rather than a fabricated zero O —
// this is exactly the signal runRecordUsage's unresolved branch relies on never being silently true.
func TestPriceFile_UnknownFileIDNotOK(t *testing.T) {
	acct := &Accounting{}
	if _, ok := acct.PriceFile("/never/discovered.jsonl", RateTable{}); ok {
		t.Fatal("PriceFile reported ok=true for a fileID absent from the ledger")
	}
}
