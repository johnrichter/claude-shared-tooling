package toolchain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
)

// TestWorkflowPairResolves checks WORKFLOW-PAIR is a first-class dispatch entry:
// its PairID is "workflow lint", ResolveCheck returns it with no unsupported
// diagnostic, and its one tool is actionlint (SC1, WFRULES clause (a)).
func TestWorkflowPairResolves(t *testing.T) {
	entry, diag := ResolveCheck(LanguageWorkflow, CheckLint, "")
	if diag != nil {
		t.Fatalf("ResolveCheck(workflow, lint) returned unsupported diagnostic %+v", diag)
	}
	if got := entry.PairID(); got != "workflow lint" {
		t.Fatalf("PairID = %q, want %q", got, "workflow lint")
	}
	if len(entry.Tools) != 1 || entry.Tools[0] != actionlintTool {
		t.Fatalf("tools = %v, want [%s]", entry.Tools, actionlintTool)
	}
	if entry.Config != "" {
		t.Errorf("config = %q, want empty (SC37: actionlint takes no config row)", entry.Config)
	}
}

// TestWorkflowEnumeration checks enumerateWorkflowBodies selects exactly
// WFRULES clause (a)'s two shapes from the git-tracked tree and nothing else: a
// .yml/.yaml under the workflow directory, at any depth, and any
// *.github-workflow.yml anywhere. A plugin-shipped body outside a workflow
// directory is the shape a bare actionlint run misses, so its inclusion is the
// load-bearing case. The population is census.py's tracked() set, so a tracked
// body under a linked worktree, a project directory or a fixture tree is
// dropped, and an untracked body never enters at all.
func TestWorkflowEnumeration(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The three tracked bodies the two shapes select.
	write(".github/workflows/ci.yml", "on: push\n")
	write(".github/workflows/nested/deep.yaml", "on: push\n")
	write("plugins/p/gate/navigator-gate.github-workflow.yml", "on: push\n")
	// Tracked, but not a workflow body by shape.
	write(".github/other.yml", "not: a workflow\n")    // not under a workflow dir
	write("docs/readme.yaml", "not: a workflow\n")     // not under a workflow dir
	write("plugins/p/values.yml", "not: a workflow\n") // plain yml, wrong suffix
	// Tracked workflow bodies census.py's tracked() drops: a linked worktree, a
	// project directory, and a fixture tree. They are staged below, so their
	// absence proves the prefix/substring exclusion rather than mere
	// untrackedness.
	write(".claude/worktrees/wt/.github/workflows/ci.yml", "on: push\n")
	write(".dat/effort/x.github-workflow.yml", "on: push\n")
	write("go/internal/testdata/ci.github-workflow.yml", "on: push\n")

	gitInitAdd(t, root)

	// An untracked workflow body, written after staging: the raw walk picked it
	// up, the tracked-tree read does not.
	write(".github/workflows/untracked.yml", "on: push\n")

	got, err := enumerateWorkflowBodies(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(root, ".github/workflows/ci.yml"),
		filepath.Join(root, ".github/workflows/nested/deep.yaml"),
		filepath.Join(root, "plugins/p/gate/navigator-gate.github-workflow.yml"),
	}
	if len(got) != len(want) {
		t.Fatalf("enumerated %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("body[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

// TestWorkflowActionlintExplicitVsBare records E4: a fixture tree holding a
// clean workflow-directory body and a faulty plugin-shipped body proves a bare
// actionlint invocation (which scans the workflow directory alone) misses the
// second, while the adapter's explicit path list catches it.
func TestWorkflowActionlintExplicitVsBare(t *testing.T) {
	if _, err := exec.LookPath(actionlintTool); err != nil {
		t.Skipf("%s not on PATH; skipping (not a defect in the adapter)", actionlintTool)
	}
	root := t.TempDir()
	// actionlint's bare invocation discovers workflows relative to the git
	// project root, so the fixture is a git repository.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH; the bare-invocation half needs a git project")
	}
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Skipf("git init failed: %v\n%s", err, out)
	}
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	clean := "name: ci\non: push\njobs:\n  a:\n    runs-on: ubuntu-24.04\n    steps:\n      - run: echo hi\n        shell: bash\n"
	// An undefined-variable expression is a deliberate actionlint fault.
	faulty := "name: gate\non: push\njobs:\n  a:\n    runs-on: ubuntu-24.04\n    steps:\n      - run: echo ${{ zzz.nope }}\n        shell: bash\n"
	write(".github/workflows/ci.yml", clean)
	write("plugins/p/gate/navigator-gate.github-workflow.yml", faulty)
	// The explicit-list half reads the git-tracked tree, so both bodies must be
	// staged for enumeration to see them.
	if out, err := exec.Command("git", "-C", root, "add", "-A").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}

	// Bare invocation: from the repository root, actionlint scans the workflow
	// directory alone and never sees the plugin-shipped body, so it exits 0.
	bare := exec.Command(actionlintTool, "-no-color")
	bare.Dir = root
	if out, err := bare.CombinedOutput(); err != nil {
		t.Fatalf("bare actionlint exited non-zero, so it did not miss the shipped body as expected: %v\n%s", err, out)
	}

	// Explicit list: the adapter passes every enumerated path, so it catches
	// the shipped body's fault.
	bodies, err := enumerateWorkflowBodies(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	diags, err := workflowAdapter{}.runActionlint(context.Background(), root, bodies)
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) == 0 {
		t.Fatal("explicit path list produced no diagnostic, so it missed the shipped body's fault")
	}
	found := false
	for _, d := range diags {
		if d.File == "plugins/p/gate/navigator-gate.github-workflow.yml" {
			found = true
		}
	}
	if !found {
		t.Errorf("no diagnostic named the shipped body; diags = %+v", diags)
	}
}

// TestWorkflowInheritance is fleet rule one's subject-classification table
// (WFRULES clause (b)): both subject cases, the non-subject case, the
// DD6-exempt case, and — crucially for E3 — cases proving the YAML parse
// decides a run: step rather than a line pattern.
func TestWorkflowInheritance(t *testing.T) {
	const compliantUses = "johnrichter/claude-shared-tooling/.github/workflows/ci-go.yml@v3.0.0"
	const branchUses = "johnrichter/claude-shared-tooling/.github/workflows/ci-go.yml@main"

	job := func(steps string) string {
		return "name: w\non: push\njobs:\n  a:\n    runs-on: ubuntu-24.04\n    steps:\n" + steps
	}
	callerJob := func(uses string) string {
		return "name: w\non: push\njobs:\n  a:\n    uses: " + uses + "\n"
	}

	cases := []struct {
		name       string
		rel        string
		body       string
		wantEmit   bool         // an error Diagnostic
		wantSilent noticeReason // the silent reason when not emitting
	}{
		{
			name:     "subject via banned run, no template — fails",
			rel:      ".github/workflows/ci.yml",
			body:     job("      - run: go test ./...\n"),
			wantEmit: true,
		},
		{
			name:     "subject with compliant uses but a banned run — still fails",
			rel:      ".github/workflows/ci.yml",
			body:     "name: w\non: push\njobs:\n  a:\n    runs-on: ubuntu-24.04\n    steps:\n      - uses: actions/checkout@v4\n      - run: cargo test\n  b:\n    uses: " + compliantUses + "\n",
			wantEmit: true,
		},
		{
			name:     "subject via compliant template uses, no banned run — passes",
			rel:      ".github/workflows/ci.yml",
			body:     callerJob(compliantUses),
			wantEmit: false,
		},
		{
			name:     "subject via template uses at a branch ref — fails",
			rel:      ".github/workflows/ci.yml",
			body:     callerJob(branchUses),
			wantEmit: true,
		},
		{
			name:       "job but no fleet check — non-subject, silent not_a_check_subject",
			rel:        ".github/workflows/gate.yml",
			body:       job("      - uses: actions/checkout@v4\n      - run: echo hello\n"),
			wantEmit:   false,
			wantSilent: reasonNotACheckSubject,
		},
		{
			name:     "DD6-exempt template body with a banned run — outside the rule",
			rel:      ".github/workflows/ci-go.yml",
			body:     job("      - run: go build ./...\n"),
			wantEmit: false,
		},
		{
			name:     "same-repository path ref, no banned run — passes",
			rel:      ".github/workflows/ci.yml",
			body:     callerJob("./.github/workflows/ci-go.yml"),
			wantEmit: false,
		},
		{
			// E3: "go test" appears only in a step name, never in a run: body,
			// so the parse resolves no banned run: step and the body is a
			// non-subject — a line pattern would have wrongly flagged it.
			name:       "banned text in a step name, not a run body — non-subject",
			rel:        ".github/workflows/named.yml",
			body:       job("      - name: go test manually\n        uses: actions/checkout@v4\n"),
			wantEmit:   false,
			wantSilent: reasonNotACheckSubject,
		},
		{
			// E3: a commented line inside a run: body is skipped, exactly as
			// census.py:scan_run_lines skips it, so this is not a subject.
			name:       "banned command in a run: comment line — non-subject",
			rel:        ".github/workflows/commented.yml",
			body:       job("      - run: |\n          # go test ./...\n          echo done\n"),
			wantEmit:   false,
			wantSilent: reasonNotACheckSubject,
		},
	}

	dir := t.TempDir()
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(dir, "body", string(rune('a'+i))+".yml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(c.body), 0o644); err != nil {
				t.Fatal(err)
			}
			wf, err := parseWorkflowBody(path)
			if err != nil {
				t.Fatal(err)
			}
			diag, emit, silent := evaluateInheritance(c.rel, wf)
			if emit != c.wantEmit {
				t.Fatalf("emit = %v (%+v), want %v", emit, diag, c.wantEmit)
			}
			if emit && diag.Severity != SeverityError {
				t.Errorf("failing subject severity = %q, want error", diag.Severity)
			}
			if !emit && silent != c.wantSilent {
				t.Errorf("silent reason = %q, want %q", silent, c.wantSilent)
			}
		})
	}
}

// relEntry is one tag for a test catalog, with RFC 3339 dates.
type relEntry struct {
	name, commit, published string
}

func mustCatalog(t *testing.T, entries ...relEntry) *releaseCatalog {
	t.Helper()
	cat := &releaseCatalog{}
	for _, e := range entries {
		tr, err := buildTaggedRelease(e.name, e.commit, e.published)
		if err != nil {
			t.Fatalf("build tag %q: %v", e.name, err)
		}
		cat.tags = append(cat.tags, tr)
	}
	return cat
}

// TestWorkflowCurrency is fleet rule two's verdict table (WFRULES clause (c)):
// the three verdicts, the inclusive 90-day boundary from both sides, the
// major-behind error regardless of age, the commit-date fallback for a tag with
// no release record, and the two look-alike lookup outcomes (no_tag vs
// path_ref).
func TestWorkflowCurrency(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	at := func(days int) string { return now.Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339) }

	cat := mustCatalog(t,
		relEntry{name: "v3.1.0", published: at(1)},           // highest release
		relEntry{name: "v3.0.9", published: at(89)},          // same major, inside window
		relEntry{name: "v3.0.1", published: at(90)},          // same major, exactly at the boundary
		relEntry{name: "v2.5.0", published: at(1)},           // one major behind, recent
		relEntry{name: "v3.0.5", commit: at(30)},             // same major, no release record
		relEntry{name: "go/toolchain/v0.3.0", commit: at(1)}, // path-prefixed: not in the comparison set
	)

	cases := []struct {
		name       string
		ref        templateRef
		wantKind   verdictKind
		wantReason noticeReason
	}{
		{"highest release is current", templateRef{version: "v3.1.0"}, verdictCurrent, ""},
		{"same major inside window warns", templateRef{version: "v3.0.9"}, verdictWarn, ""},
		{"exactly 90 days errors (inclusive)", templateRef{version: "v3.0.1"}, verdictError, ""},
		{"major behind errors regardless of age", templateRef{version: "v2.5.0"}, verdictError, ""},
		{"no release record ages by commit date", templateRef{version: "v3.0.5"}, verdictWarn, ""},
		{"ref resolving to no tag is silent no_tag", templateRef{version: "v9.9.9"}, verdictSilent, reasonNoTag},
		{"path ref is silent path_ref", templateRef{isPath: true, raw: "./.github/workflows/ci-go.yml"}, verdictSilent, reasonPathRef},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := evaluateCurrency(c.ref, cat, "", now)
			if v.kind != c.wantKind {
				t.Fatalf("kind = %d, want %d (%+v)", v.kind, c.wantKind, v)
			}
			if v.kind == verdictSilent && v.reason != c.wantReason {
				t.Errorf("reason = %q, want %q", v.reason, c.wantReason)
			}
		})
	}
}

// TestWorkflowCurrencyLookupFailure checks a nil catalog (a classified lookup
// failure) yields the silent verdict at the carried reason, while a path ref
// stays path_ref even when the lookup failed — the rule never fails on an
// unresolved lookup (WFRULES clause (d)).
func TestWorkflowCurrencyLookupFailure(t *testing.T) {
	now := time.Now().UTC()
	for _, r := range []noticeReason{reasonNoNetwork, reasonNoCredential, reasonRateLimited, reasonServerError} {
		v := evaluateCurrency(templateRef{version: "v3.0.0"}, nil, r, now)
		if v.kind != verdictSilent || v.reason != r {
			t.Errorf("nil catalog with %q: verdict = %+v, want silent %q", r, v, r)
		}
	}
	v := evaluateCurrency(templateRef{isPath: true}, nil, reasonRateLimited, now)
	if v.kind != verdictSilent || v.reason != reasonPathRef {
		t.Errorf("path ref under a failed lookup = %+v, want silent path_ref", v)
	}
}

// TestWorkflowReasonSetClosed is the reason-set exhaustiveness proof (WFRULES
// clause (d)): exactly seven members, every one valid and reachable, and no
// eighth accepted.
func TestWorkflowReasonSetClosed(t *testing.T) {
	if len(allNoticeReasons) != 7 {
		t.Fatalf("reason set has %d members, want 7", len(allNoticeReasons))
	}
	seen := map[noticeReason]bool{}
	for _, r := range allNoticeReasons {
		if !r.valid() {
			t.Errorf("canonical reason %q reports invalid", r)
		}
		if seen[r] {
			t.Errorf("reason %q appears twice", r)
		}
		seen[r] = true
	}
	if noticeReason("eighth").valid() {
		t.Error("an invented reason reports valid; the set is not closed")
	}

	// Reachability: each member is produced by some evaluation path.
	reached := map[noticeReason]bool{}
	now := time.Now().UTC()
	for _, r := range []noticeReason{reasonNoNetwork, reasonNoCredential, reasonRateLimited, reasonServerError} {
		reached[evaluateCurrency(templateRef{version: "v1.0.0"}, nil, r, now).reason] = true
	}
	reached[evaluateCurrency(templateRef{isPath: true}, mustCatalog(t, relEntry{name: "v1.0.0", published: now.Format(time.RFC3339)}), "", now).reason] = true
	reached[evaluateCurrency(templateRef{version: "v9.9.9"}, mustCatalog(t, relEntry{name: "v1.0.0", published: now.Format(time.RFC3339)}), "", now).reason] = true
	wf, err := parseWorkflowBody(writeTemp(t, "name: w\non: push\njobs:\n  a:\n    runs-on: ubuntu-24.04\n    steps:\n      - run: echo hi\n"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, silent := evaluateInheritance(".github/workflows/w.yml", wf)
	reached[silent] = true

	for _, r := range allNoticeReasons {
		if !reached[r] {
			t.Errorf("reason %q is unreachable by any evaluation path", r)
		}
	}
}

// writeTemp writes body to a temp file and returns its path.
func writeTemp(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "w.yml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestWorkflowReleasesFileSeam proves WFRULES clause (e): the warning verdict
// and both error verdicts are reachable through LANGUAGE_TOOLS_RELEASES_FILE,
// which is the only path to them since SC15 leaves one real release.
func TestWorkflowReleasesFileSeam(t *testing.T) {
	now := time.Now().UTC()
	at := func(days int) string { return now.Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339) }
	file := filepath.Join(t.TempDir(), "releases.json")
	doc := `{"tags":[
	  {"name":"v3.1.0","commit_date":"` + at(1) + `","published_at":"` + at(1) + `"},
	  {"name":"v3.0.0","commit_date":"` + at(30) + `","published_at":"` + at(30) + `"},
	  {"name":"v3.0.1","commit_date":"` + at(120) + `","published_at":"` + at(120) + `"},
	  {"name":"v2.0.0","commit_date":"` + at(1) + `","published_at":"` + at(1) + `"}
	]}`
	if err := os.WriteFile(file, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(releasesFileEnv, file)

	cat, reason, err := resolveReleaseSource().catalog(context.Background())
	if err != nil {
		t.Fatalf("file source catalog: %v", err)
	}
	if cat == nil {
		t.Fatalf("file source returned nil catalog with reason %q", reason)
	}
	cases := []struct {
		version  string
		wantKind verdictKind
	}{
		{"v3.0.0", verdictWarn},  // same major, 30 days
		{"v3.0.1", verdictError}, // same major, 120 days > window
		{"v2.0.0", verdictError}, // one major behind
	}
	for _, c := range cases {
		v := evaluateCurrency(templateRef{version: c.version}, cat, "", now)
		if v.kind != c.wantKind {
			t.Errorf("%s verdict kind = %d, want %d", c.version, v.kind, c.wantKind)
		}
	}
}

// TestWorkflowReleaseSourceFallback checks that an unset seam variable falls
// back to the platform API source rather than to silence (WFRULES clause (e)).
func TestWorkflowReleaseSourceFallback(t *testing.T) {
	t.Setenv(releasesFileEnv, "")
	if _, ok := resolveReleaseSource().(apiReleaseSource); !ok {
		t.Fatalf("unset %s did not fall back to the API source", releasesFileEnv)
	}
}

// TestWorkflowExitMapping checks the severity-to-exit precursors EXIT and F83
// pin: a currency warning is a SeverityWarning (a warning-carrying run is a
// success the result layer promotes to caveats, exit 10) and a rule-one failure
// is a SeverityError (gate_negative, exit 20). The exit codes themselves come
// from clikit, restated here so the mapping cannot drift.
func TestWorkflowExitMapping(t *testing.T) {
	warn, ok := currencyVerdict{kind: verdictWarn, message: "x"}.diagnostic("w.yml")
	if !ok || warn.Severity != SeverityWarning {
		t.Errorf("warn verdict diagnostic = %+v (ok=%v), want a SeverityWarning", warn, ok)
	}
	errDiag, ok := currencyVerdict{kind: verdictError, message: "x"}.diagnostic("w.yml")
	if !ok || errDiag.Severity != SeverityError {
		t.Errorf("error verdict diagnostic = %+v (ok=%v), want a SeverityError", errDiag, ok)
	}
	if _, ok := (currencyVerdict{kind: verdictCurrent}).diagnostic("w.yml"); ok {
		t.Error("current verdict produced a diagnostic; it must leave the run green")
	}
	if _, ok := (currencyVerdict{kind: verdictSilent, reason: reasonNoTag}).diagnostic("w.yml"); ok {
		t.Error("silent verdict produced a diagnostic; a notice must fail no step")
	}
	if clikit.StatusCaveats.ExitCode() != 10 {
		t.Errorf("caveats exit = %d, want 10", clikit.StatusCaveats.ExitCode())
	}
	if clikit.StatusGateNegative.ExitCode() != 20 {
		t.Errorf("gate_negative exit = %d, want 20", clikit.StatusGateNegative.ExitCode())
	}
}

// TestWorkflowRunEndToEnd exercises the whole adapter through Run over a fixture
// repository: a rule-one-failing body exits gate_negative (20), and a
// currency-warning caller under AllowWarnings is a success carrying the warning
// the result layer promotes to caveats (10). Guarded on actionlint, which the
// adapter spawns.
func TestWorkflowRunEndToEnd(t *testing.T) {
	if _, err := exec.LookPath(actionlintTool); err != nil {
		t.Skipf("%s not on PATH; skipping (not a defect in the adapter)", actionlintTool)
	}

	// A body running a fleet check inline: actionlint-clean, so the only
	// finding is fleet rule one's error.
	inlineRoot := t.TempDir()
	writeBody(t, inlineRoot, ".github/workflows/inline.yml",
		"name: inline\non: push\njobs:\n  build:\n    runs-on: ubuntu-24.04\n    steps:\n      - run: go test ./...\n        shell: bash\n")
	gitInitAdd(t, inlineRoot)
	res, err := Run(context.Background(), Target{Language: LanguageWorkflow, Check: CheckLint, Dir: inlineRoot}, Options{LogDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Run over inline body: %v", err)
	}
	if res.Status != clikit.StatusGateNegative {
		t.Errorf("inline-check run status = %q, want gate_negative (exit %d)", res.Status, ExitCheckFailed)
	}

	// A compliant caller on an older same-major release: a currency warning,
	// which under AllowWarnings is a success carrying the warning.
	now := time.Now().UTC()
	at := func(days int) string { return now.Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339) }
	releases := filepath.Join(t.TempDir(), "releases.json")
	if err := os.WriteFile(releases, []byte(`{"tags":[
	  {"name":"v3.1.0","commit_date":"`+at(1)+`","published_at":"`+at(1)+`"},
	  {"name":"v3.0.0","commit_date":"`+at(20)+`","published_at":"`+at(20)+`"}
	]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(releasesFileEnv, releases)

	callerRoot := t.TempDir()
	writeBody(t, callerRoot, ".github/workflows/caller.yml",
		"name: caller\non: push\njobs:\n  ci:\n    uses: johnrichter/claude-shared-tooling/.github/workflows/ci-go.yml@v3.0.0\n")
	gitInitAdd(t, callerRoot)
	res, err = Run(context.Background(), Target{Language: LanguageWorkflow, Check: CheckLint, Dir: callerRoot}, Options{LogDir: t.TempDir(), AllowWarnings: true})
	if err != nil {
		t.Fatalf("Run over caller body: %v", err)
	}
	if res.Status != clikit.StatusSuccess {
		t.Fatalf("currency-warning run status = %q, want success (the caveats/exit-10 precursor)", res.Status)
	}
	if res.Counts.Warnings != 1 {
		t.Errorf("currency-warning run warnings = %d, want 1", res.Counts.Warnings)
	}
}

// writeBody writes rel (a forward-slash path) under root with body.
func writeBody(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// gitInitAdd makes root a git repository and stages every file already written
// under it, so enumerateWorkflowBodies — which reads the git-tracked tree —
// sees them. It skips the test when git is unavailable rather than failing,
// since a missing git is an environment gap, not an adapter defect.
func gitInitAdd(t *testing.T, root string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH; the tracked-tree enumeration needs a git repository")
	}
	for _, args := range [][]string{{"init"}, {"add", "-A"}} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, root, err, out)
		}
	}
}
