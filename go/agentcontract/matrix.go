package agentcontract

import "fmt"

// CheckMatrix checks one roster's discriminator matrix for completeness. The sibling set each
// brief is checked against is Roster.Siblings(brief) — every other roster member — never the
// keys of that brief's own Discriminators map. A roster of one has an empty sibling set and
// passes with no declared cells; a roster of more than one requires a cell for every sibling.
func CheckMatrix(r Roster) []Finding {
	var findings []Finding

	for _, b := range r.Briefs {
		siblings := r.Siblings(b)
		siblingNames := make(map[string]bool, len(siblings))
		for _, s := range siblings {
			siblingNames[s.Frontmatter.Name] = true
		}

		declared := b.Frontmatter.Contract.Discriminators

		for name := range declared {
			if name == b.Frontmatter.Name {
				findings = append(findings, Finding{
					Rule: "MATRIX-SELF-REFERENCE", Brief: b.Path,
					Message: "discriminators declares a cell against its own name; a brief is not its own sibling",
				})
				continue
			}
			if !siblingNames[name] {
				findings = append(findings, Finding{
					Rule: "MATRIX-STALE-SIBLING", Brief: b.Path,
					Message: fmt.Sprintf("discriminators declares a cell for %q, which is not a member of roster %s", name, r.Dir),
				})
			}
		}

		for name := range siblingNames {
			cell, ok := declared[name]
			if !ok {
				findings = append(findings, Finding{
					Rule: "MATRIX-MISSING-CELL", Brief: b.Path,
					Message: fmt.Sprintf("no discriminator or not-confusable declaration against sibling %q", name),
				})
				continue
			}
			findings = append(findings, checkCell(b.Path, name, cell)...)
		}
	}

	return findings
}

// checkCell validates one declared cell's own structure: a recognized relation, a nonempty
// reason, and — where the boundary is marked fuzzy — a nonempty tie-break.
func checkCell(briefPath, sibling string, cell Discriminator) []Finding {
	var findings []Finding

	switch cell.Relation {
	case RelationDiscriminator, RelationNotConfusable:
	default:
		findings = append(findings, Finding{
			Rule: "MATRIX-BAD-RELATION", Brief: briefPath,
			Message: fmt.Sprintf("cell for %q declares relation %q, must be %q or %q", sibling, cell.Relation, RelationDiscriminator, RelationNotConfusable),
		})
	}

	if trimmedEmpty(cell.Reason) {
		findings = append(findings, Finding{
			Rule: "MATRIX-MISSING-REASON", Brief: briefPath,
			Message: fmt.Sprintf("cell for %q has no reason", sibling),
		})
	}

	if cell.Fuzzy && trimmedEmpty(cell.TieBreak) {
		findings = append(findings, Finding{
			Rule: "MATRIX-MISSING-TIEBREAK", Brief: briefPath,
			Message: fmt.Sprintf("cell for %q marks the boundary fuzzy but names no tie-break rule", sibling),
		})
	}

	return findings
}
