package toolchain

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
)

func init() {
	Register(pythonAdapter{})
}

// pythonAdapter is the Adapter for Python projects, fronting two tools: uv
// for build and test, ruff for lint and format. build and test run through
// uv rather than a bare python/pytest invocation so a project's own
// dependency pins govern the run, exactly like cargo and go run against
// their own module's declared dependencies rather than whatever happens to
// be on PATH.
type pythonAdapter struct{}

func (pythonAdapter) Language() string { return "python" }

// Route reports the subprocess route for every check: both uv and ruff ship
// as binaries, and pythonAdapter performs no analysis of its own.
func (pythonAdapter) Route(check Check) Route { return RouteSubprocess }

// Tool names uv for build and test, ruff for lint and format. vet has no
// Python equivalent and is never reached through Run (Command rejects it
// first), but Tool must still answer something for it: Run reads Tool
// before it reads Command's error.
func (pythonAdapter) Tool(check Check) string {
	switch check {
	case CheckLint, CheckFormat:
		return "ruff"
	default:
		return "uv"
	}
}

// Command returns uv's or ruff's argv for check. build runs `uv sync
// --locked`, which fails on a uv.lock that doesn't match pyproject.toml
// rather than silently re-resolving it — the same stale-lock guard cargo's
// --locked and go.sum's checksum verification give their languages. test
// runs `uv run pytest`: pytest itself is never installed as a toolchain
// pin, it arrives through the project's own dependency group (e.g.
// dependency-groups.dev), and `uv run` is what resolves and activates that
// project's environment before invoking it — a bare `pytest` on PATH would
// depend on whatever happened to be installed globally instead. lint runs
// `ruff check`; format runs `ruff format --check`, never bare `ruff
// format`, because without --check ruff format rewrites every unformatted
// file in place rather than just reporting which ones need it.
func (a pythonAdapter) Command(check Check) ([]string, error) {
	switch check {
	case CheckBuild:
		return []string{"sync", "--locked"}, nil
	case CheckTest:
		return []string{"run", "pytest"}, nil
	case CheckLint:
		return []string{"check"}, nil
	case CheckFormat:
		return []string{"format", "--check"}, nil
	default:
		return nil, errUnsupportedCheck(a.Tool(check), check)
	}
}

// RunInProcess is unreachable through Run — Route sends every check through
// the subprocess path — and reports the unsupported-check error to a direct
// caller.
func (a pythonAdapter) RunInProcess(_ context.Context, target Target) ([]Diagnostic, error) {
	return nil, errUnsupportedCheck(a.Tool(target.Check), target.Check)
}

// pytestFailureRE matches one line of pytest's short test summary info (the
// section after "===... short test summary info ...===="): a failed test's
// node ID and, optionally, the one-line reason pytest printed after the
// dash. This is the one line in pytest's default output that names a
// specific failing test without also carrying a full traceback for Parse to
// pick apart.
var pytestFailureRE = regexp.MustCompile(`^FAILED (\S+)(?: - (.*))?$`)

// ruffRuleHeaderRE matches the first line of one `ruff check` violation
// block: a rule code, an optional "[*]" marking it fixable, and the
// violation's message.
var ruffRuleHeaderRE = regexp.MustCompile(`^([A-Z]+[0-9]+) (?:\[\*\] )?(.+)$`)

// ruffFormatHeaderRE matches the first line of one `ruff format --check`
// violation block. ruff names every such block "unformatted" regardless of
// what changed, so the block carries no rule code the way a lint violation
// does.
var ruffFormatHeaderRE = regexp.MustCompile(`^unformatted: (.+)$`)

// ruffLocationRE matches the second line of either ruff violation block: the
// file/line/column the block's header describes, e.g. " --> src/app.py:3:1".
var ruffLocationRE = regexp.MustCompile(`^ --> (.+):([0-9]+):[0-9]+$`)

// Parse reads uv's and ruff's output line by line. A ruff violation prints
// its header (a rule code or "unformatted") on one line and its file
// location on the next; Parse holds the header's code/message until that
// location line arrives, then emits one Diagnostic combining both — this is
// the only shape here that spans more than one line. A pytest failure names
// its test on a single short-summary line. Everything else — uv's own
// progress and error text, ruff's per-run summary line, the diff body under
// a violation block — is expected noise and yields nothing directly; a
// non-zero exit with nothing parsed still falls back to one synthetic
// diagnostic, the same fallback every subprocess-routed adapter in this
// package uses for a shape it doesn't specifically parse.
func (pythonAdapter) Parse(exitCode int, stdout, stderr []byte) ([]Diagnostic, error) {
	var diags []Diagnostic
	var pendingCode, pendingMessage string
	havePending := false

	for _, chunk := range [][]byte{stdout, stderr} {
		for _, raw := range bytes.Split(chunk, []byte("\n")) {
			line := string(bytes.TrimRight(raw, "\r"))
			trimmed := bytes.TrimSpace(raw)
			if len(trimmed) == 0 {
				continue
			}

			if m := ruffLocationRE.FindStringSubmatch(line); m != nil && havePending {
				diags = append(diags, Diagnostic{
					Severity: SeverityError,
					Code:     pendingCode,
					Message:  pendingMessage,
					File:     m[1],
					Line:     atoiOrZero(m[2]),
				})
				havePending = false
				continue
			}
			if m := ruffRuleHeaderRE.FindStringSubmatch(line); m != nil {
				pendingCode, pendingMessage, havePending = m[1], m[2], true
				continue
			}
			if m := ruffFormatHeaderRE.FindStringSubmatch(line); m != nil {
				pendingCode, pendingMessage, havePending = "", m[1], true
				continue
			}
			if m := pytestFailureRE.FindStringSubmatch(string(trimmed)); m != nil {
				msg := "test failed: " + m[1]
				if m[2] != "" {
					msg = fmt.Sprintf("test failed: %s - %s", m[1], m[2])
				}
				diags = append(diags, Diagnostic{Severity: SeverityError, Message: msg})
				continue
			}
		}
	}

	if len(diags) == 0 && exitCode != 0 {
		diags = append(diags, Diagnostic{
			Severity: SeverityError,
			Message:  fmt.Sprintf("uv or ruff exited %d with no parsed diagnostics; see log_ref for raw output", exitCode),
		})
	}
	return diags, nil
}

// atoiOrZero parses a decimal string already validated by ruffLocationRE's
// digit class, falling back to zero rather than propagating an error Parse
// has no channel to report through.
func atoiOrZero(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}
