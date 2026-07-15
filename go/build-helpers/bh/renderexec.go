package bh

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"
)

// RenderExecution renders the human-readable execution.md mirror from canonical state + the
// plan (for milestone/phase names and order). The mirror carries a do-not-hand-edit banner;
// execution.json is the source of truth.
func RenderExecution(ex ExecState, p Plan) string {
	var b strings.Builder
	w := func(s string) { b.WriteString(s); b.WriteByte('\n') }

	rowByID := map[string]ExecTask{}
	for _, t := range ex.Tasks {
		rowByID[t.ID] = t
	}
	nameByID := map[string]string{}
	for _, r := range WalkTasks(p) {
		nameByID[r.Task.ID] = r.Task.Name
	}
	topic := cmp.Or(ex.Topic, "tooling")

	// frontmatter (description is required by the workspace schema)
	w("---")
	w("name: " + ex.Name)
	w(fmt.Sprintf("description: %q", fmt.Sprintf("Live execution-state mirror for the %s project — generated from execution.json (canonical); per-task status, verdicts, commit SHAs, cost, and resume pointer. Do not hand-edit.", ex.Project)))
	w("id: project:" + ex.Project + ":execution")
	w("tags:")
	for _, tg := range []string{"type:project", "topic:" + topic, "status:complete"} {
		w("  - " + tg)
	}
	w("links: []")
	w("updated: " + ex.Updated)
	w("---")
	w("")
	w("# " + ex.Name)
	w("")
	w("> Generated mirror of `execution.json` (canonical). Do not hand-edit — re-render with `build-helpers render-exec`.")
	w("")
	w("- **Design:** `design.md` (proposal/context; source of truth)")
	w("- **Plan:** `plan.json` (immutable build spec) · readable mirror `plan.md`")
	w(fmt.Sprintf("- **Derived from:** plan.json @ %s · design.md @ %s", dash(ex.Provenance.PlanUpdated), dash(ex.Provenance.DesignUpdated)))
	w("- **Goal:** " + cell(ex.Goal))
	w(fmt.Sprintf("- **Started:** %s · **Updated:** %s", ex.Started, ex.Updated))
	w("")
	w("## Run config")
	w("- pause mode: `" + ex.RunConfig.PauseMode + "`")
	w("- budget: " + ex.RunConfig.Budget)
	w(fmt.Sprintf("- spent: $%.2f   <!-- cumulative OUTPUT-cost (lower bound; input tokens not priced) -->", ex.RunConfig.SpentUSD))
	w(fmt.Sprintf("- output tokens (measured): %s   <!-- engine-measured per-task output; same basis as spent -->", commaInt(ex.RunConfig.TokensOut)))
	if u := ex.RunConfig.TrueUsage; u != nil {
		w(fmt.Sprintf("- true tokens (transcript): %s in + %s out + %s cache = **%s total** over %d turns   <!-- whole session incl. subagents; internal transcript format, best-effort -->",
			commaInt(u.InputTokens), commaInt(u.OutputTokens), commaInt(u.CacheCreationTokens+u.CacheReadTokens), commaInt(u.TotalTokens), u.Turns))
	}
	if a := ex.RunConfig.Accounting; a != nil {
		w(fmt.Sprintf("- true cost (transcript): **$%.4f** across %d models   <!-- whole session incl. subagents; input+cache+output priced per anthropic-specifications.json -->", a.CostUSD, len(a.Models)))
		if len(a.Unpriced) > 0 {
			w("- ⚠️ unpriced models (no rate matched, excluded from cost): " + strings.Join(a.Unpriced, ", "))
		}
	}
	w("- rates: " + ex.RunConfig.Rates)
	w("- override: " + orNone(ex.RunConfig.Override))
	w("- last runId: " + dash(ex.RunConfig.LastRunID) + "   <!-- SAME-SESSION fast-resume only; discard on a fresh session -->")
	w("")
	w("## Resume pointer")
	switch np := NextTask(ex, p); {
	case np.Done:
		w("**All tasks ✅ — build complete.**")
	case np.Task != nil:
		w(fmt.Sprintf("**Resume here →** %s — %s", np.Task.ID, cell(np.Task.Name)))
	case np.OrchestratorOnly != nil:
		w(fmt.Sprintf("**Orchestrator-only, run inline (refused for dispatch) →** %s — %s", np.OrchestratorOnly.ID, cell(np.OrchestratorOnly.Name)))
	default:
		w("**Blocked →** " + cell(orFallback(np.Reason, "no eligible task")))
	}
	w("")
	w("## Progress")
	curM, curP := "", ""
	for _, m := range p.Milestones {
		for _, ph := range m.Phases {
			for _, t := range ph.Tasks {
				if m.ID != curM {
					w("")
					w("### " + m.ID + " — " + cell(m.Name))
					curM, curP = m.ID, ""
				}
				if ph.ID != curP {
					w("")
					w("#### " + ph.ID + " — " + cell(ph.Name))
					w("| Task | Status | Kind | Model/Effort | Test | Review | Commit | Out-tok | Updated | Notes |")
					w("| --- | :--: | :--: | --- | :--: | :--: | --- | --: | --- | --- |")
					curP = ph.ID
				}
				row, ok := rowByID[t.ID]
				if !ok {
					row = ExecTask{ID: t.ID, Status: StatusNotStarted}
				}
				label := row.ID
				if row.Status == StatusSuperseded {
					label = "~~" + row.ID + "~~"
				}
				outTok := "—"
				if row.TokensOut > 0 {
					outTok = commaInt(row.TokensOut)
				}
				w(fmt.Sprintf("| %s — %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |",
					label, cell(nameByID[row.ID]), row.Status.Emoji(), string(row.Kind.Resolve()), tierOf(row.Model, row.Effort),
					orDash(row.Test), orDash(row.Review), orDash(row.Commit), outTok, orDash(row.Updated), orDash(cell(row.Notes))))
			}
		}
	}
	w("")
	w("## Log")
	if len(ex.Log) == 0 {
		w("- (none yet)")
	}
	for _, e := range ex.Log {
		w("- " + e)
	}
	w("")
	return tripleNL.ReplaceAllString(b.String(), "\n\n")
}

// commaInt formats an integer with thousands separators for the human mirror (e.g. 1234567 →
// "1,234,567"). Token totals are large; grouping keeps the run-config and table readable.
func commaInt(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		if neg {
			return "-" + s
		}
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func dash(s string) string   { return cmp.Or(s, "—") }
func orDash(s string) string { return dash(s) }
func orNone(s string) string { return cmp.Or(s, "none") }
func orFallback(s, fb string) string {
	if strings.TrimSpace(s) == "" {
		return fb
	}
	return s
}
