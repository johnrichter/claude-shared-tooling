package compliance

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/johnrichter/claude-shared-tooling/go/fsx"
)

// FeedbackSchema marks the register document shape a compliance defect is filed under: the
// delivery-agent-team's feedback-register/v1 contract -- {schema, project, entries[]}, one entry
// per finding, ranked downstream by impact*urgency. This package keeps its own copy of the
// register shape (matching this repo's other feedback-register writers) rather than depending on
// the delivery-agent-team's own build tooling.
const FeedbackSchema = "feedback-register/v1"

// Impact/urgency scores for the two defect shapes this package opens. A below-floor miss pauses
// the owner's next release, so both axes score high. A still-unmeasured advisory blocks nothing --
// it is visible, not enforcing -- so both axes score lower.
const (
	belowFloorImpact  = 4
	belowFloorUrgency = 4
	unmeasuredImpact  = 3
	unmeasuredUrgency = 2
)

// FeedbackEntry is one register row.
type FeedbackEntry struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	SourceTaskID     string `json:"source_task_id,omitempty"`
	Feedback         string `json:"feedback"`
	ProposedSolution string `json:"proposed_solution,omitempty"`
	WhyItMatters     string `json:"why_it_matters,omitempty"`
	Impact           int    `json:"impact"`
	Urgency          int    `json:"urgency"`
	Criticality      int    `json:"criticality"`
	Added            string `json:"added,omitempty"`
}

// FeedbackRegister is the append-only feedback.json document a compliance defect is opened in.
type FeedbackRegister struct {
	Schema  string          `json:"schema"`
	Project string          `json:"project,omitempty"`
	Entries []FeedbackEntry `json:"entries"`
}

// LoadFeedbackRegister reads path, tolerating an absent file as a fresh, empty, schema-stamped
// register -- this project's first compliance defect does not require one to pre-exist.
func LoadFeedbackRegister(path string) (FeedbackRegister, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return FeedbackRegister{Schema: FeedbackSchema}, nil
		}
		return FeedbackRegister{}, fmt.Errorf("compliance: read %s: %w", path, err)
	}
	var reg FeedbackRegister
	if err := json.Unmarshal(raw, &reg); err != nil {
		return FeedbackRegister{}, fmt.Errorf("compliance: parse %s: %w", path, err)
	}
	if reg.Schema == "" {
		reg.Schema = FeedbackSchema
	}
	return reg, nil
}

// Save writes reg to path atomically, pretty-printed for reviewability.
func (reg FeedbackRegister) Save(path string) error {
	raw, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return fmt.Errorf("compliance: encode feedback register: %w", err)
	}
	raw = append(raw, '\n')
	return fsx.WriteAtomic(path, raw, registryFilePerm)
}

// Ensure returns reg's existing entry whose SourceTaskID equals key, or appends one built by
// build and returns that. Every defect this package opens routes through here, so a repeat
// measurement run that reports the same finding again never opens a second defect for it.
func (reg FeedbackRegister) Ensure(key string, build func(id string) FeedbackEntry) (FeedbackRegister, FeedbackEntry) {
	for _, e := range reg.Entries {
		if e.SourceTaskID == key {
			return reg, e
		}
	}
	entry := build(fmt.Sprintf("FB%d", len(reg.Entries)+1))
	entry.SourceTaskID = key
	entry.Criticality = entry.Impact * entry.Urgency
	out := reg
	out.Entries = append(append([]FeedbackEntry{}, reg.Entries...), entry)
	return out, entry
}
