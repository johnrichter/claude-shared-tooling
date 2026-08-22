package toolchain

import (
	"context"
	"fmt"
	"time"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
	"github.com/johnrichter/claude-shared-tooling/go/jsondoc"
	"github.com/johnrichter/claude-shared-tooling/go/sysops"
)

// Run executes target's check through its language's registered Adapter and
// returns the normalized, capped RunResult. It is the one execution path
// every adapter shares: Run spawns the tool (sysops.Run), applies the
// content-hash impact-skip cache when opts.CacheDir is set, classifies
// pass/fail per opts.AllowWarnings, caps Diagnostics at MaxDiagnostics, and
// writes the uncapped detail to opts.LogDir.
//
// A returned error means the tool could not be run at all or its output
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
	argv, err := adapter.Command(target.Check)
	if err != nil {
		return nil, err
	}
	argv = append(argv, target.Args...)
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
	execRes, err := sysops.Run(ctx, adapter.Tool(target.Check), argv, sysops.Options{Dir: target.Dir, Timeout: timeout})
	duration := time.Since(start)
	if err != nil {
		return nil, fmt.Errorf("toolchain: run %s %v in %s: %w", adapter.Tool(target.Check), argv, target.Dir, err)
	}

	diags, err := adapter.Parse(execRes.ExitCode, execRes.Stdout, execRes.Stderr)
	if err != nil {
		return nil, fmt.Errorf("toolchain: parse %s output: %w", adapter.Tool(target.Check), err)
	}

	counts := Counts{}
	for _, d := range diags {
		switch d.Severity {
		case SeverityError:
			counts.Errors++
		case SeverityWarning:
			counts.Warnings++
		}
	}

	capped := diags
	overflow := 0
	if len(diags) > MaxDiagnostics {
		overflow = len(diags) - MaxDiagnostics
		capped = diags[:MaxDiagnostics]
	}

	logRef, err := writeLog(opts.LogDir, id, logDetail{
		Tool:        adapter.Tool(target.Check),
		Command:     argv,
		Diagnostics: diags,
		Stdout:      string(execRes.Stdout),
		Stderr:      string(execRes.Stderr),
	})
	if err != nil {
		return nil, err
	}

	result := &RunResult{
		SchemaVersion: SchemaVersion,
		ID:            id,
		Tool:          adapter.Tool(target.Check),
		Language:      target.Language,
		Command:       argv,
		Status:        classifyStatus(execRes.ExitCode, counts, opts.AllowWarnings),
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
// target: its language, check, directory, and args. The same target always
// produces the same ID, which doubles as the cache key (cache.go) and the
// log file's base name (log.go) — a re-run of the identical target
// overwrites its own prior log rather than accumulating one file per run.
// Two targets that differ only in Args (e.g. a release build vs. a debug
// build of the same dir) hash to distinct IDs, so they get distinct cache
// entries and log files rather than colliding on one.
func runID(target Target) (string, error) {
	key := map[string]any{
		"language": target.Language,
		"check":    string(target.Check),
		"dir":      target.Dir,
		"args":     target.Args,
	}
	hash, err := jsondoc.ContentHash(key)
	if err != nil {
		return "", fmt.Errorf("toolchain: derive run id: %w", err)
	}
	return hash[:16], nil
}
