package bandcheck

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/gate"
	"github.com/johnrichter/claude-shared-tooling/go/roster"
	"github.com/johnrichter/claude-shared-tooling/go/transcript"
)

// TierBand is the floor/ceiling a session's (model, effort) tier is checked against. Both ends
// are caller-supplied — this package hardcodes neither — and both are inclusive.
type TierBand struct {
	FloorModel    string
	FloorEffort   roster.Effort
	CeilingModel  string
	CeilingEffort roster.Effort
}

// effortIndex returns e's position in roster.AllEfforts (the roster's own canonical low-to-max
// ordering), so effort tie-breaking never restates that order as a second table. ok is false for
// an effort value the roster doesn't declare.
func effortIndex(e roster.Effort) (int, bool) {
	for i, want := range roster.AllEfforts {
		if want == e {
			return i, true
		}
	}
	return 0, false
}

// compareTier orders (m1, e1) against (m2, e2): model is the dominant axis via roster.Compare —
// the sole model ordering this package uses — and effort only breaks a tie between two models
// roster.Compare finds equally ranked (typically the same model on both sides). An effort value
// absent from roster.AllEfforts breaks no tie in either direction (rank 0, both sides equal),
// since roster declares no order for it to defer to.
func compareTier(m1 string, e1 roster.Effort, m2 string, e2 roster.Effort) (int, error) {
	cmp, err := roster.Compare(m1, m2)
	if err != nil {
		return 0, err
	}
	if cmp != 0 {
		return cmp, nil
	}
	r1, _ := effortIndex(e1)
	r2, _ := effortIndex(e2)
	switch {
	case r1 < r2:
		return -1, nil
	case r1 > r2:
		return 1, nil
	default:
		return 0, nil
	}
}

// SelfCheckResult is SelfCheck's verdict. Verdict is always gate's own three-way outcome
// (VerdictAbort/VerdictSilent/VerdictWarn) — this package never invents a fourth value — except
// when RosterStale is true, a distinct outcome meaning the roster has no answer for the observed
// model or a band endpoint, not that the session is under- or over-tiered. A JSON consumer reads
// RosterStale before Verdict for exactly that reason.
type SelfCheckResult struct {
	Model          string        `json:"model"`
	Effort         roster.Effort `json:"effort,omitempty"`
	EffortDetected bool          `json:"effort_detected"`
	Verdict        gate.Verdict  `json:"-"`
	VerdictName    string        `json:"verdict"`
	RosterStale    bool          `json:"roster_stale,omitempty"`
	Reason         string        `json:"reason"`
}

// tierRank collapses a below/above-band decision to the one signed value gate.Band's
// three-way switch already knows how to classify, evaluated against a fixed [0, 0] band: -1 is
// always below floor (abort), 1 is always above ceiling (warn), 0 is always in band (silent).
// This is the ONLY place SelfCheck decides abort/warn/silent, and it decides it by calling
// gate.Band, not by re-implementing gate.Band's switch.
func tierVerdict(belowFloor, aboveCeiling bool) gate.Verdict {
	switch {
	case belowFloor:
		return gate.Band(-1, 0, 0)
	case aboveCeiling:
		return gate.Band(1, 0, 0)
	default:
		return gate.Band(0, 0, 0)
	}
}

// SelfCheck checks the observed (model, effort) session tier against band, ordering solely
// through roster.Compare and deciding the resulting verdict solely through gate.Band (see
// tierVerdict) — no independent floor/ceiling/abort/warn/silent logic lives in this function.
//
// When effortDetected is false, only the model axis is enforced: band's effort fields are
// ignored rather than guessed, and Reason always notes the effort axis was skipped, even when
// the model itself lands in band. A model this package cannot observe at all is never guessed
// into an axis skip — that is the caller's job before calling SelfCheck.
//
// A model (observed or either band endpoint) absent from the roster, or an undeclared
// cross-family pair, sets RosterStale and returns immediately — never a below-floor guess.
func SelfCheck(model string, effort roster.Effort, effortDetected bool, band TierBand) SelfCheckResult {
	r := SelfCheckResult{Model: model, Effort: effort, EffortDetected: effortDetected}

	var belowFloor, aboveCeiling bool
	var err error
	if effortDetected {
		var cmpFloor, cmpCeiling int
		if cmpFloor, err = compareTier(model, effort, band.FloorModel, band.FloorEffort); err == nil {
			cmpCeiling, err = compareTier(model, effort, band.CeilingModel, band.CeilingEffort)
		}
		belowFloor, aboveCeiling = cmpFloor < 0, cmpCeiling > 0
	} else {
		var cmpFloor, cmpCeiling int
		if cmpFloor, err = roster.Compare(model, band.FloorModel); err == nil {
			cmpCeiling, err = roster.Compare(model, band.CeilingModel)
		}
		belowFloor, aboveCeiling = cmpFloor < 0, cmpCeiling > 0
	}
	if err != nil {
		r.RosterStale, r.Reason = true, err.Error()
		return r
	}

	r.Verdict = tierVerdict(belowFloor, aboveCeiling)
	r.VerdictName = r.Verdict.String()
	switch {
	case !effortDetected && r.Verdict == gate.VerdictSilent:
		r.Reason = "model within band; effort undetectable ($CLAUDE_EFFORT and settings.json effortLevel both absent) -- model-only check"
	case !effortDetected:
		r.Reason = fmt.Sprintf("model %s (%s); effort undetectable -- model-only check", model, r.VerdictName)
	case r.Verdict == gate.VerdictAbort:
		r.Reason = fmt.Sprintf("%s/%s is below the floor %s/%s", model, effort, band.FloorModel, band.FloorEffort)
	case r.Verdict == gate.VerdictWarn:
		r.Reason = fmt.Sprintf("%s/%s is above the ceiling %s/%s", model, effort, band.CeilingModel, band.CeilingEffort)
	default:
		r.Reason = "within band"
	}
	return r
}

// DetectSessionModel resolves a session's live model tier from a transcript stream: the model
// named on the LAST line src.Turns reports as orchestrator-authored (transcript.AuthorOrchestrator).
//
// This is a narrower reading than "the last line that names a model" on purpose (FB13): a line
// whose Authorship is transcript.AuthorSubagent is never inspected, and — the case that matters —
// neither is a line whose Authorship is transcript.AuthorUnknown, even when it is the transcript's
// final line. A transcript format that starts inlining subagent turns without an authorship
// marker on them yields ok == false here, never a subagent's model value: this function states
// the authorship it requires and checks it, rather than inheriting file-layout separation as an
// unstated assumption. It also never recurses into a turn's own payload (tool input, message
// text) looking for a model-shaped string — only transcript.Turn's own flat Model field, as
// src.Turns already parsed it, is ever consulted.
func DetectSessionModel(src transcript.TranscriptSource, r io.Reader) (model string, ok bool, err error) {
	err = src.Turns(r, func(t transcript.Turn) error {
		if t.Malformed || t.Authorship != transcript.AuthorOrchestrator {
			return nil
		}
		if t.Model != "" {
			model, ok = t.Model, true
		}
		return nil
	})
	return model, ok, err
}

// ParseSettingsEffort extracts effortLevel from a Claude Code settings.json's bytes — the static
// effort signal (it reflects the value at session launch, not a later /effort override). ok is
// false when the field is absent, empty, or raw does not parse as JSON.
func ParseSettingsEffort(raw []byte) (effort roster.Effort, ok bool) {
	var doc struct {
		EffortLevel string `json:"effortLevel"`
	}
	if json.Unmarshal(raw, &doc) != nil || doc.EffortLevel == "" {
		return "", false
	}
	return roster.Effort(strings.ToLower(doc.EffortLevel)), true
}
