package bh

import (
	"cmp"
	"fmt"
	"regexp"
	"strings"
)

var multiNL = regexp.MustCompile(`\n+`)
var tripleNL = regexp.MustCompile(`\n{3,}`)

// cell sanitizes a value for a Markdown table cell: escape pipes, collapse newlines, trim.
func cell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	s = multiNL.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// line sanitizes a value for a Markdown bullet/heading: collapse newlines, trim (no pipes).
func line(s string) string {
	return strings.TrimSpace(multiNL.ReplaceAllString(s, " "))
}

func tierOf(m Model, e Effort) string {
	ms, es := string(m), string(e)
	if ms == "" {
		ms = "?"
	}
	if es == "" {
		es = "?"
	}
	return ms + "/" + es
}

// PlanDocMeta supplies the frontmatter the workspace requires on the plan.md mirror. When Slug
// is empty, RenderPlan omits frontmatter (handy for ad-hoc viewing); the skill always passes it.
type PlanDocMeta struct {
	Slug    string
	Topic   string
	Updated string
}

// RenderPlan renders the human-readable plan.md mirror from a Plan. Pure string-out so it is
// fully testable; escaping is applied at every interpolation in one auditable place.
func RenderPlan(p Plan, meta PlanDocMeta) string {
	var b strings.Builder
	w := func(s string) { b.WriteString(s); b.WriteByte('\n') }

	if meta.Slug != "" {
		topic := cmp.Or(meta.Topic, "tooling")
		w("---")
		w("name: " + meta.Slug + " — Plan")
		w(fmt.Sprintf("description: %q", fmt.Sprintf("Build plan mirror for the %s project — generated from plan.json (canonical, immutable spec); milestones → phases → tasks with per-task tier, deps, acceptance, and test strategy. Do not hand-edit.", meta.Slug)))
		w("id: project:" + meta.Slug + ":plan")
		w("tags:")
		for _, tg := range []string{"type:project", "topic:" + topic, "status:complete"} {
			w("  - " + tg)
		}
		w("links: []")
		w("updated: " + cmp.Or(meta.Updated, "(unset)"))
		w("---")
		w("")
	}

	goal := line(p.Goal)
	if goal == "" {
		goal = "(no goal)"
	}
	w("# Build plan — " + goal)
	w("")
	if len(p.SuccessCriteria) > 0 {
		w("## Success criteria")
		for _, s := range p.SuccessCriteria {
			w("- " + line(s))
		}
		w("")
	}
	if len(p.Assumptions) > 0 {
		w("## Assumptions")
		for _, a := range p.Assumptions {
			w("- " + line(a))
		}
		w("")
	}
	if len(p.Tradeoffs) > 0 {
		w("## Key tradeoffs")
		for _, t := range p.Tradeoffs {
			opts := ""
			if len(t.Options) > 0 {
				parts := make([]string, len(t.Options))
				for i, o := range t.Options {
					parts[i] = line(o)
				}
				opts = " (options: " + strings.Join(parts, " / ") + ")"
			}
			w("- **" + line(t.Decision) + "**" + opts + " → **" + line(t.Recommendation) + "** — " + line(t.Why))
		}
		w("")
	}
	for _, m := range p.Milestones {
		w("## " + m.ID + " — " + line(m.Name))
		w("")
		for _, ph := range m.Phases {
			w("### " + ph.ID + " — " + line(ph.Name))
			w("")
			w("| Task | Summary | Kind | Model/Effort | Deps | Test strategy |")
			w("| --- | --- | :--: | --- | --- | --- |")
			for _, t := range ph.Tasks {
				deps := "—"
				if len(t.Deps) > 0 {
					deps = strings.Join(t.Deps, ", ")
				}
				w("| " + t.ID + " — " + cell(t.Name) + " | " + cell(t.Summary) + " | " + string(t.Kind.Resolve()) + " | " + tierOf(t.Model, t.Effort) + " | " + deps + " | " + cell(t.TestStrategy) + " |")
			}
			w("")
			for _, t := range ph.Tasks {
				w("- **" + t.ID + " — " + line(t.Name) + "** — " + line(t.Deliverable))
				if t.OrchestratorOnly {
					w("  - **orchestrator-only** — runs inline in the orchestrator; refused for subagent dispatch")
				}
				if t.Thinking != "" {
					w("  - thinking: " + line(t.Thinking))
				}
				if len(t.FileSurface) > 0 {
					surfaces := make([]string, len(t.FileSurface))
					for i, e := range t.FileSurface {
						req := ""
						if e.Required {
							req = ", required"
						}
						surfaces[i] = line(e.Path) + " (" + string(e.Kind.Resolve()) + req + ")"
					}
					w("  - file surface: " + strings.Join(surfaces, ", "))
				}
				for _, a := range t.Acceptance {
					w("  - acceptance: " + line(a))
				}
			}
			w("")
		}
	}
	if len(p.Risks) > 0 {
		w("## Risks")
		for _, r := range p.Risks {
			fp := ""
			if r.ForcesPause {
				fp = " **[forces pause]**"
			}
			mit := ""
			if r.Mitigation != "" {
				mit = " — mitigation: " + line(r.Mitigation)
			}
			w("- " + line(r.Risk) + mit + fp)
		}
		w("")
	}
	if len(p.OpenQuestions) > 0 {
		w("## Open questions")
		for _, q := range p.OpenQuestions {
			w("- " + line(q))
		}
		w("")
	}
	return tripleNL.ReplaceAllString(b.String(), "\n\n")
}
