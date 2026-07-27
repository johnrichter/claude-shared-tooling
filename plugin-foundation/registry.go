package plugin_foundation

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/adoption"
)

// CLIRoute names one governed operation's sanctioned CLI invocation: the Bash command prefix
// Classify recognizes as the CLI route, plus what forced-use-hook.sh needs to decide whether that
// CLI is actually available on this host (BinName, and BinEnv when a download-script exports the
// verified binary path under a specific env var) and how to redirect a raw caller to it.
type CLIRoute struct {
	// InvocationPrefix is the Bash command prefix that marks an invocation as using this
	// operation's CLI. Compared the same way RawRoute.CommandPrefixes are: a literal string
	// prefix, never a glob or regex.
	InvocationPrefix string `json:"invocation_prefix"`
	// BinName is the governed CLI's executable name, used as the availability fallback the
	// hook checks with `command -v` when BinEnv is unset or does not resolve to an executable.
	BinName string `json:"bin_name"`
	// BinEnv is the environment variable a download-script exports with the verified,
	// checksummed binary's absolute path. Empty when the operation has no such variable.
	BinEnv string `json:"bin_env,omitempty"`
	// UsageHint is the exact invocation the hook's deny-and-redirect message names.
	UsageHint string `json:"usage_hint"`
}

// RawRoute names the raw tool invocation one governed operation's CLI supersedes.
type RawRoute struct {
	// ToolName is the Claude Code tool name a raw invocation used (e.g. "Bash", "Read").
	ToolName string `json:"tool_name"`
	// CommandPrefixes, when ToolName is "Bash", lists the command prefixes that count as a
	// raw invocation of this operation. Empty means every invocation of ToolName counts —
	// the shape a non-Bash raw route (or a Bash operation with no narrower prefix) declares.
	CommandPrefixes []string `json:"command_prefixes,omitempty"`
}

// Operation is one governed operation's routing rule: its name (carried through to every
// adoption.Classification and adoption.CLIAdoption it produces), its sanctioned CLI route, and
// the raw route that CLI supersedes.
type Operation struct {
	Name string   `json:"name"`
	CLI  CLIRoute `json:"cli"`
	Raw  RawRoute `json:"raw"`
}

// RoutingRules is one plugin's routing-rules.json: every governed operation it forces Claude
// toward its CLI for, in the precedence order Classify and forced-use-hook.sh both honor — the
// first operation whose CLI or raw route matches an invocation wins.
type RoutingRules struct {
	Operations []Operation `json:"operations"`
}

// LoadRoutingRules parses r as a routing-rules.json document.
func LoadRoutingRules(r io.Reader) (RoutingRules, error) {
	var rules RoutingRules
	if err := json.NewDecoder(r).Decode(&rules); err != nil {
		return RoutingRules{}, fmt.Errorf("plugin_foundation: decode routing rules: %w", err)
	}
	return rules, nil
}

// LoadRoutingRulesFile opens path and parses it as a routing-rules.json document.
func LoadRoutingRulesFile(path string) (RoutingRules, error) {
	f, err := os.Open(path)
	if err != nil {
		return RoutingRules{}, fmt.Errorf("plugin_foundation: open routing rules %s: %w", path, err)
	}
	defer f.Close()
	return LoadRoutingRules(f)
}

// bashCommand extracts inv's Bash command string, or "" (and false) when inv is not a Bash
// invocation or carries no string "command" input.
func bashCommand(inv adoption.Invocation) (string, bool) {
	if inv.ToolName != "Bash" {
		return "", false
	}
	cmd, ok := inv.Input["command"].(string)
	return cmd, ok
}

// hasCommandPrefix reports whether cmd invokes prefix as a whole token sequence: cmd equals
// prefix exactly, or starts with prefix followed by a space. A plain strings.HasPrefix would also
// match "example-cli statuses" against the prefix "example-cli status" — a different, unrelated
// subcommand that merely shares leading characters.
func hasCommandPrefix(cmd, prefix string) bool {
	return cmd == prefix || strings.HasPrefix(cmd, prefix+" ")
}

// matchesCommandPrefixes reports whether cmd matches any of prefixes (see hasCommandPrefix), or —
// when prefixes is empty — always reports true, matching every Bash invocation regardless of
// command text.
func matchesCommandPrefixes(cmd string, prefixes []string) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, p := range prefixes {
		if hasCommandPrefix(cmd, p) {
			return true
		}
	}
	return false
}

// cliMatch builds op.CLI's CLIMatch predicate: a Bash invocation whose command matches the
// operation's sanctioned invocation prefix (see hasCommandPrefix).
func cliMatch(op Operation) func(adoption.Invocation) bool {
	prefix := op.CLI.InvocationPrefix
	return func(inv adoption.Invocation) bool {
		cmd, ok := bashCommand(inv)
		return ok && hasCommandPrefix(cmd, prefix)
	}
}

// rawMatch builds op.Raw's RawMatch predicate: an invocation of the declared raw tool, and — for
// Bash — one whose command matches one of the declared prefixes (or any command, when none are
// declared).
func rawMatch(op Operation) func(adoption.Invocation) bool {
	raw := op.Raw
	return func(inv adoption.Invocation) bool {
		if inv.ToolName != raw.ToolName {
			return false
		}
		if raw.ToolName != "Bash" {
			return true
		}
		cmd, ok := bashCommand(inv)
		return ok && matchesCommandPrefixes(cmd, raw.CommandPrefixes)
	}
}

// BuildRegistry turns rules into the adoption.GovernedOperation registry adoption.Classify
// expects, in the same declaration order — the one conversion every govern-now CLI's plugin
// relies on instead of writing its own CLIMatch/RawMatch closures. forced-use-hook.sh applies the
// identical prefix semantics against the same routing-rules.json, so a plugin's live routing
// decision and its adoption measurement read one shared source of truth.
func BuildRegistry(rules RoutingRules) []adoption.GovernedOperation {
	out := make([]adoption.GovernedOperation, 0, len(rules.Operations))
	for _, op := range rules.Operations {
		out = append(out, adoption.GovernedOperation{
			Name:     op.Name,
			CLIMatch: cliMatch(op),
			RawMatch: rawMatch(op),
		})
	}
	return out
}
