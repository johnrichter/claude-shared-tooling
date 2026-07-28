package characterize

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/johnrichter/claude-shared-tooling/go/sysops"
	"github.com/johnrichter/claude-shared-tooling/go/transcript"
)

// DefaultGeneratorTool is Options.Tool's fallback when a caller leaves it empty, so a manifest
// this package builds always carries a non-empty generator.tool.
const DefaultGeneratorTool = "ai-shared-lib/characterize"

// Options configures one characterizing run.
type Options struct {
	// Plugin identifies the plugin being characterized. Plugin.Path is the repo-qualified prefix
	// every citation in the resulting manifest is checked against; Plugin.Name seeds this run's
	// minted ids.
	Plugin PluginIdentity
	// PluginDir is the filesystem directory Plugin.Path resolves to. Every citation the probe
	// returns is checked against a real file under this directory.
	PluginDir string

	// Model is the caller's requested tier -- never trusted as-is: ResolveModel's ambient pin,
	// when set, overrides it before the probe runs.
	Model string
	// MaxBudgetUSD is this run's per-run cost ceiling. Must be > 0; a metered run declaring no
	// ceiling is rejected before any probe runs.
	MaxBudgetUSD float64

	// Run is the subprocess seam the probe invokes through. Nil selects SysopsProbeRunner.
	Run ProbeRunner
	// ProbeOptions configures the probe subprocess. Dir defaults to PluginDir when left empty --
	// the probe reads only inside the plugin it is characterizing.
	ProbeOptions sysops.Options

	// Transcripts, when set, lets this run corroborate its resolved model against the session
	// transcript the probe actually produced. Nil skips that corroboration entirely; it is never
	// required for a manifest to be built.
	Transcripts     transcript.TranscriptSource
	TranscriptsRoot string
	TranscriptScope string

	// Tool and ToolVersion name this run's generator. Tool defaults to DefaultGeneratorTool.
	Tool        string
	ToolVersion string
}

// Run drives one metered characterizing read of Options.Plugin and returns its capability
// manifest. The manifest is returned even when the run's own cost ceiling was exceeded -- the
// spend already happened, and the caller needs the record it bought -- but err is then a
// *BudgetCeilingExceededError, so a caller never mistakes an over-budget run for a clean one.
func Run(ctx context.Context, opts Options) (*Manifest, error) {
	if strings.TrimSpace(opts.Plugin.Name) == "" {
		return nil, fmt.Errorf("characterize: Options.Plugin.Name is required")
	}
	if strings.TrimSpace(opts.Plugin.Path) == "" {
		return nil, fmt.Errorf("characterize: Options.Plugin.Path is required")
	}
	if strings.TrimSpace(opts.PluginDir) == "" {
		return nil, fmt.Errorf("characterize: Options.PluginDir is required")
	}
	if err := checkBudget(opts.MaxBudgetUSD, 0); err != nil {
		return nil, err
	}

	model := ResolveModel(opts.Model)
	runner := opts.Run
	if runner == nil {
		runner = SysopsProbeRunner
	}
	probeOpts := opts.ProbeOptions
	if probeOpts.Dir == "" {
		probeOpts.Dir = opts.PluginDir
	}

	prompt := buildPrompt(opts.Plugin.Name, opts.Plugin.Path)
	result, raw, err := runProbe(ctx, runner, prompt, model, opts.MaxBudgetUSD, probeOpts)
	if err != nil {
		return nil, err
	}

	cand, err := parseCandidate(result.Result)
	if err != nil {
		const rawPreviewLen = 2000
		preview := string(raw)
		if len(preview) > rawPreviewLen {
			preview = preview[:rawPreviewLen] + "...(truncated)"
		}
		return nil, fmt.Errorf("characterize: probe's reply did not parse as a candidate manifest: %w\nraw probe output:\n%s", err, preview)
	}

	ids := newIDMinter(slugify(opts.Plugin.Name))
	surfaces, gaps := buildManifest(ids, opts.Plugin.Path, opts.PluginDir, cand)

	if check := verifyModelHonored(opts.Transcripts, opts.TranscriptsRoot, opts.TranscriptScope, result.SessionID, model); check.attempted && !check.honored {
		gaps = append(gaps, Gap{ID: ids.next("gap"), Area: "model-pin honored", Reason: check.reason})
	}

	tool := opts.Tool
	if tool == "" {
		tool = DefaultGeneratorTool
	}

	manifest := &Manifest{
		Schema:            SchemaVersion,
		Plugin:            opts.Plugin,
		GeneratedAt:       time.Now().UTC().Format(time.RFC3339),
		Generator:         &Generator{Tool: tool, Version: opts.ToolVersion, Model: model},
		Surfaces:          surfaces,
		CouldNotDetermine: gaps,
		Coverage:          Coverage{ManifestCaseIDs: []string{}, ExecutedCaseIDs: []string{}},
	}

	return manifest, checkBudget(opts.MaxBudgetUSD, result.TotalCostUSD)
}
