package bh

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
)

// This file implements the session-tier self-check (design SC7): both build-with-team and
// plan-with-team self-check their LIVE session tier at Phase 0 against a caller-supplied
// {floor, ceiling} band. The bands themselves are NOT hardcoded here — each skill passes its
// own (plan-with-team's Opus-only floor vs build-with-team's Sonnet-only band) — this package
// only owns the resolve/compare mechanics common to both.

// modelProbe reads just the model off one transcript line, preferring the message-nested shape
// (the common case) over a top-level model field. Deliberately decoupled from usage.go's
// lineProbe/usageRaw: this probe answers "what model wrote this line", not "what did it cost",
// so a future usage-shape change (governed by rule:tooling:usage-parser-fixture-current) cannot
// silently break the self-check, and vice versa.
type modelProbe struct {
	Model   string `json:"model"`
	Message *struct {
		Model string `json:"model"`
	} `json:"message"`
}

func (p modelProbe) resolve() string {
	if p.Message != nil && p.Message.Model != "" {
		return p.Message.Model
	}
	return p.Model
}

// LatestTranscriptModel scans a Claude Code session transcript (JSONL, one object per line) and
// returns the model of the LAST line that names one. This is the live truth for the self-check —
// it reflects a mid-session /model override that $ANTHROPIC_MODEL (a launch-time setting) would
// miss. ok is false when no line names a model (e.g. an empty or pre-first-turn transcript);
// malformed lines are skipped, matching the rest of this package's best-effort transcript parsing.
func LatestTranscriptModel(r io.Reader) (model Model, ok bool) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var p modelProbe
		if json.Unmarshal(line, &p) != nil {
			continue
		}
		if m := p.resolve(); m != "" {
			model, ok = Model(m), true
		}
	}
	return model, ok
}

// ParseSettingsEffort extracts effortLevel from Claude Code settings.json bytes — the static
// effort fallback per SC7 (it reflects the value at session launch, not a later /effort
// override). ok is false when the field is absent, empty, or the bytes do not parse as JSON.
func ParseSettingsEffort(raw []byte) (effort Effort, ok bool) {
	var doc struct {
		EffortLevel string `json:"effortLevel"`
	}
	if json.Unmarshal(raw, &doc) != nil || doc.EffortLevel == "" {
		return "", false
	}
	return Effort(strings.ToLower(doc.EffortLevel)), true
}

// ---- band comparison ----

// TierBand is a caller-supplied floor/ceiling pair for the self-check — SC7's bands are
// parameters both skills pass, never hardcoded here. Both ends are inclusive: at-floor and
// at-ceiling are within band.
type TierBand struct {
	FloorModel    Model  `json:"floor_model"`
	FloorEffort   Effort `json:"floor_effort"`
	CeilingModel  Model  `json:"ceiling_model"`
	CeilingEffort Effort `json:"ceiling_effort"`
}

// modelRank orders models by capability, weakest to strongest — the SOLE ranking the self-check
// uses. Distinct from CheckTiers' xhigh/max availability tables (bh/plan.go): those gate PLAN
// authoring; this gates a LIVE session against a runtime band. A model absent from this table
// (unranked/unrecognized) gets rank -1, so it is always below any real floor rather than silently
// passing.
var modelRank = map[Model]int{
	ModelHaiku45:  0,
	ModelSonnet46: 1,
	ModelSonnet5:  2,
	ModelOpus46:   3,
	ModelOpus47:   4,
	ModelOpus48:   5,
	ModelFable5:   6,
}

// effortRank orders effort low -> max. effortBase (below) exceeds the highest effort rank so a
// model-rank difference always dominates the combined score in tierRank.
var effortRank = map[Effort]int{
	EffortLow:    0,
	EffortMedium: 1,
	EffortHigh:   2,
	EffortXHigh:  3,
	EffortMax:    4,
}

const effortBase = 10 // > max effortRank(4); keeps model the dominant term in tierRank

// modelOnlyRank looks up m's capability rank, tolerant of the trailing -YYYYMMDD real transcript
// model IDs carry (e.g. "claude-sonnet-5-20260101" for the bare pinned "claude-sonnet-5") — the
// same dateSuffixRE accounting.go's RateTable.Match strips, reused here as the one place this
// package normalizes a dated transcript ID to its bare pinned form. A model unrecognized even
// after stripping (unranked/unknown) gets rank -1, so it is always below any real floor rather
// than silently passing.
func modelOnlyRank(m Model) int {
	if r, ok := modelRank[m]; ok {
		return r
	}
	if stripped := dateSuffixRE.ReplaceAllString(string(m), ""); stripped != string(m) {
		if r, ok := modelRank[Model(stripped)]; ok {
			return r
		}
	}
	return -1
}

// tierRank combines model + effort into one composite score so a floor..ceiling band — even one
// spanning two different models — reduces to a single integer range check: below the floor's
// score aborts, above the ceiling's score warns, in between (inclusive) is silently fine. An
// unrecognized effort ranks as the bottom of the effort scale rather than inflating the score.
func tierRank(m Model, e Effort) int {
	er, ok := effortRank[e]
	if !ok {
		er = 0
	}
	return modelOnlyRank(m)*effortBase + er
}

// SelfCheckResult is the self-check verdict. Abort means the caller MUST stop (below the floor,
// OR a session-id mismatch per SCf); otherwise Warnings carries zero or more non-fatal notices
// (above the ceiling, effort undetectable) and the caller proceeds. SessionIDChecked/Match are
// only meaningful when the caller opted into the SCf identity guard (main.runSelfCheck's
// --session-id/--scratchpad-path); both stay false when that guard wasn't requested, so a JSON
// consumer can distinguish "not checked" from "checked and matched".
type SelfCheckResult struct {
	Model            Model    `json:"model"`
	Effort           Effort   `json:"effort,omitempty"`
	EffortDetected   bool     `json:"effort_detected"`
	Abort            bool     `json:"abort"`
	Warnings         []string `json:"warnings,omitempty"`
	Reason           string   `json:"reason"`
	SessionIDChecked bool     `json:"session_id_checked,omitempty"`
	SessionIDMatch   bool     `json:"session_id_match,omitempty"`
}

// SelfCheck enforces band on the observed (model, effort) session tier.
//
// When effortDetected is true, both dimensions are enforced together via tierRank: below floor
// aborts, above ceiling warns, in band is silent.
//
// When effortDetected is false (SC7: "$CLAUDE_EFFORT and settings.json effortLevel both absent"),
// ONLY the model dimension is enforced — floor/ceiling's effort fields are ignored rather than
// guessed — and a warning always notes the effort check was skipped, even when the model itself
// is within band. This never aborts on a dimension that cannot be observed, but never silently
// passes the full band unchecked either.
func SelfCheck(model Model, effort Effort, effortDetected bool, band TierBand) SelfCheckResult {
	r := SelfCheckResult{Model: model, Effort: effort, EffortDetected: effortDetected}

	if !effortDetected {
		r.Warnings = append(r.Warnings, "effort undetectable ($CLAUDE_EFFORT and settings.json effortLevel both absent) -- enforcing the model band only")
		mr, floorMR, ceilMR := modelOnlyRank(model), modelOnlyRank(band.FloorModel), modelOnlyRank(band.CeilingModel)
		switch {
		case mr < floorMR:
			r.Abort = true
			r.Reason = fmt.Sprintf("model %q is below the floor model %q", model, band.FloorModel)
		case mr > ceilMR:
			r.Warnings = append(r.Warnings, fmt.Sprintf("model %q is above the ceiling model %q", model, band.CeilingModel))
			r.Reason = "above the ceiling model (warn only)"
		default:
			r.Reason = "within the model band"
		}
		return r
	}

	observed, floor, ceiling := tierRank(model, effort), tierRank(band.FloorModel, band.FloorEffort), tierRank(band.CeilingModel, band.CeilingEffort)
	switch {
	case observed < floor:
		r.Abort = true
		r.Reason = fmt.Sprintf("%s/%s is below the floor %s/%s", model, effort, band.FloorModel, band.FloorEffort)
	case observed > ceiling:
		r.Warnings = append(r.Warnings, fmt.Sprintf("%s/%s is above the ceiling %s/%s", model, effort, band.CeilingModel, band.CeilingEffort))
		r.Reason = "above the ceiling (warn only)"
	default:
		r.Reason = "within band"
	}
	return r
}

// ---- deterministic transcript resolution + session-id identity guard ----
//
// The old $TRANSCRIPT resolution picked the newest *.jsonl mtime in the cwd's shared
// projects dir. Under two concurrent sessions in the same cwd, a longer-running session's
// transcript can be newer than the CALLING session's own — mtime silently hands the wrong
// session's transcript to accounting/self-check, poisoning cost data with no error anywhere.
// This section replaces that heuristic with a pure, deterministic path derived from the
// session's OWN id (never a directory scan/ModTime comparison), plus a trailing-line read that
// proves the resolved file really is this session's transcript before anything trusts it.

// sessionIDPattern is the UUID shape (8-4-4-4-12 hex) Claude Code session ids take. A value that
// doesn't match this shape is "unparseable" rather than silently accepted as a session id.
var sessionIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ValidSessionID reports whether id has the shape of a real Claude Code session id.
func ValidSessionID(id string) bool { return sessionIDPattern.MatchString(id) }

// nonAlnumRE matches every rune outside [A-Za-z0-9] — the ONE substitution SlugifyCWD applies.
var nonAlnumRE = regexp.MustCompile(`[^A-Za-z0-9]`)

// SlugifyCWD reproduces Claude Code's project-directory slug (already documented in
// build-with-team/SKILL.md's Phase 0: "the current working dir with non-alphanumerics replaced
// by -"): every rune outside [A-Za-z0-9] becomes '-'. Used to locate ~/.claude/projects/<slug>/
// from a live --cwd when the caller identifies itself via --session-id rather than
// --scratchpad-path (whose own cwd-slug segment ParseScratchpadPath extracts directly).
func SlugifyCWD(cwd string) string {
	return nonAlnumRE.ReplaceAllString(cwd, "-")
}

// ParseScratchpadPath extracts (cwdSlug, sessionID) from a path under a session's own scratchpad
// directory: .../<cwd-slug>/<session-id>/scratchpad[/...] — the shape Claude Code hands agents
// their scratchpad location in (e.g. the workspace's own scratchpad-directory convention). This
// is the ONE place this package derives a session's identity from its scratchpad location; it
// never scans a directory or compares ModTime. ok is false when: no path component is literally
// "scratchpad", "scratchpad" is among the first two components (no room for a preceding
// session-id/cwd-slug pair), or the component immediately before "scratchpad" doesn't match
// ValidSessionID — each an "unparseable" input the caller must reject, not guess past.
func ParseScratchpadPath(path string) (cwdSlug, sessionID string, ok bool) {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	idx := -1
	for i, p := range parts {
		if p == "scratchpad" {
			idx = i // last occurrence wins, in case a deeper path segment is also named "scratchpad"
		}
	}
	if idx < 2 {
		return "", "", false
	}
	sessionID = parts[idx-1]
	cwdSlug = parts[idx-2]
	if !ValidSessionID(sessionID) || cwdSlug == "" {
		return "", "", false
	}
	return cwdSlug, sessionID, true
}

// ResolveTranscriptPath builds the ONE deterministic transcript path for a session: a plain join
// of the projects root, the cwd slug, and the session id — SCf's replacement for mtime-newest
// directory scanning. No directory listing and no os.ModTime comparison occur anywhere in this
// function (or anywhere else in this file): by construction, a concurrently-newer OTHER session's
// transcript sitting in the same cwd-derived directory can never be selected, because it is never
// even looked at — the path is computed, not searched for. Existence is the caller's concern
// (main.runResolveTranscript stats it); this function is a pure string join.
func ResolveTranscriptPath(projectsDir, cwdSlug, sessionID string) string {
	return filepath.Join(projectsDir, cwdSlug, sessionID+".jsonl")
}

// sessionIDLineProbe reads just the sessionId off one transcript line.
type sessionIDLineProbe struct {
	SessionID string `json:"sessionId"`
}

// SessionIDScan is the outcome of scanning a transcript for the session identity its lines
// carry — the trailing-line read self-check uses to confirm a resolved transcript genuinely
// belongs to THIS session, not a concurrent session's file (SCf). Lines counts every non-blank
// line seen; Parsed counts lines that parsed as JSON (whether or not they named a sessionId).
// Together the two let a caller tell apart three DIFFERENT failure edges: an empty transcript
// (Lines==0), a transcript whose every line is malformed JSON (Parsed==0), and a well-formed
// transcript that simply never names a sessionId on any line (Parsed>0, SessionID=="").
type SessionIDScan struct {
	SessionID string
	Lines     int
	Parsed    int
}

// Found reports whether any line named a non-empty sessionId.
func (s SessionIDScan) Found() bool { return s.SessionID != "" }

// LatestTranscriptSessionID scans a transcript (JSONL) and returns the sessionId of the LAST
// line that names one, mirroring LatestTranscriptModel's last-line-wins rule (a genuine Claude
// Code transcript's sessionId never changes mid-file, so any line would answer the same, but this
// keeps the contract identical to its model-probing counterpart). A malformed line is counted in
// Lines but not Parsed, and never aborts the scan — matching this package's best-effort transcript
// tolerance elsewhere (LatestTranscriptModel, ParseTranscriptUsage).
func LatestTranscriptSessionID(r io.Reader) SessionIDScan {
	var scan SessionIDScan
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		scan.Lines++
		var p sessionIDLineProbe
		if json.Unmarshal(line, &p) != nil {
			continue
		}
		scan.Parsed++
		if p.SessionID != "" {
			scan.SessionID = p.SessionID
		}
	}
	return scan
}

// CheckSessionID compares a transcript's resolved sessionId (scan, already confirmed Found() by
// the caller — main.runSelfCheck rejects the empty/all-malformed/no-sessionId edges before ever
// calling this) against the caller's OWN want session id. This is SCf's hard-fail guard against
// silent cross-session accounting poisoning: a resolved transcript that does not actually belong
// to THIS session MUST abort, never merely warn — Abort is unconditionally true on a mismatch,
// with no ceiling-style "warn and continue" escape hatch.
func CheckSessionID(want string, scan SessionIDScan) SelfCheckResult {
	if scan.SessionID == want {
		return SelfCheckResult{SessionIDChecked: true, SessionIDMatch: true, Reason: "session id matches"}
	}
	return SelfCheckResult{
		SessionIDChecked: true,
		SessionIDMatch:   false,
		Abort:            true,
		Reason: fmt.Sprintf("transcript sessionId %q does not match this session's id %q -- cross-session transcript, aborting",
			scan.SessionID, want),
	}
}
