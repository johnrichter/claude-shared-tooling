package agentcontract

import "fmt"

// fb11FieldName is the canonical output-schema field name this lint requires of an
// edit-proposing agent's per-edit output: the other locations asserting the same claim, with an
// explicit "none" reading for an edit that corrects an unrepeated claim.
const fb11FieldName = "other_locations_asserting_claim"

// Options controls where CheckProperties resolves a brief's referenced output schema.
type Options struct {
	// SchemaRoots is searched, in order, after the brief's own directory, when resolving
	// Contract.OutputSchema.
	SchemaRoots []string
}

// CheckProperties checks the mechanically-checkable instruction properties on one brief: the
// output-schema reference, failure-path termination, decision derivation, and the FB3/FB11
// defect-class properties. It does not check one-rule-per-decision judgment in general, nor
// discriminator-matrix completeness — see CheckMatrix and Report.ReviewerChecked for those.
func CheckProperties(b *Brief, opts Options) []Finding {
	var findings []Finding
	findings = append(findings, checkOutputSchema(b, opts)...)
	findings = append(findings, checkFailurePaths(b)...)
	findings = append(findings, checkDecisions(b)...)
	findings = append(findings, checkFB3(b)...)
	findings = append(findings, checkFB11(b, opts)...)
	return findings
}

func checkOutputSchema(b *Brief, opts Options) []Finding {
	ref := b.Frontmatter.Contract.OutputSchema
	if trimmedEmpty(ref) {
		return []Finding{{
			Rule: "SCHEMA-REF-MISSING", Brief: b.Path,
			Message: "contract.output_schema is empty; output must be a schema reference (a path), not left undeclared",
		}}
	}
	if !looksLikePath(ref) {
		return []Finding{{
			Rule: "SCHEMA-REF-NOT-A-PATH", Brief: b.Path,
			Message: fmt.Sprintf("contract.output_schema %q reads as prose, not a path (must be whitespace-free with a .json/.yaml/.yml extension)", ref),
		}}
	}
	if _, ok := resolveSchemaPath(b, opts.SchemaRoots); !ok {
		return []Finding{{
			Rule: "SCHEMA-REF-UNRESOLVABLE", Brief: b.Path,
			Message: fmt.Sprintf("contract.output_schema %q does not resolve to a file relative to the brief's directory or the configured schema roots", ref),
		}}
	}
	return nil
}

func checkFailurePaths(b *Brief) []Finding {
	var findings []Finding
	seen := map[string]bool{}
	for _, fp := range b.Frontmatter.Contract.FailurePaths {
		if trimmedEmpty(fp.Name) {
			findings = append(findings, Finding{
				Rule: "FAILUREPATH-EMPTY-NAME", Brief: b.Path,
				Message: "a failure_paths entry has no name",
			})
			continue
		}
		if seen[fp.Name] {
			findings = append(findings, Finding{
				Rule: "FAILUREPATH-DUPLICATE-NAME", Brief: b.Path,
				Message: fmt.Sprintf("failure path %q is declared more than once", fp.Name),
			})
		}
		seen[fp.Name] = true
		if trimmedEmpty(fp.Action) {
			findings = append(findings, Finding{
				Rule: "FAILUREPATH-NO-ACTION", Brief: b.Path,
				Message: fmt.Sprintf("failure path %q names no terminating action", fp.Name),
			})
		}
	}
	return findings
}

// checkDecisions enforces "derived once, referenced by name": no two decisions share the same
// statement text, and no decision's statement is restated verbatim in the body rather than
// referenced by its name.
func checkDecisions(b *Brief) []Finding {
	var findings []Finding
	byStatement := map[string]string{} // normalized statement -> first decision name declaring it
	body := normalize(b.Body)

	for _, d := range b.Frontmatter.Contract.Decisions {
		if trimmedEmpty(d.Name) {
			findings = append(findings, Finding{
				Rule: "DECISION-EMPTY-NAME", Brief: b.Path,
				Message: "a decisions entry has no name",
			})
			continue
		}
		if trimmedEmpty(d.Statement) {
			findings = append(findings, Finding{
				Rule: "DECISION-EMPTY-STATEMENT", Brief: b.Path,
				Message: fmt.Sprintf("decision %q has no statement", d.Name),
			})
			continue
		}

		norm := normalize(d.Statement)
		if first, ok := byStatement[norm]; ok && first != d.Name {
			findings = append(findings, Finding{
				Rule: "DECISION-RESTATED", Brief: b.Path,
				Message: fmt.Sprintf("decision %q restates the same rule already declared as %q; a decision is derived once and referenced by name", d.Name, first),
			})
		} else {
			byStatement[norm] = d.Name
		}

		// A short statement risks false positives against unrelated body prose that merely
		// shares common words; only a substantial verbatim match is treated as a restatement.
		if len(norm) >= 20 && containsAll(body, norm) {
			findings = append(findings, Finding{
				Rule: "DECISION-RESTATED-IN-BODY", Brief: b.Path,
				Message: fmt.Sprintf("decision %q's statement also appears verbatim in the body; the body must reference it by name instead of restating it", d.Name),
			})
		}
	}
	return findings
}
