package agentcontract

// fb3ForbiddenPhrases are the superseded separate-dispatch guidance FB3 replaced. Any of these
// appearing anywhere the brief states its large-artifact rule is the specific defect FB3 names:
// two rules for one decision, one of them wrong.
var fb3ForbiddenPhrases = []string{
	"split across dispatches",
	"split the work across dispatches",
	"splits the work across dispatches",
	"split it across dispatches",
	"separate dispatches",
	"multiple dispatches",
}

// checkFB3 enforces: an agent that can produce an artifact too large for one payload must
// state the write-bounded-fragments-to-disk / assemble / validate rule, and must not state the
// superseded split-across-dispatches rule. Checked against both the brief's decisions and its
// prose body — a brief can carry the rule as a named decision, as body prose, or both.
func checkFB3(b *Brief) []Finding {
	if !b.Frontmatter.Contract.LargeArtifact {
		return nil
	}

	text := normalize(b.Body)
	for _, d := range b.Frontmatter.Contract.Decisions {
		text += " " + normalize(d.Statement)
	}

	hasRequired := containsAll(text, "disk", "assembl", "valid") &&
		containsAny(text, "fragment", "bounded piece", "bounded pieces")
	hasForbidden := containsAny(text, fb3ForbiddenPhrases...)

	switch {
	case hasRequired && hasForbidden:
		return []Finding{{
			Rule: "FB3-CONTRADICTION", Brief: b.Path,
			Message: "states both the write-bounded-fragments-to-disk rule and the superseded split-across-dispatches rule; one rule per decision",
		}}
	case hasForbidden:
		return []Finding{{
			Rule: "FB3-SUPERSEDED-RULE", Brief: b.Path,
			Message: "states the superseded split-across-dispatches rule; large_artifact agents must state write-bounded-fragments-to-disk, assemble, then validate instead",
		}}
	case !hasRequired:
		return []Finding{{
			Rule: "FB3-MISSING-RULE", Brief: b.Path,
			Message: "large_artifact is true but neither the body nor any decision states the write-bounded-fragments-to-disk, assemble, then validate rule",
		}}
	default:
		return nil
	}
}

// checkFB11 enforces: an edit-proposing agent must require, per proposed edit, the other
// locations asserting the same claim with an explicit none value — checked against both the
// brief's referenced output schema (structurally) and its prose, so a brief cannot satisfy one
// and contradict the other.
func checkFB11(b *Brief, opts Options) []Finding {
	if !b.Frontmatter.Contract.EditProposing {
		return nil
	}
	var findings []Finding

	body := normalize(b.Body)
	proseOK := containsAll(body, "other location", "same claim", "none") ||
		containsAll(body, "other locations", "same claim", "none")
	if !proseOK {
		findings = append(findings, Finding{
			Rule: "FB11-MISSING-PROSE", Brief: b.Path,
			Message: "edit_proposing is true but the body does not require naming the other locations asserting the same claim, with an explicit none value",
		})
	}

	schemaPath, ok := resolveSchemaPath(b, opts.SchemaRoots)
	if !ok {
		// Already reported by checkOutputSchema; avoid a duplicate, unresolvable-path finding
		// under a different rule name here.
		return findings
	}
	doc, err := loadSchemaDoc(schemaPath)
	if err != nil {
		return append(findings, Finding{
			Rule: "FB11-SCHEMA-UNREADABLE", Brief: b.Path,
			Message: "edit_proposing is true but the referenced output schema could not be parsed as JSON: " + err.Error(),
		})
	}

	if !requiredArrayContains(doc, fb11FieldName) {
		findings = append(findings, Finding{
			Rule: "FB11-SCHEMA-FIELD-NOT-REQUIRED", Brief: b.Path,
			Message: "output schema does not list \"" + fb11FieldName + "\" as required on the edit object",
		})
	}
	if def, found := propertyDefinition(doc, fb11FieldName); !found {
		findings = append(findings, Finding{
			Rule: "FB11-SCHEMA-FIELD-MISSING", Brief: b.Path,
			Message: "output schema declares no \"" + fb11FieldName + "\" property",
		})
	} else if !subtreeMentions(def, "none") {
		findings = append(findings, Finding{
			Rule: "FB11-SCHEMA-FIELD-NO-NONE", Brief: b.Path,
			Message: "output schema's \"" + fb11FieldName + "\" property does not document an explicit none value",
		})
	}

	return findings
}
