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

	"github.com/johnrichter/claude-shared-tooling/go/roster"
)

// This file implements the session-tier self-check (design SC7): both build-with-team and
// plan-with-team self-check their LIVE session tier at Phase 0 against a {floor, ceiling} band.
// A caller supplies its own band either literally (four Model/Effort values) or by name
// (namedBands, resolved via ResolveBand) — plan-with-team's Opus-only floor vs build-with-team's
// Sonnet-only band. Either way, ordering is enforced solely through roster.Compare
// (ai-shared-lib/go/roster): this package owns the resolve/compare mechanics common to both, not
// a model capability table.

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

// namedBands are the roster-resolved bands self-check's --band flag may select in place of the
// four literal --floor-*/--ceiling-* flags (SC-MODELROSTER's "derived, not enumerated" treatment
// of the two skill bands: plan-with-team's Opus floor, build-with-team's Sonnet band). Ordering
// is always enforced through roster.Compare regardless of how the band was supplied, so a newly
// released model slots in correctly on either side without a code change here.
var namedBands = map[string]TierBand{
	"plan":  {FloorModel: ModelOpus48, FloorEffort: EffortHigh, CeilingModel: ModelOpus48, CeilingEffort: EffortMax},
	"build": {FloorModel: ModelSonnet5, FloorEffort: EffortMedium, CeilingModel: ModelSonnet5, CeilingEffort: EffortHigh},
}

// ResolveBand looks up a named band. ok is false for a name outside this package's own closed
// set — a caller/argv error (unlike an unrecognized MODEL, which is a roster-stale verdict), so
// main.go treats it as a usage error, never a self-check verdict.
func ResolveBand(name string) (TierBand, bool) {
	b, ok := namedBands[name]
	return b, ok
}

// effortRank orders effort low -> max, used only to break a tie when roster.Compare finds two
// models equally ranked (typically the same model on both sides of a comparison).
var effortRank = map[Effort]int{
	EffortLow:    0,
	EffortMedium: 1,
	EffortHigh:   2,
	EffortXHigh:  3,
	EffortMax:    4,
}

func effortRankOf(e Effort) int {
	if r, ok := effortRank[e]; ok {
		return r
	}
	return 0
}

// compareTier orders (m1, e1) against (m2, e2): model is the dominant dimension, via
// roster.Compare (the SOLE model ordering this package uses); effort only breaks a tie between
// equally-ranked models. Returns roster.StaleError (wrapped, unmodified) when either model is
// absent from the roster or is an undeclared cross-family pair — the caller turns that into the
// roster-stale verdict, never a below-floor guess.
func compareTier(m1 Model, e1 Effort, m2 Model, e2 Effort) (int, error) {
	cmp, err := roster.Compare(string(m1), string(m2))
	if err != nil {
		return 0, err
	}
	if cmp != 0 {
		return cmp, nil
	}
	switch {
	case effortRankOf(e1) < effortRankOf(e2):
		return -1, nil
	case effortRankOf(e1) > effortRankOf(e2):
		return 1, nil
	default:
		return 0, nil
	}
}

// SelfCheckResult is the self-check verdict. Abort means the caller MUST stop (below the floor,
// OR a session-id mismatch per SCf). RosterStale means the roster has no answer for the observed
// model or a band endpoint (SC-MODELROSTER) — a THIRD outcome, distinct from Abort: the caller
// must refresh the roster, not assume the session is under-tiered; Abort stays false on this
// path. Otherwise Warnings carries zero or more non-fatal notices (above the ceiling, effort
// undetectable) and the caller proceeds. SessionIDChecked/Match are only meaningful when the
// caller opted into the SCf identity guard (main.runSelfCheck's --session-id/--scratchpad-path);
// both stay false when that guard wasn't requested, so a JSON consumer can distinguish "not
// checked" from "checked and matched".
type SelfCheckResult struct {
	Model            Model    `json:"model"`
	Effort           Effort   `json:"effort,omitempty"`
	EffortDetected   bool     `json:"effort_detected"`
	Abort            bool     `json:"abort"`
	RosterStale      bool     `json:"roster_stale,omitempty"`
	Warnings         []string `json:"warnings,omitempty"`
	Reason           string   `json:"reason"`
	SessionIDChecked bool     `json:"session_id_checked,omitempty"`
	SessionIDMatch   bool     `json:"session_id_match,omitempty"`
}

// SelfCheck enforces band on the observed (model, effort) session tier, ordering solely through
// roster.Compare (bh/roster's Compare, embedded roster data — no hardcoded rank table).
//
// When effortDetected is true, both dimensions are enforced together via compareTier: below
// floor aborts, above ceiling warns, in band is silent.
//
// When effortDetected is false (SC7: "$CLAUDE_EFFORT and settings.json effortLevel both absent"),
// ONLY the model dimension is enforced — floor/ceiling's effort fields are ignored rather than
// guessed — and a warning always notes the effort check was skipped, even when the model itself
// is within band. This never aborts on a dimension that cannot be observed, but never silently
// passes the full band unchecked either.
//
// A model (observed or either band endpoint) absent from the roster, or an undeclared
// cross-family pair, sets RosterStale and returns immediately: never a below-floor guess.
func SelfCheck(model Model, effort Effort, effortDetected bool, band TierBand) SelfCheckResult {
	r := SelfCheckResult{Model: model, Effort: effort, EffortDetected: effortDetected}

	if !effortDetected {
		r.Warnings = append(r.Warnings, "effort undetectable ($CLAUDE_EFFORT and settings.json effortLevel both absent) -- enforcing the model band only")
		belowFloor, err := roster.Compare(string(model), string(band.FloorModel))
		if err != nil {
			r.RosterStale, r.Reason = true, err.Error()
			return r
		}
		aboveCeiling, err := roster.Compare(string(model), string(band.CeilingModel))
		if err != nil {
			r.RosterStale, r.Reason = true, err.Error()
			return r
		}
		switch {
		case belowFloor < 0:
			r.Abort = true
			r.Reason = fmt.Sprintf("model %q is below the floor model %q", model, band.FloorModel)
		case aboveCeiling > 0:
			r.Warnings = append(r.Warnings, fmt.Sprintf("model %q is above the ceiling model %q", model, band.CeilingModel))
			r.Reason = "above the ceiling model (warn only)"
		default:
			r.Reason = "within the model band"
		}
		return r
	}

	belowFloor, err := compareTier(model, effort, band.FloorModel, band.FloorEffort)
	if err != nil {
		r.RosterStale, r.Reason = true, err.Error()
		return r
	}
	aboveCeiling, err := compareTier(model, effort, band.CeilingModel, band.CeilingEffort)
	if err != nil {
		r.RosterStale, r.Reason = true, err.Error()
		return r
	}
	switch {
	case belowFloor < 0:
		r.Abort = true
		r.Reason = fmt.Sprintf("%s/%s is below the floor %s/%s", model, effort, band.FloorModel, band.FloorEffort)
	case aboveCeiling > 0:
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
