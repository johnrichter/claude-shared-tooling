package toolchain

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
	"github.com/johnrichter/claude-shared-tooling/go/jsondoc"
	"github.com/johnrichter/claude-shared-tooling/go/sysops"
)

// ResolveCheck decides how the command surface treats a (language, check,
// test) request against the dispatch table. It returns the MatrixEntry and a
// nil diagnostic for a pair the Matrix declares and an adapter implements.
// For every other request — a language/check the Matrix does not cover, or a
// declared pair no adapter implements yet — it returns the zero MatrixEntry
// and the unsupported diagnostic (DiagUnsupportedCheck, EXIT 80). It never
// returns a success for a pair that would not actually run, so a caller fails
// closed rather than reporting a silent pass. It runs nothing and has no side
// effect; it reads the committed table only.
func ResolveCheck(language string, check Check, test TestKind) (MatrixEntry, *clikit.Diagnostic) {
	entry, ok := LookupPair(language, check, test)
	if !ok || !entry.Implemented {
		d := UnsupportedDiagnostic(language, check, test)
		return MatrixEntry{}, &d
	}
	return entry, nil
}

// UnsupportedDiagnostic builds the diagnostic a caller returns for a pair the
// dispatch contract does not run: code DiagUnsupportedCheck, whose class maps
// to EXIT 80 (ExitUnsupported). Its context names the language and check so a
// reader sees which pair was refused, and its triage is manual — no
// re-invocation makes an unimplemented or out-of-matrix pair run. It has no
// side effect.
func UnsupportedDiagnostic(language string, check Check, test TestKind) clikit.Diagnostic {
	pair := string(check)
	if check == CheckTest {
		pair = string(check) + " " + string(test)
	}
	d, err := clikit.NewError(
		DiagUnsupportedCheck,
		fmt.Sprintf("no adapter supports the %s %s check", language, pair),
		clikit.Manual("choose a language-and-check pair the toolchain matrix supports"),
		map[string]any{"language": language, "check": pair},
	)
	if err != nil {
		// NewError only rejects a malformed code, message, triage or context;
		// all four are fixed constants here, so a failure is a programming
		// error in this package rather than a runtime condition.
		panic(fmt.Sprintf("toolchain: building unsupported diagnostic: %v", err))
	}
	return d
}

// Run executes target's check through its language's registered Adapter and
// returns the normalized, capped RunResult. The adapter picks the route —
// a spawned tool or in-process analysis — and Run owns everything after it:
// the content-hash impact-skip cache when opts.CacheDir is set,
// classification of pass/fail per opts.AllowWarnings, the MaxDiagnostics cap
// on Diagnostics, and the uncapped detail written to opts.LogDir. All of
// that is identical on both routes; only how the diagnostics were obtained
// differs.
//
// A returned error means the check could not be run at all or its output
// could not be parsed — an infrastructure failure, never a code problem the
// tool itself reported (that is always a Diagnostic on a returned
// RunResult).
func Run(ctx context.Context, target Target, opts Options) (*RunResult, error) {
	if target.Dir == "" {
		return nil, fmt.Errorf("toolchain: target.Dir is required")
	}
	if opts.LogDir == "" {
		return nil, fmt.Errorf("toolchain: options.LogDir is required")
	}
	adapter, ok := lookup(target.Language)
	if !ok {
		return nil, fmt.Errorf("toolchain: no adapter registered for language %q", target.Language)
	}
	route := adapter.Route(target.Check)
	if !route.valid() {
		return nil, fmt.Errorf("toolchain: adapter for language %q reported unknown route %q for check %q",
			target.Language, route, target.Check)
	}
	// Tool answers on every route, so the name on the result, the log, and
	// every error message below comes from this one read.
	tool := adapter.Tool(target.Check)

	// Only a spawned check has an argv; the in-process route leaves Command
	// unread and the command field empty on both the result and the log.
	var argv []string
	if route == RouteSubprocess {
		var cmd []string
		var err error
		// An adapter that implements ConfigPathCommand gets target.ConfigPath
		// threaded into its argv; every other adapter keeps calling Command
		// exactly as before ConfigPath existed.
		if cpa, ok := adapter.(ConfigPathCommand); ok {
			cmd, err = cpa.CommandWithConfigPath(target.Check, target.ConfigPath)
		} else {
			cmd, err = adapter.Command(target.Check)
		}
		if err != nil {
			return nil, err
		}
		argv = append(cmd, target.Args...)
	}

	id, err := runID(target)
	if err != nil {
		return nil, err
	}

	var contentHash string
	if opts.CacheDir != "" {
		contentHash, err = ContentHash(target.Dir)
		if err != nil {
			return nil, err
		}
		if cached, hit, err := lookupCache(opts.CacheDir, id, contentHash, opts.AllowWarnings); err != nil {
			return nil, err
		} else if hit {
			cached.Impact = ImpactSkippedNoChange
			cached.DurationMS = 0
			return &cached, nil
		}
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	start := time.Now()
	var out outcome
	// Exhaustive: the route.valid() guard above rejected anything else.
	switch route {
	case RouteSubprocess:
		out, err = runSubprocess(ctx, adapter, target, tool, argv, timeout)
	case RouteInProcess:
		out, err = runInProcess(ctx, adapter, target, timeout)
	}
	duration := time.Since(start)
	if err != nil {
		return nil, err
	}

	counts := Counts{}
	for _, d := range out.diags {
		switch d.Severity {
		case SeverityError:
			counts.Errors++
		case SeverityWarning:
			counts.Warnings++
		}
	}

	capped := out.diags
	overflow := 0
	if len(out.diags) > MaxDiagnostics {
		overflow = len(out.diags) - MaxDiagnostics
		capped = out.diags[:MaxDiagnostics]
	}

	logRef, err := writeLog(opts.LogDir, id, logDetail{
		Tool:        tool,
		Command:     argv,
		Diagnostics: out.diags,
		Stdout:      string(out.stdout),
		Stderr:      string(out.stderr),
	})
	if err != nil {
		return nil, err
	}

	result := &RunResult{
		SchemaVersion: SchemaVersion,
		ID:            id,
		Tool:          tool,
		Language:      target.Language,
		Command:       argv,
		Status:        classifyStatus(out.exitCode, counts, opts.AllowWarnings),
		Counts:        counts,
		Diagnostics:   capped,
		Overflow:      overflow,
		LogRef:        logRef,
		Impact:        ImpactExecuted,
		DurationMS:    duration.Milliseconds(),
	}

	if opts.CacheDir != "" {
		if err := saveCache(opts.CacheDir, id, contentHash, opts.AllowWarnings, *result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// outcome is what one route produced, and the only thing Run's shared tail —
// counting, capping, classification, logging, caching — reads. The subprocess
// route fills exitCode from the one tool it spawned; the in-process route
// leaves it zero, since no single tool among the several an adapter may spawn
// owns the verdict there (the diagnostics do). Both routes carry the raw
// output the tail hands writeLog: the subprocess route the one tool's streams,
// the in-process route the concatenation of every tool it spawned (captured
// through runTool, see inProcessCapture).
type outcome struct {
	exitCode int
	diags    []Diagnostic
	stdout   []byte
	stderr   []byte
}

// runSubprocess spawns tool with argv in target's directory and hands what
// it wrote to the adapter's Parse. timeout bounds the child process.
func runSubprocess(ctx context.Context, adapter Adapter, target Target, tool string, argv []string, timeout time.Duration) (outcome, error) {
	execRes, err := sysops.Run(ctx, tool, argv, sysops.Options{Dir: target.Dir, Timeout: timeout})
	if err != nil {
		return outcome{}, fmt.Errorf("toolchain: run %s %v in %s: %w", tool, argv, target.Dir, err)
	}
	diags, err := adapter.Parse(execRes.ExitCode, execRes.Stdout, execRes.Stderr)
	if err != nil {
		return outcome{}, fmt.Errorf("toolchain: parse %s output: %w", tool, err)
	}
	return outcome{
		exitCode: execRes.ExitCode,
		diags:    diags,
		stdout:   execRes.Stdout,
		stderr:   execRes.Stderr,
	}, nil
}

// runInProcess hands the target to the adapter's own analysis, bounded by the
// same timeout a spawned tool gets. The adapter still spawns tools — through
// runTool, one or several — it just parses their output itself rather than
// leaving that to Parse. An inProcessCapture rides the context so runTool
// records each tool's raw streams as it goes, and the outcome carries their
// concatenation: without it the log of a failed in-process check held an empty
// stdout/stderr, the one record a reader needs when the adapter turned the
// failure into no diagnostic Parse could. There is still no single exit status
// for classifyStatus to weigh, so exitCode stays zero; the diagnostics carry
// the verdict.
func runInProcess(ctx context.Context, adapter Adapter, target Target, timeout time.Duration) (outcome, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	capture := &inProcessCapture{}
	diags, err := adapter.RunInProcess(withCapture(ctx, capture), target)
	if err != nil {
		return outcome{}, fmt.Errorf("toolchain: in-process %s check in %s: %w", target.Check, target.Dir, err)
	}
	stdout, stderr := capture.streams()
	return outcome{diags: diags, stdout: stdout, stderr: stderr}, nil
}

// inProcessCapture accumulates the raw output of every tool an in-process
// check spawns through runTool. The subprocess route hands writeLog its one
// tool's stdout and stderr verbatim; the in-process route spawns its tools
// itself and used to drop that output, so a failed multi-tool check — every
// gate_negative the cargo, python, shell and workflow adapters raise — wrote
// an empty stdout/stderr to its log, exactly the record needed when a failure
// produced no diagnostic. Run installs one on the context (withCapture) before
// calling RunInProcess; runTool records into it when present, so an adapter
// needs no change and a direct RunInProcess caller that installs none records
// nothing. Because the log's command field stays empty on this route (Run
// never reads Command here), each recorded block is headed by the argv that
// produced it — the only place the in-process invocation is named.
type inProcessCapture struct {
	mu     sync.Mutex
	stdout bytes.Buffer
	stderr bytes.Buffer
}

// captureKey is the unexported context key inProcessCapture rides under, so no
// other package can collide with or read it.
type captureKey struct{}

// withCapture returns ctx carrying c, so a runTool call inside the adapter's
// RunInProcess records into it.
func withCapture(ctx context.Context, c *inProcessCapture) context.Context {
	return context.WithValue(ctx, captureKey{}, c)
}

// captureFrom returns the capture installed on ctx, or nil when none is — the
// case for a direct RunInProcess caller that never went through Run.
func captureFrom(ctx context.Context) *inProcessCapture {
	c, _ := ctx.Value(captureKey{}).(*inProcessCapture)
	return c
}

// record appends one tool invocation's raw streams, each headed by the argv
// that produced it so a log carrying several tools' output stays attributable
// to the tool that wrote each block.
func (c *inProcessCapture) record(tool string, args []string, res *sysops.Result) {
	header := "$ " + strings.TrimSpace(tool+" "+strings.Join(args, " ")) + "\n"
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stdout.WriteString(header)
	c.stdout.Write(res.Stdout)
	c.stderr.WriteString(header)
	c.stderr.Write(res.Stderr)
}

// streams returns the accumulated stdout and stderr, or nil for a stream no
// tool wrote to, so an in-process check that spawned nothing keeps the empty
// log the zero outcome would have written.
func (c *inProcessCapture) streams() (stdout, stderr []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stdout.Len() > 0 {
		stdout = c.stdout.Bytes()
	}
	if c.stderr.Len() > 0 {
		stderr = c.stderr.Bytes()
	}
	return stdout, stderr
}

// classifyStatus derives a RunResult's clikit.Status from what the tool
// reported: any error, or (per the must-fail-on-warning default) any
// warning, is clikit.StatusGateNegative — an expected negative, the check
// ran and the answer is "no, this does not pass". A clean run, or a
// warning-only run with opts.AllowWarnings, is clikit.StatusSuccess. A
// non-zero exit with zero parsed diagnostics still fails: Parse guarantees
// at least one synthetic Diagnostic in that case, so counts.Errors is never
// zero when exitCode isn't.
func classifyStatus(exitCode int, counts Counts, allowWarnings bool) clikit.Status {
	if counts.Errors > 0 {
		return clikit.StatusGateNegative
	}
	if counts.Warnings > 0 && !allowWarnings {
		return clikit.StatusGateNegative
	}
	if exitCode != 0 {
		return clikit.StatusGateNegative
	}
	return clikit.StatusSuccess
}

// runID derives RunResult.ID deterministically from what identifies a
// target: its language, check, test kind, directory, args, and — when set —
// config path. The same target always produces the same ID, which doubles as
// the cache key (cache.go) and the log file's base name (log.go) — a re-run
// of the identical target overwrites its own prior log rather than
// accumulating one file per run. Two targets that differ only in Args (e.g. a
// release build vs. a debug build of the same dir), only in ConfigPath, or
// only in Test (e.g. the unit and e2e pairs of the same CheckTest target),
// hash to distinct IDs, so they get distinct cache entries and log files
// rather than colliding on one.
func runID(target Target) (string, error) {
	key := map[string]any{
		"language": target.Language,
		"check":    string(target.Check),
		"test":     string(target.Test),
		"dir":      target.Dir,
		"args":     target.Args,
	}
	// ConfigPath only enters the key when set, so a target that leaves it
	// unset hashes to the exact ID a pre-ConfigPath Target would have — a
	// caller upgrading without setting it keeps every cache entry and log
	// file it already has.
	if target.ConfigPath != "" {
		key["config_path"] = target.ConfigPath
	}
	hash, err := jsondoc.ContentHash(key)
	if err != nil {
		return "", fmt.Errorf("toolchain: derive run id: %w", err)
	}
	return hash[:16], nil
}
