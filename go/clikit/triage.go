package clikit

import "fmt"

// TriageKind is a triage directive's closed kind. Adding a member is a MAJOR
// change: a caller matches it exhaustively.
type TriageKind string

const (
	// TriageReinvoke says run this same CLI again, this way. Command[0] is
	// this tool.
	TriageReinvoke TriageKind = "reinvoke"
	// TriageRunTool says run that other executable, this way. Command[0] is
	// not this tool - the sanctioned exit from the CLI-before-raw-OS-tool
	// routing rule.
	TriageRunTool TriageKind = "run_tool"
	// TriageManual says no invocation fixes this; a person must act.
	TriageManual TriageKind = "manual"
)

// Triage is the fix directive every error and caveat carries: what the
// caller does next, executable as given - concrete argv, never a
// placeholder.
type Triage struct {
	Kind         TriageKind `json:"kind"`
	Command      []string   `json:"command,omitempty"`
	Instruction  string     `json:"instruction,omitempty"`
	AfterSeconds int        `json:"after_seconds,omitempty"`
}

// Reinvoke builds a `reinvoke` triage: run this CLI again with command.
func Reinvoke(command ...string) Triage {
	return Triage{Kind: TriageReinvoke, Command: command}
}

// ReinvokeAfter builds a `reinvoke` triage carrying a retry floor - only
// meaningful for a transient failure. after is a floor, not a schedule: the
// earliest a retry is worth attempting.
func ReinvokeAfter(afterSeconds int, command ...string) Triage {
	return Triage{Kind: TriageReinvoke, Command: command, AfterSeconds: afterSeconds}
}

// RunTool builds a `run_tool` triage: hand off to a different executable.
func RunTool(command ...string) Triage {
	return Triage{Kind: TriageRunTool, Command: command}
}

// Manual builds a `manual` triage: no invocation fixes this, instruction says
// what a person does.
func Manual(instruction string) Triage {
	return Triage{Kind: TriageManual, Instruction: instruction}
}

// validate checks t against the triage shape and kind-specific rules the
// schema pins.
func (t Triage) validate() error {
	switch t.Kind {
	case TriageReinvoke:
		if len(t.Command) == 0 {
			return fmt.Errorf("clikit: reinvoke triage requires command")
		}
	case TriageRunTool:
		if len(t.Command) == 0 {
			return fmt.Errorf("clikit: run_tool triage requires command")
		}
		if t.AfterSeconds != 0 {
			return fmt.Errorf("clikit: run_tool triage must not carry after_seconds")
		}
	case TriageManual:
		if t.Instruction == "" {
			return fmt.Errorf("clikit: manual triage requires instruction")
		}
		if len(t.Command) != 0 {
			return fmt.Errorf("clikit: manual triage must not carry command")
		}
		if t.AfterSeconds != 0 {
			return fmt.Errorf("clikit: manual triage must not carry after_seconds")
		}
	default:
		return fmt.Errorf("clikit: unknown triage kind %q", string(t.Kind))
	}
	if len(t.Command) > 128 {
		return fmt.Errorf("clikit: triage command has %d elements, max 128", len(t.Command))
	}
	for _, tok := range t.Command {
		if !isArgvToken(tok) {
			return fmt.Errorf("clikit: invalid triage command token %q", tok)
		}
	}
	if t.Instruction != "" && !isLine(t.Instruction) {
		return fmt.Errorf("clikit: invalid triage instruction %q", t.Instruction)
	}
	if t.AfterSeconds < 0 || t.AfterSeconds > 86400 {
		return fmt.Errorf("clikit: after_seconds %d out of range [0,86400]", t.AfterSeconds)
	}
	return nil
}
