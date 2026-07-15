// Command build-helpers is the CLI front-end for the build-with-team deterministic helpers.
// It owns all filesystem IO and process exit codes; the logic lives in package bh (pure,
// testable). Invoked by the orchestrator skill via the `run` wrapper (build-once, then exec).
//
// Exit codes: 0 ok; 1 validation failed (validate / check-tiers report ok:false); 2 usage/IO error.
package main

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"build-helpers/bh"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "render":
		runRender(rest)
	case "diff":
		oldP := readPlan(arg(rest, 0, "diff <old-plan.json> <new-plan.json>"))
		newP := readPlan(arg(rest, 1, "diff <old-plan.json> <new-plan.json>"))
		printJSON(bh.Diff(oldP, newP))
	case "check-tiers":
		res := bh.CheckTiers(readPlan(arg(rest, 0, "check-tiers <plan.json>")))
		printJSON(res)
		exitOK(res.OK)
	case "hash":
		printJSON(bh.Hashes(readPlan(arg(rest, 0, "hash <plan.json>"))))
	case "validate":
		res := bh.ValidatePlanBytes(readFile(arg(rest, 0, "validate <plan.json>")))
		printJSON(res)
		exitOK(res.OK)
	case "classify":
		printJSON(runClassify(arg(rest, 0, "classify <project-dir>")))
	case "escalate":
		runEscalate(rest)
	case "classify-scope":
		runClassifyScope(rest)
	case "init-exec":
		runInitExec(rest)
	case "render-exec":
		ex := readExec(arg(rest, 0, "render-exec <execution.json> <plan.json>"))
		plan := readPlan(arg(rest, 1, "render-exec <execution.json> <plan.json>"))
		fmt.Print(bh.RenderExecution(ex, plan))
	case "next":
		ex := readExec(arg(rest, 0, "next <execution.json> <plan.json>"))
		plan := readPlan(arg(rest, 1, "next <execution.json> <plan.json>"))
		res := bh.NextTask(ex, plan)
		printJSON(res)
		// Structural refusal (design.md SCe): a non-nil OrchestratorOnly is a hard error, never a
		// prose caveat a caller could ignore — the orchestrator must run that task inline, not via
		// this dispatch path.
		exitOK(res.OrchestratorOnly == nil)
	case "batch":
		runBatch(rest)
	case "verify-surface":
		runVerifySurface(rest)
	case "check-changed-surface":
		runCheckChangedSurface(rest)
	case "record":
		runRecord(rest)
	case "log-note":
		runLogNote(rest)
	case "reconcile-exec":
		runReconcile(rest)
	case "archive":
		runArchive(rest)
	case "migrate-project":
		runMigrateProject(rest)
	case "usage":
		printJSON(readUsage(arg(rest, 0, "usage <transcript.jsonl>")))
	case "record-usage":
		runRecordUsage(rest)
	case "attribute":
		runAttribute(rest)
	case "retrieve":
		runRetrieve(rest)
	case "self-check":
		runSelfCheck(rest)
	case "resolve-transcript":
		runResolveTranscript(rest)
	case "feedback":
		runFeedback(rest)
	case "-h", "--help", "help":
		usage()
	default:
		die(2, "unknown command %q (try --help)\n", cmd)
	}
}

// ---- command runners that take flags ----
//
// Each peels its fixed positionals off the front, then lets a flag.FlagSet parse the rest.
// Go's flag stops at the first non-flag arg, so positionals MUST precede flags — the skill
// follows that convention and it's documented in usage().

func runClassify(dir string) bh.ClassifyResult {
	read := func(name string) (string, bool) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return "", false
		}
		return string(b), true
	}
	designText, designPresent := read("design.md")
	in := bh.ClassifyInput{DesignPresent: designPresent, DesignText: designText}
	if planRaw, ok := read("plan.json"); ok {
		var p struct {
			Provenance *bh.Provenance `json:"provenance"`
		}
		if json.Unmarshal([]byte(planRaw), &p) == nil {
			in.PlanPresent = true
			if p.Provenance != nil {
				in.PlanProvenanceDesignUpdated = p.Provenance.DesignUpdated
			}
		}
	}
	_, in.ExecutionPresent = read("execution.json")
	_, in.MirrorPresent = read("execution.md")
	return bh.Classify(in)
}

// runEscalate classifies one observed condition against the closed named trigger set (SC8). The
// magistrate route is reachable ONLY for an exact-match named trigger; --touches-success-criteria /
// --touches-scope force the design-level plan-with-team hand-back regardless of condition; anything
// else is no-escalation. Deterministic: no filesystem IO, one condition in, one route out.
func runEscalate(rest []string) {
	fs := flag.NewFlagSet("escalate", flag.ContinueOnError)
	condition := fs.String("condition", "", "observed condition label (matched exactly against the closed trigger set)")
	touchesSC := fs.Bool("touches-success-criteria", false, "delta touches design success_criteria (forces plan-with-team hand-back)")
	touchesScope := fs.Bool("touches-scope", false, "delta touches design scope (forces plan-with-team hand-back)")
	parse(fs, rest)
	if *condition == "" {
		die(2, "escalate: --condition is required\n")
	}
	printJSON(bh.ClassifyEscalation(bh.EscalationInput{
		Condition:              *condition,
		TouchesSuccessCriteria: *touchesSC,
		TouchesScope:           *touchesScope,
	}))
}

// runClassifyScope is the standalone scope predicate: a delta touching design success_criteria or
// scope is design-level -> pause + hand back to plan-with-team; neither -> in-scope.
func runClassifyScope(rest []string) {
	fs := flag.NewFlagSet("classify-scope", flag.ContinueOnError)
	touchesSC := fs.Bool("touches-success-criteria", false, "delta touches design success_criteria")
	touchesScope := fs.Bool("touches-scope", false, "delta touches design scope")
	parse(fs, rest)
	printJSON(bh.ClassifyScope(bh.ScopeInput{
		TouchesSuccessCriteria: *touchesSC,
		TouchesScope:           *touchesScope,
	}))
}

func runInitExec(rest []string) {
	pos := positionals(rest, 1, "init-exec <plan.json> --slug S [--name … --topic … --design-updated … --plan-updated … --pause … --budget … --rates … --override … --at …]")
	fs := flag.NewFlagSet("init-exec", flag.ContinueOnError)
	var o bh.InitExecOptions
	fs.StringVar(&o.Slug, "slug", "", "project slug (required)")
	fs.StringVar(&o.Name, "name", "", "execution doc name")
	fs.StringVar(&o.Topic, "topic", "", "topic tag")
	fs.StringVar(&o.DesignUpdated, "design-updated", "", "source design.md updated timestamp")
	fs.StringVar(&o.PlanUpdated, "plan-updated", "", "source plan.json updated timestamp")
	fs.StringVar(&o.Pause, "pause", "", "pause mode: task|phase|milestone|none")
	fs.StringVar(&o.Budget, "budget", "", "unlimited | $<amount>")
	fs.StringVar(&o.Rates, "rates", "", "list-price | negotiated")
	fs.StringVar(&o.Override, "override", "", "active override directive")
	at := fs.String("at", "", "timestamp override (default: now UTC)")
	parse(fs, rest[1:])
	o.At = orNow(*at)
	plan := readPlan(pos[0])
	ex, err := bh.InitExec(plan, o)
	if err != nil {
		die(2, "init-exec: %v\n", err)
	}
	printJSON(ex)
}

func runRecord(rest []string) {
	pos := positionals(rest, 2, "record <execution.json> <taskId> [--status … --test … --review … --commit … --cost … --tokens-out … --input-tokens … --cache-write-tokens … --cache-read-tokens … --usage-turns … --note … --run-id … --override … --at …]")
	fs := flag.NewFlagSet("record", flag.ContinueOnError)
	status := fs.String("status", "", "not-started|in-progress|blocked|failed|done|superseded")
	test := fs.String("test", "", "PASS|FAIL")
	review := fs.String("review", "", "ACCEPT|FIX-APPLIED|RETURN")
	commit := fs.String("commit", "", "short SHA")
	note := fs.String("note", "", "row note")
	runID := fs.String("run-id", "", "Workflow runId (same-session fast-resume)")
	override := fs.String("override", "", "active override directive")
	cost := fs.Float64("cost", 0, "task cost in USD")
	tokensOut := fs.Int64("tokens-out", 0, "measured output tokens for this task")
	inputTokens := fs.Int64("input-tokens", 0, "this task's measured input tokens")
	cacheWriteTokens := fs.Int64("cache-write-tokens", 0, "this task's measured cache-write (cache-creation) tokens")
	cacheReadTokens := fs.Int64("cache-read-tokens", 0, "this task's measured cache-read tokens")
	usageTurns := fs.Int64("usage-turns", 0, "this task's measured turn count (for the usage record)")
	at := fs.String("at", "", "timestamp override (default: now UTC)")
	parse(fs, rest[2:])

	// Only fields whose flag was actually set are applied (nil pointer == leave unchanged).
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	var f bh.RecordFields
	if set["status"] {
		s := bh.Status(*status)
		f.Status = &s
	}
	if set["test"] {
		f.Test = test
	}
	if set["review"] {
		f.Review = review
	}
	if set["commit"] {
		f.Commit = commit
	}
	if set["note"] {
		f.Note = note
	}
	if set["run-id"] {
		f.RunID = runID
	}
	if set["override"] {
		f.Override = override
	}
	if set["cost"] {
		f.Cost = cost
	}
	if set["tokens-out"] {
		f.TokensOut = tokensOut
	}
	// Usage (the four measured token classes) is recorded together, once any one of its flags is
	// set — a partial usage record (e.g. output-tokens with no input/cache) is still useful (it is
	// the same measured basis --tokens-out already carries), so no flag is required to be set jointly.
	if set["input-tokens"] || set["cache-write-tokens"] || set["cache-read-tokens"] || set["tokens-out"] || set["usage-turns"] {
		u := &bh.Usage{
			InputTokens:         *inputTokens,
			CacheCreationTokens: *cacheWriteTokens,
			CacheReadTokens:     *cacheReadTokens,
			OutputTokens:        *tokensOut,
			Turns:               *usageTurns,
			At:                  orNow(*at),
		}
		u.TotalTokens = u.InputTokens + u.CacheCreationTokens + u.CacheReadTokens + u.OutputTokens
		f.Usage = u
	}
	ex := readExec(pos[0])
	if err := bh.RecordTask(&ex, pos[1], f, orNow(*at)); err != nil {
		die(2, "%v\n", err)
	}
	printJSON(ex)
}

func runRender(rest []string) {
	pos := positionals(rest, 1, "render <plan.json> [--slug … --topic … --updated …]")
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	var meta bh.PlanDocMeta
	fs.StringVar(&meta.Slug, "slug", "", "project slug (emits plan.md frontmatter when set)")
	fs.StringVar(&meta.Topic, "topic", "", "topic tag")
	fs.StringVar(&meta.Updated, "updated", "", "plan.md updated timestamp")
	parse(fs, rest[1:])
	fmt.Print(bh.RenderPlan(readPlan(pos[0]), meta))
}

func runLogNote(rest []string) {
	pos := positionals(rest, 1, "log-note <execution.json> --note \"…\" [--at …]")
	fs := flag.NewFlagSet("log-note", flag.ContinueOnError)
	note := fs.String("note", "", "plan-level log entry (required)")
	at := fs.String("at", "", "timestamp override (default: now UTC)")
	parse(fs, rest[1:])
	if *note == "" {
		die(2, "log-note: --note is required\n")
	}
	ex := readExec(pos[0])
	bh.LogNote(&ex, *note, orNow(*at))
	printJSON(ex)
}

func runReconcile(rest []string) {
	pos := positionals(rest, 3, "reconcile-exec <execution.json> <old-plan.json> <new-plan.json> [--design-updated … --plan-updated … --at …]")
	fs := flag.NewFlagSet("reconcile-exec", flag.ContinueOnError)
	designUpdated := fs.String("design-updated", "", "new design.md updated timestamp")
	planUpdated := fs.String("plan-updated", "", "new plan.json updated timestamp")
	at := fs.String("at", "", "timestamp override (default: now UTC)")
	parse(fs, rest[3:])
	ex := readExec(pos[0])
	oldP := readPlan(pos[1])
	newP := readPlan(pos[2])
	bh.ReconcileExec(&ex, oldP, newP, *designUpdated, *planUpdated, orNow(*at))
	printJSON(ex)
}

// runArchive is the explicit, operator-invoked archive op: it
// moves every task under each named --milestone (which must be wholly terminal) out of the live
// plan.json/execution.json into the preserved archive.json, then rewrites all three files. This
// is the ONE command that writes multiple files itself rather than leaving the atomic
// write-temp-then-rename swap to its caller (every other command prints one JSON doc to stdout
// for `> tmp && mv tmp target`) — archive touches three docs in one call. It must never be wired
// into the build loop (next/batch/SKILL.md never call it) — archiving is deliberate cross-session
// hygiene, run between sessions or on demand, not a loop step.
func runArchive(rest []string) {
	pos := positionals(rest, 3, "archive <plan.json> <execution.json> <archive.json> --milestone ID[,ID…] [--at …]")
	fs := flag.NewFlagSet("archive", flag.ContinueOnError)
	milestones := fs.String("milestone", "", "comma-separated milestone id(s) to archive (required); every task under each must be done|superseded")
	at := fs.String("at", "", "timestamp override (default: now UTC)")
	parse(fs, rest[3:])
	ids := splitTasks(*milestones)
	if len(ids) == 0 {
		die(2, "archive: --milestone is required (comma-separated milestone id(s))\n")
	}

	plan := readPlan(pos[0])
	ex := readExec(pos[1])
	existing := readArchive(pos[2])

	out, err := bh.Archive(plan, ex, existing, bh.ArchiveOptions{MilestoneIDs: ids}, orNow(*at))
	if err != nil {
		die(2, "%v\n", err)
	}

	// Write order: archive.json first (durable copy of the full record), then execution.json,
	// then plan.json last — a crash between renames always favors preserving data (the archive
	// is committed before either live doc drops it) over losing it, and re-running archive is
	// idempotent, so a partial run self-heals on retry.
	writeJSONFile(pos[2], out.Archive)
	writeJSONFile(pos[1], out.Exec)
	writeJSONFile(pos[0], out.Plan)

	printJSON(struct {
		Archived []string `json:"archived"`
		Skipped  []string `json:"skipped,omitempty"`
	}{out.Archived, out.Skipped})
}

// runMigrateProject upgrades an in-flight v1 project's plan.json + execution.json to the v2 harness
// shapes (SC14): every entity's id+name (SC15), the execution schema_version stamp, and any retired
// enum id. It is lossless (task done/SHA/cost/verdicts and the log are preserved), idempotent (an
// already-v2 project rewrites nothing), and previewable (--dry-run prints the exact change list
// without touching either file). The change report is printed to stdout; the upgraded files are
// written atomically (write-temp-then-rename) only when there are changes AND --dry-run is off.
//
// execution.json is read RAW here (readExecRaw, not readExec) so MigrateProject sees the true
// on-disk schema_version and can report the stamp — readExec would migrate on load and hide it.
func runMigrateProject(rest []string) {
	pos := positionals(rest, 2, "migrate-project <plan.json> <execution.json> [--dry-run]")
	fs := flag.NewFlagSet("migrate-project", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "preview the exact changes without writing either file")
	parse(fs, rest[2:])

	plan := readPlan(pos[0])
	ex := readExecRaw(pos[1])

	rep, err := bh.MigrateProject(&plan, &ex, *dryRun)
	if err != nil {
		die(2, "migrate-project: %v\n", err)
	}
	if !rep.AlreadyV2 && !*dryRun {
		writeJSONFile(pos[0], plan)
		writeJSONFile(pos[1], ex)
	}
	printJSON(rep)
}

// discoverTranscripts resolves a session's transcript files from the main transcript path: the
// main transcript itself plus every subagent transcript found by bh.DiscoverSubagentTranscripts —
// the ONE recursive discovery seam that also feeds discoverSubagents/attribution,
// so the two views can never disagree on what counts as a subagent. Results are deduped by cleaned
// absolute path and sorted so the walk and its watermark keys are stable across runs. resolved is
// false when the main transcript cannot be stat'd (missing/unreadable) — the caller decides whether
// that is fatal: readUsage (the `usage` command) dies on it; runRecordUsage treats it as the
// unresolved-transcript condition (non-fatal marker, except a baseline-capture run — see
// SetAccountingUnresolved). A subagents/ root that has nothing under it is simply absent (a run with
// no subagents is valid). This is the whole-session set the old orchestrator-only parser missed, now
// including nested-workflow subagents the old fixed-depth SubagentGlobs also missed.
func discoverTranscripts(mainPath string) (paths []string, resolved bool) {
	mainAbs := absClean(mainPath)
	if _, err := os.Stat(mainAbs); err != nil {
		return nil, false
	}
	subs, err := bh.DiscoverSubagentTranscripts(mainPath)
	if err != nil {
		subs = nil // a walk error is a bug, not a user error; degrade to main-transcript-only rather than fail the run
	}
	seen := map[string]bool{mainAbs: true}
	paths = []string{mainAbs}
	for _, a := range subs {
		if !seen[a] {
			seen[a] = true
			paths = append(paths, a)
		}
	}
	sort.Strings(paths)
	return paths, true
}

// discoverSubagents resolves ONLY the subagent transcripts from the main transcript path (every
// agent-*.jsonl found by bh.DiscoverSubagentTranscripts, at any depth), excluding the main
// transcript itself. Task attribution is per-subagent: the orchestrator/main transcript is not a
// subagent dispatch and its cost is session overhead, not attributable to a single task. Results
// are deduped by cleaned absolute path and sorted so attribution output is stable across runs. A
// walk that finds nothing is simply absent (a run with no fan-out subagents yields an empty set,
// which Attribute handles).
func discoverSubagents(mainPath string) []string {
	subs, err := bh.DiscoverSubagentTranscripts(mainPath)
	if err != nil {
		return nil
	}
	mainAbs := absClean(mainPath)
	seen := map[string]bool{mainAbs: true}
	var paths []string
	for _, a := range subs {
		if !seen[a] {
			seen[a] = true
			paths = append(paths, a)
		}
	}
	sort.Strings(paths)
	return paths
}

// openSources opens each transcript file and seeks it to its prior watermark, returning the sources
// to fold plus the handles to close. A watermark past the current file size (rotation/truncation)
// resets that file to a full re-parse from offset 0. When prior is nil (fresh / --final) every file
// starts at offset 0. Files that fail to open are skipped (best-effort, whole-session true cost).
func openSources(paths []string, prior *bh.Accounting) ([]bh.TranscriptSource, []*os.File) {
	var sources []bh.TranscriptSource
	var handles []*os.File
	for _, p := range paths {
		fh, err := os.Open(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "build-helpers: skipping unreadable transcript %s: %v\n", p, err)
			continue
		}
		handles = append(handles, fh)
		var start int64
		if prior != nil {
			if wm, ok := prior.Watermarks[p]; ok {
				if fi, err := fh.Stat(); err == nil && wm <= fi.Size() {
					if _, err := fh.Seek(wm, io.SeekStart); err == nil {
						start = wm
					}
				}
			}
		}
		sources = append(sources, bh.TranscriptSource{FileID: p, Reader: fh, StartOffset: start})
	}
	return sources, handles
}

// readUsage parses true cumulative token usage across the WHOLE session — the main transcript plus
// every subagent transcript (input + cache + output, all turns). The transcript format is internal
// to Claude Code and may change between releases — the parser is best-effort and tolerates
// malformed lines and unreadable subagent files.
func readUsage(path string) bh.Usage {
	paths, resolved := discoverTranscripts(path)
	if !resolved {
		die(2, "cannot read transcript %s\n", path)
	}
	sources, handles := openSources(paths, nil)
	defer closeAll(handles)
	acct, err := bh.Account(nil, sources, nil, "")
	if err != nil {
		die(2, "parse transcript %s: %v\n", path, err)
	}
	return acct.Flatten()
}

// loadRates reads the rate table from the specs file, contract-preferred when run config selects
// negotiated rates. Best-effort: an unreadable/invalid specs file yields a nil table (buckets are
// still summed, cost is left at $0) with a warning, since true-cost accounting must never block a run.
func loadRates(specsPath, ratesMode string) bh.RateTable {
	b, err := os.ReadFile(specsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build-helpers: specs %s unreadable, recording token buckets without cost: %v\n", specsPath, err)
		return nil
	}
	table, err := bh.LoadRateTable(b, ratesMode == "negotiated")
	if err != nil {
		fmt.Fprintf(os.Stderr, "build-helpers: specs %s invalid, recording token buckets without cost: %v\n", specsPath, err)
		return nil
	}
	return table
}

// defaultSpecsPath locates anthropic-specifications.json relative to the executable
// (<skill>/build-helpers/.bin/build-helpers -> <skill>/anthropic-specifications.json), falling back
// to a cwd-relative path. Override with --specs.
func defaultSpecsPath() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "..", "..", "anthropic-specifications.json")
	}
	return "anthropic-specifications.json"
}

func runRecordUsage(rest []string) {
	pos := positionals(rest, 1, "record-usage <execution.json> --transcript <path> [--specs P --final --baseline-capture --at …]")
	fs := flag.NewFlagSet("record-usage", flag.ContinueOnError)
	transcript := fs.String("transcript", "", "main session transcript JSONL path (required); subagent transcripts are discovered alongside it")
	specs := fs.String("specs", "", "anthropic-specifications.json path (default: resolved next to the executable)")
	final := fs.Bool("final", false, "finish-time authoritative snapshot: re-parse every transcript in full, ignoring prior watermarks — mandatory at finish")
	baselineCapture := fs.Bool("baseline-capture", false, "this run IS a baseline-capture measurement (e.g. a model-tier comparison); an unresolved transcript fails the run instead of writing the non-fatal cost_status:unresolved marker")
	at := fs.String("at", "", "timestamp override (default: now UTC)")
	parse(fs, rest[1:])
	if *transcript == "" {
		die(2, "record-usage: --transcript is required\n")
	}
	ex := readExec(pos[0])
	when := orNow(*at)

	// The main transcript failing to open/read is "unresolved", not silently zero-valued
	// accounting. Non-fatal by default (accounting never blocks a build) — a loud cost_status:
	// unresolved marker is persisted and prior accounting is left untouched. A baseline-capture run
	// (e.g. a model-tier comparison measurement) cannot tolerate a silently-degraded O, so it fails instead.
	paths, resolved := discoverTranscripts(*transcript)
	if !resolved {
		if *baselineCapture {
			die(1, "record-usage: baseline-capture run failed — main transcript %s unresolved\n", *transcript)
		}
		bh.SetAccountingUnresolved(&ex, *transcript, when)
		printJSON(ex)
		return
	}

	// Incremental by default (resume from prior per-file watermarks); --final forces a full re-parse
	// from offset 0 so the finish-time snapshot is authoritative and self-heals any incremental drift.
	// --final is mandatory at finish and is idempotent/resume-safe: a re-run always re-derives
	// the same ledger/O from the transcripts on disk rather than accumulating on top of itself.
	prior := ex.RunConfig.Accounting
	if *final {
		prior = nil
	}
	specsPath := *specs
	if specsPath == "" {
		specsPath = defaultSpecsPath()
	}
	specsBytes, specsErr := os.ReadFile(specsPath)
	specsAsOf := ""
	if specsErr == nil {
		specsAsOf = bh.SpecsAsOf(specsBytes)
	}
	rates := loadRates(specsPath, ex.RunConfig.Rates)

	mainAbs := absClean(*transcript)
	sources, handles := openSources(paths, prior)
	defer closeAll(handles)
	acct, err := bh.Account(prior, sources, rates, when)
	if err != nil {
		die(2, "record-usage: parse transcript %s: %v\n", *transcript, err)
	}
	bh.SetAccounting(&ex, acct, mainAbs, rates, *final, when, specsAsOf, buildHelpersSHA())
	printJSON(ex)
}

// buildHelpersSHA returns the sha256 of the currently-running build-helpers executable
// (hex-encoded), pinning WHICH binary produced a given accounting snapshot (see
// Accounting.BuildHelpersSHA). Best-effort: the executable path being unresolvable/unreadable
// (e.g. `go test`'s synthesized test binary, an unusual packaging) yields "" rather than an
// error — accounting must never block a run over a provenance field.
func buildHelpersSHA() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum)
}

// runAttribute produces the MEASURED per-task cost breakdown for a batch's subagent transcripts,
// replacing the batch even-split estimate. It discovers the subagent transcripts alongside the main
// transcript, extracts each one's spawning task ID from its first user turn, and attributes its true
// per-model cost to that task (EXACT-match against the known task-id set). A transcript that maps to
// no known task falls into a flagged even-split pool — surfaced, never dropped. The known set is
// --tasks (comma-separated) when given, else every task ID already in execution.json.
func runAttribute(rest []string) {
	pos := positionals(rest, 1, "attribute <execution.json> --transcript <path> [--tasks id,id,… --specs P --at …]")
	fs := flag.NewFlagSet("attribute", flag.ContinueOnError)
	transcript := fs.String("transcript", "", "main session transcript JSONL path (required); subagent transcripts are discovered alongside it")
	tasks := fs.String("tasks", "", "comma-separated known task IDs to match against (default: every task ID in execution.json)")
	specs := fs.String("specs", "", "anthropic-specifications.json path (default: resolved next to the executable)")
	at := fs.String("at", "", "timestamp override (default: now UTC)")
	parse(fs, rest[1:])
	if *transcript == "" {
		die(2, "attribute: --transcript is required\n")
	}
	ex := readExec(pos[0])

	known := splitTasks(*tasks)
	if len(known) == 0 {
		for _, t := range ex.Tasks {
			known = append(known, t.ID)
		}
	}
	specsPath := *specs
	if specsPath == "" {
		specsPath = defaultSpecsPath()
	}
	rates := loadRates(specsPath, ex.RunConfig.Rates)

	paths := discoverSubagents(*transcript)
	var sources []bh.AttribSource
	var handles []*os.File
	for _, p := range paths {
		fh, err := os.Open(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "build-helpers: skipping unreadable transcript %s: %v\n", p, err)
			continue
		}
		handles = append(handles, fh)
		sources = append(sources, bh.AttribSource{FileID: p, Reader: fh})
	}
	defer closeAll(handles)

	attr, err := bh.Attribute(sources, known, rates, orNow(*at))
	if err != nil {
		die(2, "attribute: parse transcripts under %s: %v\n", *transcript, err)
	}
	printJSON(attr)
}

// splitTasks parses a comma-separated task-id list into a trimmed, empty-dropped slice.
func splitTasks(csv string) []string {
	var out []string
	for _, part := range strings.Split(csv, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func closeAll(handles []*os.File) {
	for _, h := range handles {
		h.Close()
	}
}

func runBatch(rest []string) {
	pos := positionals(rest, 2, "batch <execution.json> <plan.json> [--max N]")
	fs := flag.NewFlagSet("batch", flag.ContinueOnError)
	max := fs.Int("max", 4, "max tasks to dispatch in one fan-out (default 4; hard cap 8)")
	parse(fs, rest[2:])
	if *max > bh.MaxBatch {
		*max = bh.MaxBatch
	}
	ex := readExec(pos[0])
	plan := readPlan(pos[1])
	res := bh.BatchTasks(ex, plan, *max)
	printJSON(res)
	// Structural refusal — same hard-error contract as `next`.
	exitOK(res.OrchestratorOnly == nil)
}

// runVerifySurface is the forward-direction file_surface re-assertion: looks up one or more
// comma-separated taskIds' declared file_surface in plan.json and checks their UNION against
// disk, rooted at --root (default the current directory). A single id is the engine's pre-done
// assertion re-run independently by the orchestrator at the pre-commit site — the caller passes
// the task's own worktree. Comma-joined ids is the post-merge site: after an octopus merge, the
// orchestrator re-verifies every merged task's surface in one call, rooted at the merged
// build-worktree tip — this is the ONE check downstream of the merge itself, catching a path
// present at every task's own pre-commit but dropped by the merge. Exit 1 (with the violation
// list) when any declared entry fails its pinned match semantics (bh.VerifyFileSurface via
// bh.VerifyMergedSurface).
func runVerifySurface(rest []string) {
	pos := positionals(rest, 2, "verify-surface <plan.json> <taskId>[,<taskId>…] [--root DIR]")
	fs := flag.NewFlagSet("verify-surface", flag.ContinueOnError)
	root := fs.String("root", ".", "directory the file_surface paths are relative to (a task's own worktree at pre-commit, or the build worktree post-merge)")
	parse(fs, rest[2:])
	plan := readPlan(pos[0])
	var perTaskSurfaces [][]bh.FileSurfaceEntry
	for _, id := range strings.Split(pos[1], ",") {
		id = strings.TrimSpace(id)
		r, found := bh.TaskByID(plan, id)
		if !found {
			die(2, "verify-surface: task %q not found in %s\n", id, pos[0])
		}
		perTaskSurfaces = append(perTaskSurfaces, r.Task.FileSurface)
	}
	res := bh.VerifyMergedSurface(os.DirFS(*root), perTaskSurfaces)
	printJSON(res)
	exitOK(res.OK)
}

// runCheckChangedSurface is the reverse-direction file_surface re-assertion:
// every path in the git-derived changed-set must be covered by taskId's declared file_surface
// (required or optional). An uncovered path is an off-surface write that a
// forward-only check (verify-surface) cannot see. --changed reads one bare path per line from a
// file, or stdin when given "-"; the caller strips git status --porcelain's 2-char status +
// separator before piping in (e.g. `git status --porcelain | cut -c4-`) — this command adds no
// second path-discovery mechanism of its own.
func runCheckChangedSurface(rest []string) {
	pos := positionals(rest, 2, "check-changed-surface <plan.json> <taskId> --changed FILE|-")
	fs := flag.NewFlagSet("check-changed-surface", flag.ContinueOnError)
	changed := fs.String("changed", "", "path to a newline-delimited changed-set (bare paths, e.g. git status --porcelain | cut -c4-), or - for stdin (required)")
	parse(fs, rest[2:])
	if *changed == "" {
		die(2, "check-changed-surface: --changed is required\n")
	}
	plan := readPlan(pos[0])
	r, found := bh.TaskByID(plan, pos[1])
	if !found {
		die(2, "check-changed-surface: task %q not found in %s\n", pos[1], pos[0])
	}
	var raw []byte
	if *changed == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			die(2, "check-changed-surface: cannot read stdin: %v\n", err)
		}
		raw = b
	} else {
		raw = readFile(*changed)
	}
	var lines []string
	for _, l := range strings.Split(string(raw), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	res := bh.VerifyChangedSetSubsetOfSurface(lines, r.Task.FileSurface)
	printJSON(res)
	exitOK(res.OK)
}

// isArchiveDoc sniffs a doc's shape ahead of isExecutionDoc's plan/exec fork:
// archive.json embeds a top-level "milestones" array too (each archived milestone carries its
// full plan-slice per task, bh/archive.go), which would otherwise misroute to RetrievePlan under
// isExecutionDoc's presence-of-"milestones" test. archive.json's own "schema" key (ArchiveSchema)
// is the one unambiguous discriminator, so this check MUST run before isExecutionDoc.
func isArchiveDoc(raw []byte) bool {
	var probe struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return probe.Schema == bh.ArchiveSchema
}

// isExecutionDoc sniffs a doc's shape (never its filename) so `retrieve` accepts either
// plan.json or execution.json under one command: only Plan declares a top-level "milestones"
// array, so its absence means the doc is execution.json. Malformed JSON is treated as
// execution.json so the exec-path unmarshal below is what surfaces the real parse error. Callers
// must check isArchiveDoc first — archive.json also declares "milestones" and would otherwise
// misroute here as a plan doc.
func isExecutionDoc(raw []byte) bool {
	var probe struct {
		Milestones json.RawMessage `json:"milestones"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return true
	}
	return probe.Milestones == nil
}

// runRetrieve serves the level-of-detail retrieval API: a read-only,
// deterministic projection of plan.json, execution.json, or archive.json at the caller's chosen
// granularity — outline (every entity) -> milestone/phase (one group's child tasks) -> task (one
// task's full record) -> field (one named field). It never decides task eligibility; `next`/
// `batch` remain the sole authority on what runs next. Dispatch order matters: isArchiveDoc runs
// before isExecutionDoc because archive.json's shape would otherwise match the plan-doc probe.
func runRetrieve(rest []string) {
	pos := positionals(rest, 1, "retrieve <plan.json|execution.json|archive.json> --level {outline|milestone|phase|task|field} [--id ID] [--field NAME]")
	fs := flag.NewFlagSet("retrieve", flag.ContinueOnError)
	level := fs.String("level", "", "outline|milestone|phase|task|field (required)")
	id := fs.String("id", "", "entity id (required for milestone/phase/task/field)")
	field := fs.String("field", "", "field name (required for level field)")
	parse(fs, rest[1:])

	in := bh.RetrieveInput{Level: bh.RetrievalLevel(*level), ID: *id, Field: *field}
	raw := readFile(pos[0])
	var (
		result any
		err    error
	)
	switch {
	case isArchiveDoc(raw):
		result, err = bh.RetrieveArchive(readArchive(pos[0]), in)
	case isExecutionDoc(raw):
		result, err = bh.RetrieveExec(readExec(pos[0]), in)
	default:
		result, err = bh.RetrievePlan(readPlan(pos[0]), in)
	}
	if err != nil {
		die(2, "%v\n", err)
	}
	printJSON(result)
}

// runSelfCheck is the session-tier self-check: resolves the LIVE session model and
// effort and enforces the caller-supplied floor/ceiling band (never hardcoded here — each skill
// passes its own). Model: transcript latest message.model, fallback $ANTHROPIC_MODEL. Effort:
// $CLAUDE_EFFORT, fallback settings.json effortLevel. An undetectable model is a hard usage error
// (nothing to check against); an undetectable effort is not — bh.SelfCheck degrades to
// enforcing the model band alone and warns. Exit 1 on abort (below floor OR — see below — a
// session-id mismatch), matching check-tiers/validate's ok-gate convention; a ceiling warning
// still exits 0.
//
// Identity guard (opt-in): pass --session-id or --scratchpad-path to ALSO verify that
// --transcript's trailing lines carry THIS session's own sessionId before trusting it for
// anything — closing the silent cross-session accounting poisoning a stale mtime-newest transcript
// pick could cause. Omitting both flags preserves model/effort tier check only (SessionIDChecked
// stays false in the JSON result).
//
// Exit-code contract: 0 in band and id verified (or id check not requested); 1 abort (below the
// floor, and/or — when the identity guard is requested — a session-id mismatch); 2 usage/IO error
// (missing required band flags, undeterminable model, or — when the identity guard is requested —
// an unparseable/missing session id, unreadable/empty/all-malformed transcript, or a transcript
// that names no sessionId on any line).
func runSelfCheck(rest []string) {
	fs := flag.NewFlagSet("self-check", flag.ContinueOnError)
	transcript := fs.String("transcript", "", "main session transcript JSONL path (model source; latest message.model wins over $ANTHROPIC_MODEL; also the identity-guard source when --session-id/--scratchpad-path is given)")
	settings := fs.String("settings", ".claude/settings.json", "settings.json path (effortLevel fallback when $CLAUDE_EFFORT is unset)")
	floorModel := fs.String("floor-model", "", "band floor model (required)")
	floorEffort := fs.String("floor-effort", "", "band floor effort (required)")
	ceilingModel := fs.String("ceiling-model", "", "band ceiling model (required)")
	ceilingEffort := fs.String("ceiling-effort", "", "band ceiling effort (required)")
	sessionID := fs.String("session-id", "", "SCf identity guard: this session's own id (UUID) -- verifies --transcript's trailing lines name THIS session, hard-abort on mismatch; mutually exclusive with --scratchpad-path; omit both to skip the guard entirely")
	scratchpadPath := fs.String("scratchpad-path", "", "SCf identity guard: a path under this session's own scratchpad dir (session id parsed from it); mutually exclusive with --session-id; omit both to skip the guard entirely")
	parse(fs, rest)
	if *floorModel == "" || *floorEffort == "" || *ceilingModel == "" || *ceilingEffort == "" {
		die(2, "self-check: --floor-model, --floor-effort, --ceiling-model, --ceiling-effort are all required\n")
	}
	band := bh.TierBand{
		FloorModel:    bh.Model(*floorModel),
		FloorEffort:   bh.Effort(*floorEffort),
		CeilingModel:  bh.Model(*ceilingModel),
		CeilingEffort: bh.Effort(*ceilingEffort),
	}

	model, modelOK := resolveSessionModel(*transcript)
	if !modelOK {
		die(2, "self-check: cannot determine session model (transcript %q unreadable/empty and $ANTHROPIC_MODEL unset)\n", *transcript)
	}
	effort, effortOK := resolveSessionEffort(*settings)

	res := bh.SelfCheck(model, effort, effortOK, band)

	if *sessionID != "" || *scratchpadPath != "" {
		applySessionIDGuard(&res, *transcript, *sessionID, *scratchpadPath)
	}

	printJSON(res)
	exitOK(!res.Abort)
}

// applySessionIDGuard runs the SCf identity guard in place on res: it derives the caller's own
// session id (exactly one of sessionIDFlag/scratchpadPathFlag must be set — runSelfCheck only
// calls this when at least one is), reads transcriptPath's trailing lines, and folds
// bh.CheckSessionID's verdict into res (Abort is OR'd in, never cleared; Reason is appended, never
// overwritten). Every precondition failure here is a usage/IO error (exit 2 via die), distinct
// from a genuine mismatch (exit 1 via res.Abort) — a caller can tell "we couldn't check" apart
// from "we checked and it's wrong".
func applySessionIDGuard(res *bh.SelfCheckResult, transcriptPath, sessionIDFlag, scratchpadPathFlag string) {
	want, _, err := resolveOwnSessionID(sessionIDFlag, scratchpadPathFlag)
	if err != nil {
		die(2, "self-check: %v\n", err)
	}
	if transcriptPath == "" {
		die(2, "self-check: --transcript is required to verify session id\n")
	}
	f, err := os.Open(transcriptPath)
	if err != nil {
		die(2, "self-check: cannot open transcript %q for session id verification: %v\n", transcriptPath, err)
	}
	scan := bh.LatestTranscriptSessionID(f)
	f.Close()
	switch {
	case scan.Lines == 0:
		die(2, "self-check: transcript %q is empty\n", transcriptPath)
	case scan.Parsed == 0:
		die(2, "self-check: transcript %q has no parseable JSONL lines\n", transcriptPath)
	case !scan.Found():
		die(2, "self-check: transcript %q names no sessionId on any line\n", transcriptPath)
	}

	idRes := bh.CheckSessionID(want, scan)
	res.SessionIDChecked = true
	res.SessionIDMatch = idRes.SessionIDMatch
	if idRes.Abort {
		res.Abort = true
		if res.Reason == "" {
			res.Reason = idRes.Reason
		} else {
			res.Reason = res.Reason + "; " + idRes.Reason
		}
	}
}

// resolveOwnSessionID derives the caller's own session id from exactly one of --session-id
// (explicit) or --scratchpad-path (parsed) — the single source-of-truth seam SCf uses for BOTH
// resolve-transcript's path construction and self-check's identity guard, so the two subcommands
// can never disagree on how a session names itself. cwdSlug is only populated when derived via
// --scratchpad-path (ParseScratchpadPath's own cwd-slug segment); callers deriving via
// --session-id must slug a --cwd themselves (see runResolveTranscript). err is non-nil (never
// itself fatal — callers decide the exact die() message/exit code) when: neither/both flags are
// set, an explicit id fails bh.ValidSessionID, or a scratchpad path fails bh.ParseScratchpadPath.
func resolveOwnSessionID(sessionIDFlag, scratchpadPathFlag string) (id, cwdSlug string, err error) {
	switch {
	case sessionIDFlag != "" && scratchpadPathFlag != "":
		return "", "", fmt.Errorf("exactly one of --session-id or --scratchpad-path is allowed, not both")
	case sessionIDFlag != "":
		if !bh.ValidSessionID(sessionIDFlag) {
			return "", "", fmt.Errorf("%q is not a valid session id", sessionIDFlag)
		}
		return sessionIDFlag, "", nil
	case scratchpadPathFlag != "":
		slug, sid, ok := bh.ParseScratchpadPath(scratchpadPathFlag)
		if !ok {
			return "", "", fmt.Errorf("could not parse a session id from scratchpad path %q", scratchpadPathFlag)
		}
		return sid, slug, nil
	default:
		return "", "", fmt.Errorf("one of --session-id or --scratchpad-path is required")
	}
}

// runResolveTranscript is the deterministic transcript resolver: given a session's own
// identity (--session-id or --scratchpad-path — the SAME derivation self-check's identity guard
// uses, via resolveOwnSessionID), it prints the ONE transcript path that session owns. No
// directory listing and no os.ModTime comparison occur anywhere in this resolution path — see
// bh.ResolveTranscriptPath's doc comment. Replaces the old "newest *.jsonl mtime in the cwd's
// projects dir" heuristic, which could silently hand a concurrent session's transcript to a
// caller under concurrent sessions sharing a cwd.
//
// Exit-code contract: 0 and the path on stdout when the transcript exists; 2 usage/IO error for
// every failure mode — neither/both of --session-id/--scratchpad-path set, an unparseable/invalid
// session id, an unresolvable cwd or $HOME (only reached when --session-id is used without
// --projects-dir), or the deterministically-resolved path not existing on disk. There is no exit
// 1: this command never "fails a check", it either resolves a real path or cannot resolve one.
func runResolveTranscript(rest []string) {
	fs := flag.NewFlagSet("resolve-transcript", flag.ContinueOnError)
	sessionID := fs.String("session-id", "", "explicit session id (UUID); mutually exclusive with --scratchpad-path")
	scratchpadPath := fs.String("scratchpad-path", "", "a path under the session's own scratchpad dir (.../<cwd-slug>/<session-id>/scratchpad[/...]); cwd-slug AND session id are both parsed from it; mutually exclusive with --session-id")
	cwd := fs.String("cwd", "", "working directory to slug for the projects-dir lookup, used ONLY with --session-id (default: the process's own cwd); ignored with --scratchpad-path, whose own cwd-slug segment is used instead")
	projectsDir := fs.String("projects-dir", "", "root of the Claude Code projects tree (default: $HOME/.claude/projects)")
	parse(fs, rest)

	id, slugFromPath, err := resolveOwnSessionID(*sessionID, *scratchpadPath)
	if err != nil {
		die(2, "resolve-transcript: %v\n", err)
	}

	slug := slugFromPath
	if slug == "" {
		wd := *cwd
		if wd == "" {
			var wderr error
			wd, wderr = os.Getwd()
			if wderr != nil {
				die(2, "resolve-transcript: cannot determine cwd: %v\n", wderr)
			}
		}
		slug = bh.SlugifyCWD(wd)
	}

	root := *projectsDir
	if root == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			die(2, "resolve-transcript: cannot determine home directory: %v\n", herr)
		}
		root = filepath.Join(home, ".claude", "projects")
	}

	path := bh.ResolveTranscriptPath(root, slug, id)
	if _, statErr := os.Stat(path); statErr != nil {
		die(2, "resolve-transcript: transcript not found at %s (session %s never wrote one there, or --cwd/--projects-dir resolved to the wrong project dir)\n", path, id)
	}
	fmt.Println(path)
}

// resolveSessionModel resolves the live session model: transcript latest message.model, falling
// back to $ANTHROPIC_MODEL (a launch-time setting that misses a mid-session /model override).
// An empty transcriptPath or an unreadable/model-less transcript
// simply falls through to the env var; ok is false only when NEITHER source names a model.
func resolveSessionModel(transcriptPath string) (bh.Model, bool) {
	if transcriptPath != "" {
		if f, err := os.Open(transcriptPath); err == nil {
			defer f.Close()
			if m, ok := bh.LatestTranscriptModel(f); ok {
				return m, true
			}
		}
	}
	if v := os.Getenv("ANTHROPIC_MODEL"); v != "" {
		return bh.Model(v), true
	}
	return "", false
}

// resolveSessionEffort resolves the live session effort: $CLAUDE_EFFORT, falling back to
// settings.json's effortLevel (a static value that misses a mid-session /effort override). ok is
// false only when NEITHER source names an effort — the documented "undetectable" case bh.SelfCheck
// degrades to a model-only band check for.
func resolveSessionEffort(settingsPath string) (bh.Effort, bool) {
	if v := os.Getenv("CLAUDE_EFFORT"); v != "" {
		return bh.Effort(strings.ToLower(v)), true
	}
	if b, err := os.ReadFile(settingsPath); err == nil {
		if e, ok := bh.ParseSettingsEffort(b); ok {
			return e, true
		}
	}
	return "", false
}

// ---- feedback register: feedback.json canonical, feedback.md rendered mirror ----

// runFeedback dispatches the feedback subcommands: `add` (writes) and `list`
// (read-only query) — list never touches add's write path.
func runFeedback(rest []string) {
	if len(rest) == 0 {
		die(2, "usage: feedback {add|list} <feedback.json> [flags]\n")
	}
	sub, rest := rest[0], rest[1:]
	switch sub {
	case "add":
		runFeedbackAdd(rest)
	case "list":
		runFeedbackList(rest)
	case "gate":
		runFeedbackGate(rest)
	default:
		die(2, "unknown feedback subcommand %q (try: add, list, gate)\n", sub)
	}
}

// runFeedbackAdd validates and appends one entry to feedback.json, then re-renders feedback.md
// from the SAME updated register in the same call — the write that keeps canonical and mirror
// from ever diverging. feedback.md's path is derived from feedback.json's,
// mirroring the plan.json/plan.md and execution.json/execution.md sibling-file convention.
func runFeedbackAdd(rest []string) {
	pos := positionals(rest, 1, "feedback add <feedback.json> --title … --feedback … --impact N --urgency N [--source-task … --proposed-solution … --why-it-matters … --at …]")
	fs := flag.NewFlagSet("feedback add", flag.ContinueOnError)
	var in bh.FeedbackInput
	fs.StringVar(&in.Title, "title", "", "short human name — required")
	fs.StringVar(&in.SourceTaskID, "source-task", "", "originating task id")
	fs.StringVar(&in.Feedback, "feedback", "", "the feedback itself — required")
	fs.StringVar(&in.ProposedSolution, "proposed-solution", "", "proposed fix")
	fs.StringVar(&in.WhyItMatters, "why-it-matters", "", "why this matters / impact rationale")
	fs.IntVar(&in.Impact, "impact", 0, "impact score 1-5 — required")
	fs.IntVar(&in.Urgency, "urgency", 0, "urgency score 1-5 — required")
	at := fs.String("at", "", "timestamp override (default: now UTC)")
	parse(fs, rest[1:])

	path := pos[0]
	reg, err := bh.AddFeedback(readFeedback(path), in, orNow(*at))
	if err != nil {
		die(2, "%v\n", err)
	}
	writeJSONFile(path, reg)
	writeTextFile(feedbackMirrorPath(path), bh.RenderFeedback(reg))
	printJSON(reg)
}

// runFeedbackList reads feedback.json (never writes it or its mirror) and prints entries matching
// the supplied filters — --by-task, --min-impact, --min-urgency compose with AND; omitting all
// three lists every entry. Output is one line per entry, ranked by criticality descending, in the
// exact "<id> — <title>" form every feedback readout uses.
func runFeedbackList(rest []string) {
	pos := positionals(rest, 1, "feedback list <feedback.json> [--by-task ID] [--min-impact N] [--min-urgency N]")
	fs := flag.NewFlagSet("feedback list", flag.ContinueOnError)
	var f bh.FeedbackFilter
	fs.StringVar(&f.SourceTaskID, "by-task", "", "filter to entries whose source task id exactly matches")
	fs.IntVar(&f.MinImpact, "min-impact", 0, "filter to entries with impact >= N")
	fs.IntVar(&f.MinUrgency, "min-urgency", 0, "filter to entries with urgency >= N")
	parse(fs, rest[1:])

	reg := readFeedback(pos[0])
	for _, e := range bh.ListFeedback(reg, f) {
		fmt.Println(e.ID + " — " + e.Title)
	}
}

// runFeedbackGate partitions the ranked register at the configurable criticality threshold,
// emitting the full gate result as JSON: amend_now is the ranked reconcile-exec
// amendment input the magistrate consumes; deferred entries are applied to the supplied plan as
// the standing feedback-review milestone (the emitted `plan` is the new-plan.json for
// reconcile-exec's sub-threshold restacking). Read-only — writes nothing; the magistrate/skill
// pipes `plan` into reconcile-exec. The threshold is a required parameter, never hardcoded.
func runFeedbackGate(rest []string) {
	pos := positionals(rest, 1, "feedback gate <feedback.json> --plan <plan.json> --threshold N")
	fs := flag.NewFlagSet("feedback gate", flag.ContinueOnError)
	planPath := fs.String("plan", "", "plan.json the feedback-review milestone is applied to — required")
	threshold := fs.Int("threshold", 0, "inclusive criticality floor for amend-now (>= amends, < defers) — required")
	parse(fs, rest[1:])
	if strings.TrimSpace(*planPath) == "" {
		die(2, "feedback gate: --plan <plan.json> is required\n")
	}
	printJSON(bh.GatePlanFeedback(readPlan(*planPath), readFeedback(pos[0]), *threshold))
}

// feedbackMirrorPath derives feedback.md's path from feedback.json's path.
func feedbackMirrorPath(jsonPath string) string {
	return strings.TrimSuffix(jsonPath, filepath.Ext(jsonPath)) + ".md"
}

// readFeedback loads feedback.json, tolerating an absent file (a project's first-ever
// `feedback add`) as an empty, freshly-schema-stamped register rather than an IO error —
// matching readArchive's first-call convention.
func readFeedback(path string) bh.FeedbackRegister {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return bh.FeedbackRegister{Schema: bh.FeedbackSchema}
		}
		die(2, "cannot read %s: %v\n", path, err)
	}
	var r bh.FeedbackRegister
	if err := json.Unmarshal(b, &r); err != nil {
		die(2, "invalid feedback JSON in %s: %v\n", path, err)
	}
	return r
}

// writeTextFile writes Markdown content to path via write-temp-then-rename in the same directory
// — the same atomic discipline as writeJSONFile, applied here because `feedback add` writes its
// feedback.md mirror directly (like `archive`, not the `> tmp && mv tmp target` shell convention
// every stdout-emitting command's caller performs).
func writeTextFile(path, content string) {
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, ".feedback-tmp-*")
	if err != nil {
		die(2, "cannot create temp file for %s: %v\n", path, err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		die(2, "cannot write %s: %v\n", path, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		die(2, "cannot write %s: %v\n", path, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		die(2, "cannot finalize %s: %v\n", path, err)
	}
}

// ---- arg / flag plumbing ----

// positionals returns the first n args, dying with usage if there are too few.
func positionals(rest []string, n int, usageLine string) []string {
	if len(rest) < n {
		die(2, "usage: %s\n", usageLine)
	}
	return rest[:n]
}

// parse runs fs.Parse and converts a parse error into a clean exit-2.
func parse(fs *flag.FlagSet, args []string) {
	if err := fs.Parse(args); err != nil {
		die(2, "%s: %v\n", fs.Name(), err)
	}
}

func arg(rest []string, i int, usageLine string) string {
	if i >= len(rest) {
		die(2, "usage: %s\n", usageLine)
	}
	return rest[i]
}

// ---- IO ----

// absClean returns the cleaned absolute form of a path, used as the stable watermark key so a
// transcript resolves to the same key whether it is passed relatively or absolutely across runs.
func absClean(path string) string {
	if a, err := filepath.Abs(path); err == nil {
		return filepath.Clean(a)
	}
	return filepath.Clean(path)
}

func readFile(path string) []byte {
	b, err := os.ReadFile(path)
	if err != nil {
		die(2, "cannot read %s: %v\n", path, err)
	}
	return b
}

func readPlan(path string) bh.Plan {
	var p bh.Plan
	if err := json.Unmarshal(readFile(path), &p); err != nil {
		die(2, "invalid plan JSON in %s: %v\n", path, err)
	}
	return p
}

// readExec loads execution.json and migrates it in place to the current schema version — the
// single, explicit upgrade step for every command that reads execution state (record, next,
// batch, render-exec, reconcile-exec, log-note). A pre-versioned or older-version file loads
// losslessly and is stamped current in memory; the next command that writes (printJSON) persists
// the upgrade. A newer-than-supported version is a hard error, not a silent corruption risk.
func readExec(path string) bh.ExecState {
	var e bh.ExecState
	if err := json.Unmarshal(readFile(path), &e); err != nil {
		die(2, "invalid execution JSON in %s: %v\n", path, err)
	}
	if err := bh.MigrateExec(&e); err != nil {
		die(2, "%v\n", err)
	}
	return e
}

// readExecRaw loads execution.json WITHOUT the on-load schema migration readExec performs. Only
// migrate-project uses it: it must see the true on-disk schema_version so MigrateProject can detect
// and report the stamp. Every other reader wants readExec's auto-upgrade.
func readExecRaw(path string) bh.ExecState {
	var e bh.ExecState
	if err := json.Unmarshal(readFile(path), &e); err != nil {
		die(2, "invalid execution JSON in %s: %v\n", path, err)
	}
	return e
}

// readArchive loads archive.json, tolerating an absent file (a project's first-ever archive
// call) as an empty archive doc rather than an IO error.
func readArchive(path string) bh.ArchiveDoc {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return bh.ArchiveDoc{Schema: bh.ArchiveSchema}
		}
		die(2, "cannot read %s: %v\n", path, err)
	}
	var a bh.ArchiveDoc
	if err := json.Unmarshal(b, &a); err != nil {
		die(2, "invalid archive JSON in %s: %v\n", path, err)
	}
	return a
}

func printJSON(v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		die(2, "cannot encode output: %v\n", err)
	}
	fmt.Println(string(b))
}

// writeJSONFile marshals v and writes it to path via write-temp-then-rename in the same
// directory, so a crash mid-write never leaves a torn file — the same atomic discipline every
// other command's caller performs with `> tmp && mv tmp target` (SKILL.md), applied here because
// `archive` is the one command that writes multiple files itself in a single call.
func writeJSONFile(path string, v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		die(2, "cannot encode %s: %v\n", path, err)
	}
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, ".archive-tmp-*")
	if err != nil {
		die(2, "cannot create temp file for %s: %v\n", path, err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		die(2, "cannot write %s: %v\n", path, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		die(2, "cannot write %s: %v\n", path, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		die(2, "cannot finalize %s: %v\n", path, err)
	}
}

func orNow(at string) string {
	if at != "" {
		return at
	}
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}

func exitOK(ok bool) {
	if !ok {
		os.Exit(1)
	}
}

func die(code int, format string, a ...any) {
	fmt.Fprintf(os.Stderr, "build-helpers: "+format, a...)
	os.Exit(code)
}

func usage() {
	fmt.Fprint(os.Stderr, `build-helpers — deterministic mechanics for the build-with-team orchestrator

Usage:
  build-helpers render         <plan.json>                          -> plan.md
  build-helpers diff           <old-plan.json> <new-plan.json>      -> {carried,changed,added,removed}
  build-helpers check-tiers    <plan.json>                          -> {ok,issues}; exit 1 if not ok
  build-helpers hash           <plan.json>                          -> {taskId: contentHash}
  build-helpers validate       <plan.json>                          -> {ok,errors,warnings}; exit 1 if not ok
  build-helpers classify       <project-dir>                        -> {design,plan,execution,route}
  build-helpers escalate       --condition NAME [--touches-success-criteria --touches-scope] -> {route: magistrate|plan-with-team|no-escalation,...} closed named trigger set, no catch-all
  build-helpers classify-scope [--touches-success-criteria --touches-scope] -> {route: plan-with-team|in-scope,...} design-level delta hands back to plan-with-team
  build-helpers init-exec      <plan.json> --slug S [flags]         -> execution.json
  build-helpers render-exec    <execution.json> <plan.json>         -> execution.md
  build-helpers next           <execution.json> <plan.json>         -> {task}|{done}|{blocked}
  build-helpers batch          <execution.json> <plan.json> [--max N] -> {tasks}|{done}|{blocked}
  build-helpers verify-surface <plan.json> <taskId>[,<taskId>…] [--root DIR] -> {ok,violations}; exit 1 if not ok. FORWARD direction: checks the UNION of the listed tasks' declared file_surface against disk (glob >=1 match, dir non-empty, required entries non-trivial/non-empty) rooted at --root (default cwd). One id = pre-commit re-assertion (a task's own worktree); comma-joined ids = post-merge re-assertion (the merged build-worktree tip, unioned across the merged batch).
  build-helpers check-changed-surface <plan.json> <taskId> --changed FILE|- -> {ok,off_surface}; exit 1 if not ok. REVERSE direction: every path in a git-derived changed-set (one bare path per line, e.g. 'git status --porcelain | cut -c4-', "-" reads stdin) must be covered by taskId's declared file_surface (required or optional); an uncovered path is an off-surface write.
  build-helpers record         <execution.json> <taskId> [flags]    -> execution.json
  build-helpers log-note       <execution.json> --note "…" [--at …] -> execution.json
  build-helpers reconcile-exec <execution.json> <old> <new> [flags] -> execution.json
  build-helpers archive        <plan.json> <execution.json> <archive.json> --milestone ID[,ID…] [--at …] -> {archived,skipped}; rewrites all three files. Operator-invoked ONLY — never call from the build loop. Refuses (no partial write) unless every task under each named milestone is done|superseded.
  build-helpers migrate-project <plan.json> <execution.json> [--dry-run] -> {already_v2,dry_run,changes,warnings}; upgrades an in-flight v1 project to the v2 shapes (entity names, execution schema_version stamp, retired-enum remap). Lossless (done/SHA/cost/verdicts preserved), idempotent (already-v2 rewrites nothing), --dry-run previews without writing.
  build-helpers usage          <transcript.jsonl>                   -> {input,output,cache,total,turns} true tokens (whole session incl. subagents)
  build-helpers record-usage   <execution.json> --transcript P [--specs P --final --baseline-capture] -> execution.json (folds per-model true-cost accounting + per-file watermarks + orchestrator-only O into run config; --final mandatory at finish; an unresolved transcript sets cost_status:unresolved — non-fatal, unless --baseline-capture)
  build-helpers attribute      <execution.json> --transcript P [--tasks id,id,… --specs P] -> {tasks:{id:{cost_usd,cost_attribution:"measured",…}},even_split,unmappable} MEASURED per-task cost from subagent transcripts
  build-helpers retrieve       <plan.json|execution.json|archive.json> --level {outline|milestone|phase|task|field} [--id ID --field NAME] -> read-only detail-level projection (never decides eligibility); archive.json serves archived detail at the same four levels
  build-helpers self-check     --floor-model M --floor-effort E --ceiling-model M --ceiling-effort E [--transcript P --settings P --session-id ID | --scratchpad-path P] -> {model,effort,effort_detected,abort,warnings,reason,session_id_checked,session_id_match} session-tier self-check against a caller-supplied band; --session-id/--scratchpad-path additionally hard-aborts if --transcript's trailing lines don't name that session id. Exit 0 ok; 1 abort (below floor and/or session-id mismatch); 2 usage/IO error (missing band flags, undeterminable model, or — with the identity guard — unparseable session id / unreadable / empty / all-malformed / no-sessionId transcript)
  build-helpers resolve-transcript --session-id ID | --scratchpad-path P [--cwd DIR --projects-dir DIR] -> prints the ONE deterministic transcript path for that session (id-based path join, never a directory mtime scan). Exit 0 ok (path on stdout); 2 usage/IO error (bad/missing id, unresolvable cwd/$HOME, or the path doesn't exist)
  build-helpers feedback add   <feedback.json> --title … --feedback … --impact N --urgency N [--source-task … --proposed-solution … --why-it-matters … --at …] -> feedback.json (writes feedback.json AND regenerates the sibling feedback.md mirror in one call)
  build-helpers feedback list  <feedback.json> [--by-task ID --min-impact N --min-urgency N] -> stdout, one "<id> — <title>" line per matching entry, ranked by criticality descending (read-only; filters compose with AND)
  build-helpers feedback gate  <feedback.json> --plan <plan.json> --threshold N -> {threshold,amend_now,deferred,plan} partition the ranked register at the inclusive criticality floor: amend_now is ranked reconcile-exec amendment input; deferred entries are applied to plan as the standing feedback-review milestone (read-only; the returned plan is the new-plan.json for reconcile-exec)

Exit codes: 0 ok; 1 validation failed; 2 usage/IO error.
Positionals precede flags: <positionals…> --key value (Go flag stops at the first positional).
`)
}
