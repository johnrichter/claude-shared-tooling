package toolchain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func init() {
	Register(workflowAdapter{})
}

// actionlintTool is the one binary the workflow track spawns, named once so
// its argv, its diagnostic-message prefix and workflowPair's tool list all
// read the same string.
const actionlintTool = "actionlint"

// templateRepo is the GitHub owner/repo of the reusable-workflow templates
// this fleet releases (the module github.com/johnrichter/claude-shared-tooling
// publishes them from ai-shared-lib/.github/workflows/). Fleet rule one reads
// it to tell a template `uses:` line from an official-action one, and fleet
// rule two reads it to compare a caller's ref against this repository's tags.
const templateRepo = "johnrichter/claude-shared-tooling"

// workflowAdapter is the Adapter for the workflow track — WORKFLOW-PAIR's
// single `workflow lint` check (OD71). Unlike a language adapter it fronts no
// build system and no manifest: its target is a repository root, and it
// discovers every tracked workflow body at or below it (enumerateWorkflowBodies)
// by the two file shapes section 2 of the design names. The check routes
// in-process because it is a composite no single spawned tool performs: it runs
// actionlint over an explicitly enumerated path list (WFRULES clause (a)) and
// then applies two fleet rules the tool has no equivalent for — inheritance
// (fleet rule one, this file) and template-version currency (fleet rule two,
// workflow_currency.go). OD71's ceiling is exactly those two rules: the adapter
// encodes no third fleet convention, CI shape or process rule, and it reads no
// config of its own, which is why SC37 records actionlint as taking no config
// row.
//
// A silent verdict — a non-subject body, or a currency lookup that did not
// resolve — produces no Diagnostic, so the run stays green and no notice fails
// a step (WFRULES clause (d)). The reader-facing notice each silent verdict
// names is carried on the verdict value the evaluation returns, not on the
// RunResult; the `ci-workflow.yml` currency step and the binary's result layer
// (separate SC39 tasks) surface it. Run itself sees only the failing and
// warning Diagnostics this adapter returns.
type workflowAdapter struct{}

func (workflowAdapter) Language() string { return LanguageWorkflow }

// Route reports the in-process route for the one check the track carries. Any
// other check falls through to the same route and is refused in RunInProcess,
// exactly as ResolveCheck would refuse it before Run ever calls an adapter.
func (workflowAdapter) Route(Check) Route { return RouteInProcess }

// Tool names actionlint for the lint check — the one binary the composite
// spawns; the two fleet rules run in-process and add no binary, so the label
// names the tool a reader sees in the run log. Any other check answers the
// bare track name, though ResolveCheck refuses it before this is consulted.
func (workflowAdapter) Tool(check Check) string {
	if check == CheckLint {
		return actionlintTool
	}
	return LanguageWorkflow
}

// Command always answers ErrUnsupportedCheck: the one check routes in-process,
// so Run's subprocess path never calls this. It exists only to satisfy the
// Adapter interface, exactly as shellAdapter's Command does.
func (a workflowAdapter) Command(check Check) ([]string, error) {
	return nil, errUnsupportedCheck(a.Tool(check), check)
}

// Parse is never reached: the one check routes in-process, so Run's subprocess
// path never calls it. It exists only to satisfy the Adapter interface.
func (workflowAdapter) Parse(int, []byte, []byte) ([]Diagnostic, error) {
	return nil, nil
}

// RunInProcess performs the workflow lint over target.Dir: it enumerates every
// tracked workflow body, runs actionlint over the explicit path list, then
// applies the two fleet rules body by body. Only CheckLint is served; any
// other check is refused (fails closed at EXIT 80 the way ResolveCheck would).
// A returned error is an infrastructure failure — the tree could not be walked,
// actionlint could not be started, or a set-but-broken LANGUAGE_TOOLS_RELEASES_FILE
// could not be read — never a finding, which is always a Diagnostic.
func (a workflowAdapter) RunInProcess(ctx context.Context, target Target) ([]Diagnostic, error) {
	if target.Check != CheckLint {
		return nil, errUnsupportedCheck(a.Tool(target.Check), target.Check)
	}

	bodies, err := enumerateWorkflowBodies(ctx, target.Dir)
	if err != nil {
		return nil, err
	}
	if len(bodies) == 0 {
		return nil, nil // nothing tracked to lint — a trivial pass
	}

	var diags []Diagnostic

	// WFRULES clause (a): actionlint on its own default rule set, over the
	// enumerated paths passed explicitly.
	actionlintDiags, err := a.runActionlint(ctx, target.Dir, bodies)
	if err != nil {
		return nil, err
	}
	diags = append(diags, actionlintDiags...)

	// The two fleet rules read each body's parsed structure. Resolve the
	// release catalog once for every currency evaluation; a classified lookup
	// failure carries a reason forward so each verdict names it rather than
	// failing the run.
	catalog, lookupReason, err := resolveReleaseSource().catalog(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()

	for _, path := range bodies {
		rel, err := filepath.Rel(target.Dir, path)
		if err != nil {
			return nil, fmt.Errorf("toolchain: relativize %s to %s: %w", path, target.Dir, err)
		}
		rel = filepath.ToSlash(rel)

		doc, err := parseWorkflowBody(path)
		if err != nil {
			return nil, err
		}

		// Fleet rule one, inheritance. A failing subject yields an error
		// Diagnostic; a non-subject yields the silent verdict at reason
		// not_a_check_subject, which carries no Diagnostic and leaves the run
		// green (the notice layer surfaces the reason).
		if d, emit, _ := evaluateInheritance(rel, doc); emit {
			diags = append(diags, d)
		}

		// Fleet rule two, template-version currency: one verdict per template
		// `uses:` line. Only a warn or error verdict yields a Diagnostic; a
		// current or silent verdict does not (WFRULES clauses (c), (d)).
		for _, ref := range doc.templateRefs {
			if d, ok := evaluateCurrency(ref, catalog, lookupReason, now).diagnostic(rel); ok {
				diags = append(diags, d)
			}
		}
	}

	return diags, nil
}

// censusExcludedPrefixes and censusExcludedSubstring reproduce census.py's
// tracked() exclusions on top of git ls-files. A linked worktree and a project
// directory are separate trees this repository does not lint as its own, and a
// fixture directory holds bodies that exist to be scanned rather than fleet
// artifacts. Reproducing them keeps the adapter's population byte-for-byte
// identical to the instrument F82 measures, so criterion 2's "same 32 paths"
// counter-probe holds.
var censusExcludedPrefixes = []string{".claude/worktrees/", ".dat/"}

const censusExcludedSubstring = "/testdata/"

// enumerateWorkflowBodies returns every tracked workflow body at or below root
// — the repository root a workflow call names — as an absolute, sorted path
// list, so actionlint runs over a deterministic order. The population is the
// git-tracked tree exactly as census.py's tracked() reads it (F82): git
// ls-files, less the paths under a linked worktree, a project directory, or a
// fixture tree. Reading the tracked tree rather than walking the filesystem is
// what keeps an untracked or gitignored body, and a body inside a linked
// worktree, out of the set.
//
// WFRULES clause (a) fixes the selector at the two file shapes section 2 names:
// any .yml or .yaml under a workflow directory, and any file whose name ends
// .github-workflow.yml anywhere, the shape a plugin ships a workflow body into
// a consumer repository with. Passing that second shape to actionlint
// explicitly is the whole reason the adapter enumerates rather than letting
// actionlint search: a bare run scans the nearest workflow directory alone and
// never sees a plugin-shipped body.
func enumerateWorkflowBodies(ctx context.Context, root string) ([]string, error) {
	tracked, err := trackedFiles(ctx, root)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, rel := range tracked {
		if isWorkflowBody(rel, filepathBase(rel)) {
			paths = append(paths, filepath.Join(root, filepath.FromSlash(rel)))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

// trackedFiles lists every git-tracked path under root — relative to root and
// forward-slashed, as git already emits — less census.py's tracked()
// exclusions. A git that cannot run, or a root that is not a git repository, is
// an infrastructure failure rather than an empty pass: the workflow track's
// target is a repository root by section 2, so a non-repository target is a
// usage error, not a tree with nothing to lint, and returning it silently green
// would mask exactly the unchecked-workflow gap SC39 closes.
func trackedFiles(ctx context.Context, root string) ([]string, error) {
	res, err := runTool(ctx, root, "git", []string{"ls-files", "-z"})
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("toolchain: git ls-files in %s exited %d: %s",
			root, res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	var out []string
	for _, rel := range strings.Split(string(res.Stdout), "\x00") {
		if rel == "" || excludedFromTracked(rel) {
			continue
		}
		out = append(out, rel)
	}
	return out, nil
}

// excludedFromTracked reports whether a git-tracked path sits in one of the
// trees census.py's tracked() drops.
func excludedFromTracked(rel string) bool {
	for _, p := range censusExcludedPrefixes {
		if strings.HasPrefix(rel, p) {
			return true
		}
	}
	return strings.Contains(rel, censusExcludedSubstring)
}

// isWorkflowBody reports whether the tracked file at rel (a repo-root-relative,
// forward-slash path) with base name name is one of WFRULES clause (a)'s two
// shapes: a .yml or .yaml under the workflow directory at any depth, or a
// *.github-workflow.yml anywhere. The two-shape selector matches census.py's
// own, so the adapter and the instrument F82 names classify a tracked path the
// same way.
func isWorkflowBody(rel, name string) bool {
	inWorkflowDir := strings.HasPrefix(rel, ".github/workflows/") &&
		(strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml"))
	shipped := strings.HasSuffix(name, ".github-workflow.yml")
	return inWorkflowDir || shipped
}

// runActionlint spawns actionlint over the explicit path list (paths made
// relative to dir, the process cwd) with its own default rule set and its JSON
// output format, then turns each finding into an error Diagnostic. actionlint
// exits 0 on no findings and non-zero when it reports any; a non-zero exit with
// nothing parsed falls back to one synthetic diagnostic so a finding actionlint
// reported can never read as a clean run.
func (workflowAdapter) runActionlint(ctx context.Context, dir string, bodies []string) ([]Diagnostic, error) {
	rel, err := relativeTo(dir, bodies)
	if err != nil {
		return nil, err
	}
	args := append([]string{"-format", "{{json .}}", "-no-color"}, rel...)
	res, err := runTool(ctx, dir, actionlintTool, args)
	if err != nil {
		return nil, err
	}
	diags := parseActionlintJSON(res.Stdout)
	if len(diags) == 0 && res.ExitCode != 0 {
		diags = append(diags, fallbackDiagnostic(actionlintTool, res.ExitCode))
	}
	return diags, nil
}

// actionlintFinding is the subset of actionlint's `{{json .}}` output shape
// this adapter reads: one object per finding, in a JSON array.
type actionlintFinding struct {
	Message  string `json:"message"`
	Filepath string `json:"filepath"`
	Line     int    `json:"line"`
	Kind     string `json:"kind"`
}

// parseActionlintJSON turns actionlint's JSON array into one error Diagnostic
// per finding, tagged with the tool and the finding's rule kind so it reads
// distinctly from a fleet-rule finding in the same run.
func parseActionlintJSON(stdout []byte) []Diagnostic {
	trimmed := strings.TrimSpace(string(stdout))
	if trimmed == "" {
		return nil
	}
	var findings []actionlintFinding
	if err := json.Unmarshal([]byte(trimmed), &findings); err != nil {
		return nil // the caller's exit-code fallback covers an unparseable report
	}
	diags := make([]Diagnostic, 0, len(findings))
	for _, f := range findings {
		diags = append(diags, Diagnostic{
			Severity: SeverityError,
			Code:     f.Kind,
			Message:  fmt.Sprintf("%s: %s", actionlintTool, f.Message),
			File:     f.Filepath,
			Line:     f.Line,
		})
	}
	return diags
}

// workflowFile is one workflow body parsed just far enough for the two fleet
// rules to read it: whether it declares a job, whether any run: step invokes a
// banned command, and every template `uses:` line it holds. E3 requires the
// YAML parse — not a line pattern — to decide what is a run: step, so the model
// carries the parsed jobs and steps rather than raw text.
type workflowFile struct {
	hasJob       bool
	bannedMatch  string        // the first banned command a run: step matched, "" if none
	templateRefs []templateRef // every template uses: line, in document order
}

// yamlWorkflow is the raw YAML shape parseWorkflowBody decodes. A reusable
// workflow is called through a job-level `uses:`; an action is a step-level
// `uses:`; a shell body is a step-level `run:`. All three are needed: the
// job-level uses feeds both fleet rules, the run body feeds rule one's
// banned-command test, and the step-level uses lets an official-action line be
// recognized and left outside rule two.
type yamlWorkflow struct {
	Jobs map[string]struct {
		Uses  string `yaml:"uses"`
		Steps []struct {
			Run  string `yaml:"run"`
			Uses string `yaml:"uses"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

// parseWorkflowBody reads and parses one body into a workflowFile. A body
// actionlint itself cannot parse (already a finding on the actionlint pass)
// yields the zero workflowFile — no job, no banned command, no template ref —
// so it classifies as a non-subject rather than crashing a rule; actionlint
// owns the parse-error report. A read error is an infrastructure failure.
func parseWorkflowBody(path string) (workflowFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return workflowFile{}, fmt.Errorf("toolchain: read workflow body %s: %w", path, err)
	}
	var doc yamlWorkflow
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return workflowFile{}, nil
	}

	wf := workflowFile{hasJob: len(doc.Jobs) > 0}
	// A map iteration order is not stable, so collect template refs sorted by
	// their raw reference for a deterministic diagnostic order.
	for _, job := range doc.Jobs {
		if ref, ok := parseTemplateRef(job.Uses); ok {
			wf.templateRefs = append(wf.templateRefs, ref)
		}
		for _, step := range job.Steps {
			if wf.bannedMatch == "" {
				if m := bannedCommandMatch(step.Run); m != "" {
					wf.bannedMatch = m
				}
			}
			if ref, ok := parseTemplateRef(step.Uses); ok {
				wf.templateRefs = append(wf.templateRefs, ref)
			}
		}
	}
	sort.Slice(wf.templateRefs, func(i, j int) bool {
		return wf.templateRefs[i].raw < wf.templateRefs[j].raw
	})
	return wf, nil
}

// bannedCommandSet is census.py:BANNED_COMMANDS, at fifteen members: F37's
// fourteen language-tool patterns plus actionlint, which SC19 adds because the
// workflow track now owns that binary exactly as the shell track owns
// shellcheck. It is deliberately not census.py:CHECK_PATTERNS, the wider
// twenty-seven-pattern scan population that also matches commands a caller or a
// template runs by design (language-tools, mise exec, git tag and the rest); a
// caller step matching none of these fifteen is an addition the rule permits.
var bannedCommandSet = compilePatterns(map[string]string{
	"go build":      `\bgo build\b`,
	"go test":       `\bgo test\b`,
	"go vet":        `\bgo vet\b`,
	"gofmt":         `\bgofmt\b`,
	"golangci-lint": `\bgolangci-lint\b`,
	"cargo build":   `\bcargo build\b`,
	"cargo test":    `\bcargo test\b`,
	"cargo clippy":  `\bcargo clippy\b`,
	"cargo fmt":     `\bcargo fmt\b`,
	"cargo check":   `\bcargo check\b`,
	"pytest":        `\bpytest\b`,
	"ruff":          `\bruff\b`,
	"mypy":          `\bmypy\b`,
	"unittest":      `\bpython3?\s+-m\s+unittest\b`,
	"actionlint":    `\bactionlint\b`,
})

// compiledPattern pairs a banned command's name with its compiled matcher, so
// a match can name which command it caught.
type compiledPattern struct {
	name string
	re   *regexp.Regexp
}

// compilePatterns compiles the banned-command map into a slice ordered by name,
// so a scan is deterministic and each match names its command.
func compilePatterns(patterns map[string]string) []compiledPattern {
	names := make([]string, 0, len(patterns))
	for name := range patterns {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]compiledPattern, 0, len(names))
	for _, name := range names {
		out = append(out, compiledPattern{name: name, re: regexp.MustCompile(patterns[name])})
	}
	return out
}

// bannedCommandMatch returns the name of the first banned command any
// non-comment line of a run: step's body invokes, or "" if none does. It scans
// the run body line by line, skipping blank and comment lines, exactly as
// census.py:scan_run_lines does — the YAML parse (parseWorkflowBody) already
// decided this text is a run: step, so this only matches the command inside it
// and never decides step-ness from a line pattern (E3).
func bannedCommandMatch(run string) string {
	if run == "" {
		return ""
	}
	for _, line := range strings.Split(run, "\n") {
		stripped := strings.TrimSpace(line)
		if stripped == "" || strings.HasPrefix(stripped, "#") {
			continue
		}
		for _, p := range bannedCommandSet {
			if p.re.MatchString(stripped) {
				return p.name
			}
		}
	}
	return ""
}

// excludedBasenames is the set of workflow bodies DD6 exempts from fleet rule
// one by name: the templates this fleet publishes and the prior fleet's
// self-test. actionlint still lints them (F82 covers all thirty-two bodies), so
// the exemption is from the inheritance rule alone — a template body is not a
// caller and has no template to inherit, and treating it as a subject would
// misreport the template repository against its own rule. A consumer repository
// holds none of these names, so the exemption never reaches a real caller.
var excludedBasenames = map[string]bool{
	"ci-go.yml":                     true,
	"ci-rust.yml":                   true,
	"ci-python.yml":                 true,
	"ci-shell.yml":                  true,
	"ci-workflow.yml":               true,
	"release-cli.yml":               true,
	"release-library.yml":           true,
	"release-template-selftest.yml": true,
}

// evaluateInheritance applies fleet rule one to one body. It returns the error
// Diagnostic a failing subject earns with emit=true; for a body that passes, is
// DD6-exempt, or is a non-subject it returns emit=false, and for a non-subject
// it also names the silent reason not_a_check_subject so the notice layer can
// surface it. WFRULES clause (b): a body is a subject when it declares a job
// and holds a run: step matching the banned set, or when it holds any template
// `uses:` line. A subject passes when it holds at least one compliant template
// reference — a bare version tag, or a same-repository path ref per DD8 — and
// no banned run: line. A non-subject names the rule's subject rather than takes
// an exclusion under S4, so no body carrying a fleet check escapes the rule.
func evaluateInheritance(rel string, wf workflowFile) (diag Diagnostic, emit bool, silent noticeReason) {
	if excludedBasenames[filepathBase(rel)] {
		return Diagnostic{}, false, ""
	}

	hasBanned := wf.bannedMatch != ""
	isSubject := (wf.hasJob && hasBanned) || len(wf.templateRefs) > 0
	if !isSubject {
		// Silent verdict, reason not_a_check_subject: no failing Diagnostic, so
		// the run stays green. F85 enumerates these bodies.
		return Diagnostic{}, false, reasonNotACheckSubject
	}

	hasCompliant := false
	for _, ref := range wf.templateRefs {
		if ref.isPath || ref.isBareVersionTag() {
			hasCompliant = true
			break
		}
	}
	if hasCompliant && !hasBanned {
		return Diagnostic{}, false, "" // subject passes
	}

	var reason string
	switch {
	case hasBanned:
		reason = fmt.Sprintf("runs %q inline instead of inheriting a %s template", wf.bannedMatch, templateRepo)
	case len(wf.templateRefs) > 0:
		reason = fmt.Sprintf("references a %s template at a branch or commit ref rather than a bare version tag", templateRepo)
	default:
		reason = fmt.Sprintf("declares no %s template uses: line", templateRepo)
	}
	return Diagnostic{
		Severity: SeverityError,
		Code:     "inheritance",
		Message:  fmt.Sprintf("%s inheritance: %s", actionlintTool, reason),
		File:     rel,
	}, true, ""
}

// filepathBase returns the base name of a forward-slash path, without importing
// path/filepath's OS-separator behavior for a path already normalized to
// slashes by the caller.
func filepathBase(rel string) string {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[i+1:]
	}
	return rel
}
