package agentcontract

import (
	"fmt"
	"io"
	"sort"
)

// Finding is one lint violation: a specific, named rule failing against a specific brief.
type Finding struct {
	// Rule is a stable identifier for the violated property, e.g. "MATRIX-MISSING-CELL".
	Rule string
	// Brief is the path of the offending brief.
	Brief string
	// Message explains the violation in terms of this brief and roster.
	Message string
}

func (f Finding) String() string {
	return fmt.Sprintf("[%s] %s: %s", f.Rule, f.Brief, f.Message)
}

// reviewerCheckedLimits is the fixed, always-reported list of properties this lint's rules
// cannot verify — cell quality and one-rule-per-decision judgment beyond the specific patterns
// this package matches mechanically. It is declared unconditionally on every Report, pass or
// fail, so a green run is never read as a quality verdict for these.
var reviewerCheckedLimits = []string{
	"Discriminator/not-confusable cell QUALITY is reviewer-checked, not mechanically verified: a nonempty reason of any content (e.g. \"different scope\") satisfies completeness.",
	"Tie-break rule QUALITY is reviewer-checked, not mechanically verified: this lint only requires a nonempty tie_break where fuzzy is true.",
	"One-rule-per-decision JUDGMENT is reviewer-checked beyond the specific patterns this lint matches: the FB3 fragment-write/split-across-dispatches pair, literal duplicate decision statements, and a decision statement restated verbatim in the body. A decision restated in different words is not caught.",
	"FB11 field QUALITY is reviewer-checked, not mechanically verified: this lint requires the schema and prose to name the other-locations-per-edit field with an explicit none value, not that the field is populated correctly for any given edit.",
}

// Report is the deterministic result of linting one or more rosters and briefs.
type Report struct {
	RostersChecked int
	BriefsChecked  int
	Findings       []Finding
	// ReviewerChecked is the honest, always-present limit statement: what this Report's Pass
	// does NOT certify.
	ReviewerChecked []string
}

// NewReport builds a Report from an accumulated finding list, sorting findings for
// deterministic output and attaching the fixed reviewer-checked limit statement.
func NewReport(rostersChecked, briefsChecked int, findings []Finding) *Report {
	sorted := append([]Finding(nil), findings...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Brief != sorted[j].Brief {
			return sorted[i].Brief < sorted[j].Brief
		}
		if sorted[i].Rule != sorted[j].Rule {
			return sorted[i].Rule < sorted[j].Rule
		}
		// Message is the final tiebreaker: several findings can share one (Brief, Rule) —
		// e.g. one brief missing a cell against each of two siblings — and they originate from
		// Go map iteration, so without this the rendered order varies run to run.
		return sorted[i].Message < sorted[j].Message
	})
	return &Report{
		RostersChecked:  rostersChecked,
		BriefsChecked:   briefsChecked,
		Findings:        sorted,
		ReviewerChecked: reviewerCheckedLimits,
	}
}

// Pass reports whether the lint found zero violations. It says nothing about the
// reviewer-checked properties above — see ReviewerChecked.
func (r *Report) Pass() bool {
	return len(r.Findings) == 0
}

// Render writes the report deterministically: a summary line, every finding, then the
// unconditional reviewer-checked limit statement.
func (r *Report) Render(w io.Writer) error {
	verdict := "PASS"
	if !r.Pass() {
		verdict = "FAIL"
	}
	if _, err := fmt.Fprintf(w, "agentcontract: %s — %d roster(s), %d brief(s), %d finding(s)\n",
		verdict, r.RostersChecked, r.BriefsChecked, len(r.Findings)); err != nil {
		return err
	}
	for _, f := range r.Findings {
		if _, err := fmt.Fprintf(w, "  %s\n", f.String()); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w, "\nThis lint proves the discriminator matrix is COMPLETE and the named properties below are STRUCTURALLY present. It does not certify cell quality or judgment calls. Reviewer-checked, not mechanically verified, regardless of verdict above:"); err != nil {
		return err
	}
	for _, limit := range r.ReviewerChecked {
		if _, err := fmt.Fprintf(w, "  - %s\n", limit); err != nil {
			return err
		}
	}
	return nil
}
