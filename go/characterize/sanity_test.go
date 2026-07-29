package characterize

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/sysops"
	"github.com/johnrichter/claude-shared-tooling/go/transcript"
)

// fixturePluginDir is the frozen fixture plugin every sanity test characterizes against: a real,
// minimal Claude Code plugin under testdata, so a citation this package resolves is a citation
// into an actual file, not a name it merely trusts.
const fixturePluginDir = "testdata/fixture-plugin"

// candidateReplyFixture is the JSON a characterizing probe would reply with for fixturePluginDir:
// one real, verifiable surface (the /hello command, with a weak spot on the same file), one
// candidate surface whose citation names a file the fixture does not ship (simulating an agent
// claim this package cannot verify), and one genuine could-not-determine gap from the model
// itself.
const candidateReplyFixture = `{
  "surfaces": [
    {
      "type": "command",
      "name": "hello",
      "trigger": "Invoked via the /hello slash command, per commands/hello.md's own description frontmatter.",
      "citation": {"path": "fixture-repo/fixture-plugin/commands/hello.md", "lines": [1, 3], "excerpt": "description: Say a short greeting back to the user by name."},
      "weak_spots": [
        {
          "description": "The greeting has no argument parsing, so it cannot actually address the user by name despite the description promising that.",
          "basis": "The command body (lines 5-6) prints a fixed one-line greeting with no placeholder or argument reference.",
          "citation": {"path": "fixture-repo/fixture-plugin/commands/hello.md", "lines": [5, 6]},
          "severity": "low"
        }
      ]
    },
    {
      "type": "mcp-server",
      "name": "obscure-server",
      "trigger": "Presumed to expose tools automatically once configured, per typical MCP server behavior.",
      "citation": {"path": "fixture-repo/fixture-plugin/mcp/obscure.json", "lines": [1, 5]}
    }
  ],
  "could_not_determine": [
    {
      "area": "statusline surface presence",
      "reason": "The fixture ships no statusline configuration file of any kind, so nothing can be characterized about a statusline surface either way.",
      "attempted_citation": {"path": "fixture-repo/fixture-plugin/.claude-plugin/plugin.json"}
    }
  ]
}`

// fakeProbe returns a ProbeRunner that ignores the plugin's actual files and always replies with
// reply, reporting sessionID and costUSD as the run's own structured cost accounting -- exactly
// what `claude -p --output-format json` would emit, without spending anything real. args, if
// non-nil, records the args passed on the most recent call so a test can assert the model this
// package actually resolved was the one placed on the command line.
func fakeProbe(sessionID string, costUSD float64, reply string, args *[]string) ProbeRunner {
	return func(_ context.Context, gotArgs []string, _ sysops.Options) (*sysops.Result, error) {
		if args != nil {
			*args = gotArgs
		}
		pr := probeResult{SessionID: sessionID, TotalCostUSD: costUSD, IsError: false, Result: reply}
		stdout, err := json.Marshal(pr)
		if err != nil {
			return nil, err
		}
		return &sysops.Result{ExitCode: 0, Stdout: stdout}, nil
	}
}

func baseOptions(run ProbeRunner) Options {
	return Options{
		Plugin:       PluginIdentity{Name: "fixture-plugin", Path: "fixture-repo/fixture-plugin"},
		PluginDir:    fixturePluginDir,
		Model:        "claude-sonnet-5",
		MaxBudgetUSD: 1.00,
		Run:          run,
	}
}

// TestSanityRun_CitesRealSurfaceFlagsObscureOneAsGap is this package's core acceptance behavior:
// a real, verifiable candidate surface lands in Surfaces with its citation intact; a candidate
// surface this package cannot verify against a real file is redirected to CouldNotDetermine
// instead of being fabricated; the result validates against the manifest schema.
func TestSanityRun_CitesRealSurfaceFlagsObscureOneAsGap(t *testing.T) {
	manifest, err := Run(context.Background(), baseOptions(fakeProbe("sess-1", 0.02, candidateReplyFixture, nil)))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(manifest.Surfaces) != 1 {
		t.Fatalf("Surfaces = %d entries, want 1: %+v", len(manifest.Surfaces), manifest.Surfaces)
	}
	surface := manifest.Surfaces[0]
	if surface.Type != SurfaceCommand || surface.Citation.Path != "fixture-repo/fixture-plugin/commands/hello.md" {
		t.Errorf("surface = %+v, want the hello command citing commands/hello.md", surface)
	}
	if len(surface.WeakSpots) != 1 {
		t.Fatalf("surface.WeakSpots = %d, want 1", len(surface.WeakSpots))
	}

	for _, s := range manifest.Surfaces {
		if s.Type == SurfaceMCPServer {
			t.Fatalf("the obscure mcp-server candidate (unresolvable citation) was fabricated as a surface: %+v", s)
		}
	}

	var sawObscureGap, sawStatuslineGap bool
	for _, g := range manifest.CouldNotDetermine {
		switch {
		case g.Area == "candidate surface: obscure-server":
			sawObscureGap = true
		case g.Area == "statusline surface presence":
			sawStatuslineGap = true
		}
	}
	if !sawObscureGap {
		t.Errorf("could_not_determine = %+v, want an entry for the unresolvable obscure-server candidate", manifest.CouldNotDetermine)
	}
	if !sawStatuslineGap {
		t.Errorf("could_not_determine = %+v, want the model's own statusline gap carried through", manifest.CouldNotDetermine)
	}

	diags, err := manifest.Validate()
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(diags) != 0 {
		t.Errorf("Validate diagnostics = %v, want none", diags)
	}
}

// TestSanityRun_AmbientModelPinOverridesRequestedTier checks the first prior-art property: a
// caller's requested tier is a parameter this package always accepts, but ModelPinEnv -- set here
// the way a matrix driver would, around a whole run -- wins over it, and the manifest records the
// model that actually ran, not the one requested.
func TestSanityRun_AmbientModelPinOverridesRequestedTier(t *testing.T) {
	t.Setenv(ModelPinEnv, "claude-haiku-4-5")

	var gotArgs []string
	opts := baseOptions(fakeProbe("sess-2", 0.01, candidateReplyFixture, &gotArgs))
	opts.Model = "claude-sonnet-5"

	manifest, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if manifest.Generator == nil || manifest.Generator.Model != "claude-haiku-4-5" {
		t.Fatalf("manifest.Generator = %+v, want model claude-haiku-4-5 (the ambient pin)", manifest.Generator)
	}
	if !containsPair(gotArgs, "--model", "claude-haiku-4-5") {
		t.Errorf("probe args = %v, want --model claude-haiku-4-5 on the command line the probe actually ran", gotArgs)
	}
}

// TestSanityRun_BudgetCeilingExceededReturnsErrorButKeepsManifest checks the second prior-art
// property: real spend above the declared ceiling is caught here regardless of whether the probe
// itself honored --max-budget-usd, and the manifest that spend already bought is still returned.
func TestSanityRun_BudgetCeilingExceededReturnsErrorButKeepsManifest(t *testing.T) {
	opts := baseOptions(fakeProbe("sess-3", 5.00, candidateReplyFixture, nil))
	opts.MaxBudgetUSD = 1.00

	manifest, err := Run(context.Background(), opts)
	if manifest == nil {
		t.Fatal("Run returned a nil manifest on a budget overrun; the spend already happened and the caller needs the record")
	}
	var budgetErr *BudgetCeilingExceededError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("Run err = %v, want a *BudgetCeilingExceededError", err)
	}
	if budgetErr.SpentUSD != 5.00 || budgetErr.CeilingUSD != 1.00 {
		t.Errorf("budgetErr = %+v, want spent 5.00 over ceiling 1.00", budgetErr)
	}
}

// TestSanityRun_ZeroCeilingRejectedBeforeAnyProbeRuns checks that a metered run declaring no
// ceiling is refused outright -- checkBudget's "rejected before any probe runs" contract -- so a
// caller can never accidentally run unmetered.
func TestSanityRun_ZeroCeilingRejectedBeforeAnyProbeRuns(t *testing.T) {
	called := false
	opts := baseOptions(func(context.Context, []string, sysops.Options) (*sysops.Result, error) {
		called = true
		return nil, fmt.Errorf("must not be called")
	})
	opts.MaxBudgetUSD = 0

	if _, err := Run(context.Background(), opts); err == nil {
		t.Fatal("Run with MaxBudgetUSD=0 succeeded, want a rejection before any probe runs")
	}
	if called {
		t.Fatal("the probe ran despite MaxBudgetUSD<=0")
	}
}

// stubTranscriptSource is a minimal transcript.TranscriptSource double: ResolvePath always
// resolves to path (ignoring root/scope/sessionID, which this test does not need to vary), and
// Turns replays turns regardless of what its reader argument actually contains.
type stubTranscriptSource struct {
	path  string
	turns []transcript.Turn
}

func (s stubTranscriptSource) ResolvePath(string, string, string) string { return s.path }

func (s stubTranscriptSource) Turns(_ io.Reader, fn func(transcript.Turn) error) error {
	for _, t := range s.turns {
		if err := fn(t); err != nil {
			return err
		}
	}
	return nil
}

func (s stubTranscriptSource) DiscoverSubagentTranscripts(string) ([]string, error) { return nil, nil }

// TestSanityVerifyModelHonored checks the corroboration this package runs through
// transcript.TranscriptSource -- never a hardcoded log-format assumption -- for a matching
// transcript, a mismatched one, and the no-source/no-transcript cases, all of which must resolve
// to "nothing to attempt" rather than an error.
func TestSanityVerifyModelHonored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("seed transcript file: %v", err)
	}

	matching := stubTranscriptSource{path: path, turns: []transcript.Turn{{Model: "claude-sonnet-5"}}}
	if got := verifyModelHonored(matching, dir, "scope", "sess", "claude-sonnet-5"); !got.attempted || !got.honored {
		t.Errorf("matching transcript = %+v, want attempted && honored", got)
	}

	mismatched := stubTranscriptSource{path: path, turns: []transcript.Turn{{Model: "claude-haiku-4-5"}}}
	if got := verifyModelHonored(mismatched, dir, "scope", "sess", "claude-sonnet-5"); !got.attempted || got.honored {
		t.Errorf("mismatched transcript = %+v, want attempted && !honored", got)
	}

	if got := verifyModelHonored(nil, dir, "scope", "sess", "claude-sonnet-5"); got.attempted {
		t.Errorf("nil source = %+v, want !attempted", got)
	}

	missing := stubTranscriptSource{path: filepath.Join(dir, "does-not-exist.jsonl")}
	if got := verifyModelHonored(missing, dir, "scope", "sess", "claude-sonnet-5"); got.attempted {
		t.Errorf("unreadable transcript = %+v, want !attempted (best-effort, never a hard failure)", got)
	}
}

// TestSanityRun_ModelMismatchSurfacesAsGap wires verifyModelHonored end to end through Run: a
// transcript reporting a different model from the one this run resolved becomes a
// could_not_determine gap, never a silent pass.
func TestSanityRun_ModelMismatchSurfacesAsGap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("seed transcript file: %v", err)
	}

	opts := baseOptions(fakeProbe("sess-4", 0.01, candidateReplyFixture, nil))
	opts.Transcripts = stubTranscriptSource{path: path, turns: []transcript.Turn{{Model: "claude-haiku-4-5"}}}
	opts.TranscriptsRoot = dir
	opts.TranscriptScope = "scope"

	manifest, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var sawMismatch bool
	for _, g := range manifest.CouldNotDetermine {
		if g.Area == "model-pin honored" {
			sawMismatch = true
		}
	}
	if !sawMismatch {
		t.Errorf("could_not_determine = %+v, want a model-pin honored gap", manifest.CouldNotDetermine)
	}
}

func containsPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}
