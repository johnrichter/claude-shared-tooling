package plugin_foundation

import (
	"strings"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/adoption"
)

// TestLoadRoutingRulesParsesOperations checks field-level decoding of a routing-rules.json
// document: operation order, an optional bin_env present, and an omitted command_prefixes
// decoding to an empty (never nil-vs-empty-ambiguous) slice.
func TestLoadRoutingRulesParsesOperations(t *testing.T) {
	doc := `{"operations":[
		{"name":"status","cli":{"invocation_prefix":"example-cli status","bin_name":"example-cli","bin_env":"EXAMPLE_CLI_BIN","usage_hint":"example-cli status"},
		 "raw":{"tool_name":"Bash","command_prefixes":["git status"]}},
		{"name":"read-config","cli":{"invocation_prefix":"example-cli config get","bin_name":"example-cli","usage_hint":"example-cli config get"},
		 "raw":{"tool_name":"Read"}}
	]}`
	rules, err := LoadRoutingRules(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("LoadRoutingRules: %v", err)
	}
	if len(rules.Operations) != 2 {
		t.Fatalf("len(Operations) = %d, want 2", len(rules.Operations))
	}
	if rules.Operations[0].Name != "status" || rules.Operations[0].CLI.BinEnv != "EXAMPLE_CLI_BIN" {
		t.Errorf("Operations[0] = %+v, want name=status bin_env=EXAMPLE_CLI_BIN", rules.Operations[0])
	}
	if rules.Operations[1].Raw.ToolName != "Read" || len(rules.Operations[1].Raw.CommandPrefixes) != 0 {
		t.Errorf("Operations[1].Raw = %+v, want ToolName=Read, no prefixes", rules.Operations[1].Raw)
	}
}

// TestLoadRoutingRulesFileMissingIsError checks that a missing routing-rules.json path surfaces
// as an error rather than an empty RoutingRules — a plugin's own load path should not silently
// run with zero governed operations.
func TestLoadRoutingRulesFileMissingIsError(t *testing.T) {
	if _, err := LoadRoutingRulesFile("testdata/does-not-exist.json"); err == nil {
		t.Fatal("LoadRoutingRulesFile(missing) = nil error, want an error")
	}
}

func statusRules() RoutingRules {
	return RoutingRules{Operations: []Operation{
		{
			Name: "status",
			CLI:  CLIRoute{InvocationPrefix: "example-cli status", BinName: "example-cli", UsageHint: "example-cli status"},
			Raw:  RawRoute{ToolName: "Bash", CommandPrefixes: []string{"git status", "git diff --stat"}},
		},
		{
			Name: "read-config",
			CLI:  CLIRoute{InvocationPrefix: "example-cli config get", BinName: "example-cli", UsageHint: "example-cli config get"},
			Raw:  RawRoute{ToolName: "Read"},
		},
	}}
}

// TestBuildRegistryPreservesOrderAndNames checks that BuildRegistry's output is a straight,
// order-preserving projection of RoutingRules.Operations — the precedence Classify relies on.
func TestBuildRegistryPreservesOrderAndNames(t *testing.T) {
	reg := BuildRegistry(statusRules())
	if len(reg) != 2 || reg[0].Name != "status" || reg[1].Name != "read-config" {
		t.Fatalf("BuildRegistry order/names = %+v, want [status read-config]", reg)
	}
}

// TestCLIMatchIsPrefixOnBashCommandOnly checks CLIMatch's three edge cases: a genuine prefix
// match, rejection of a non-Bash tool regardless of its Input contents, and rejection of a
// command that only shares leading characters with the prefix rather than the whole token.
func TestCLIMatchIsPrefixOnBashCommandOnly(t *testing.T) {
	reg := BuildRegistry(statusRules())
	status := reg[0]

	cliInv := adoption.Invocation{ToolName: "Bash", Input: map[string]any{"command": "example-cli status --json"}}
	if !status.CLIMatch(cliInv) {
		t.Error("CLIMatch should match a Bash command starting with the invocation prefix")
	}

	wrongTool := adoption.Invocation{ToolName: "Read", Input: map[string]any{"command": "example-cli status"}}
	if status.CLIMatch(wrongTool) {
		t.Error("CLIMatch should never match a non-Bash tool, regardless of Input contents")
	}

	prefixOfSomethingElse := adoption.Invocation{ToolName: "Bash", Input: map[string]any{"command": "example-cli statuses"}}
	if status.CLIMatch(prefixOfSomethingElse) {
		t.Error("CLIMatch should not match a command that only shares a leading substring, e.g. 'statuses' vs 'status'")
	}
}

// TestRawMatchWithPrefixesRequiresOneOfThem checks that a declared command_prefixes list is an
// allowlist: every listed prefix matches, and an unrelated Bash command does not.
func TestRawMatchWithPrefixesRequiresOneOfThem(t *testing.T) {
	reg := BuildRegistry(statusRules())
	status := reg[0]

	for _, cmd := range []string{"git status", "git status --short", "git diff --stat"} {
		inv := adoption.Invocation{ToolName: "Bash", Input: map[string]any{"command": cmd}}
		if !status.RawMatch(inv) {
			t.Errorf("RawMatch(%q) = false, want true", cmd)
		}
	}
	unrelated := adoption.Invocation{ToolName: "Bash", Input: map[string]any{"command": "git log"}}
	if status.RawMatch(unrelated) {
		t.Error("RawMatch should not match a Bash command outside the declared prefixes")
	}
}

// TestRawMatchWithNoPrefixesMatchesAnyInvocationOfTheTool checks the no-command_prefixes shape a
// non-Bash raw route declares: every invocation of the named tool matches, and no other tool does.
func TestRawMatchWithNoPrefixesMatchesAnyInvocationOfTheTool(t *testing.T) {
	reg := BuildRegistry(statusRules())
	readConfig := reg[1]

	anyRead := adoption.Invocation{ToolName: "Read", Input: map[string]any{"file_path": "/etc/example/config.yaml"}}
	if !readConfig.RawMatch(anyRead) {
		t.Error("RawMatch with no command_prefixes should match any invocation of the declared tool")
	}
	notRead := adoption.Invocation{ToolName: "Bash", Input: map[string]any{"command": "cat /etc/example/config.yaml"}}
	if readConfig.RawMatch(notRead) {
		t.Error("RawMatch should never match a different tool_name than the one it declares")
	}
}

// TestClassifyHonorsCLIBeforeRawPrecedencePerOperation checks that a BuildRegistry-produced
// registry preserves adoption.Classify's per-operation precedence: CLIMatch is tried before
// RawMatch, so a CLI invocation classifies as RouteCLI even when its argv also embeds raw-looking
// command text.
func TestClassifyHonorsCLIBeforeRawPrecedencePerOperation(t *testing.T) {
	reg := BuildRegistry(statusRules())
	// A CLI invocation must classify as RouteCLI even though its argv also happens to embed
	// the raw command text as an argument — CLIMatch is tried before RawMatch per operation.
	inv := adoption.Invocation{ToolName: "Bash", Input: map[string]any{"command": "example-cli status --raw-equivalent 'git status'"}}
	classifications := adoption.Classify(reg, []adoption.Invocation{inv})
	if len(classifications) != 1 || classifications[0].Route != adoption.RouteCLI || classifications[0].Operation != "status" {
		t.Fatalf("Classify = %+v, want one RouteCLI classification for operation status", classifications)
	}
}
