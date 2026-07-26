package clikit

import "regexp"

// isLine reports whether s satisfies the schema's $defs/line: non-empty, at
// most 4096 characters, and free of control characters, so it survives the
// one-line JSON rendering unchanged. Same bound as logkit's `line`.
func isLine(s string) bool {
	if s == "" || len(s) > 4096 {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// isArgvToken reports whether s satisfies $defs/argv_token: any non-empty,
// bounded string with no control character - one already-unquoted argv
// element of a triage command.
func isArgvToken(s string) bool {
	if s == "" || len(s) > 4096 {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

var (
	toolNamePattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	subcommandNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	dataKeyPattern        = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	diagnosticCodePattern = regexp.MustCompile(`^(caveats|conflict|gate_negative|internal|not_found|permission|precondition_unmet|transient|unsupported|usage)(\.[a-z0-9]+(_[a-z0-9]+)*){1,3}$`)
)

// isToolName reports whether s satisfies $defs/tool_name: command[0], the
// CLI's own identity and the same string it gives logkit as `service`.
func isToolName(s string) bool {
	return len(s) >= 1 && len(s) <= 64 && toolNamePattern.MatchString(s)
}

// isSubcommandName reports whether s satisfies $defs/subcommand_name: one
// canonical subcommand element of `command` after index 0.
func isSubcommandName(s string) bool {
	return len(s) >= 1 && len(s) <= 64 && subcommandNamePattern.MatchString(s)
}

// isDataKey reports whether s is a valid snake_case member name for `data`
// or a diagnostic's `context`.
func isDataKey(s string) bool {
	return len(s) >= 1 && len(s) <= 128 && dataKeyPattern.MatchString(s)
}

// diagnosticClass returns a diagnostic code's first segment - the status name
// it belongs to, per $defs/diagnostic_code - and whether the code is
// well-formed at all.
func diagnosticClass(code string) (string, bool) {
	if len(code) < 3 || len(code) > 128 || !diagnosticCodePattern.MatchString(code) {
		return "", false
	}
	for i, r := range code {
		if r == '.' {
			return code[:i], true
		}
	}
	return "", false
}
