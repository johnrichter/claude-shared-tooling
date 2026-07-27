package clikit

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/logkit"
)

// TestSanityAllElevenClassesRoundTrip builds one Result per exit class
// through its constructor and checks that MarshalCanonical reproduces the
// contract's golden bytes exactly, one class at a time in code order.
func TestSanityAllElevenClassesRoundTrip(t *testing.T) {
	goldens, err := os.ReadFile("../../schemas/clikit/examples/golden-results.jsonl")
	if err != nil {
		t.Fatalf("read goldens: %v", err)
	}
	lines := bytes.Split(bytes.TrimRight(goldens, "\n"), []byte("\n"))
	if len(lines) != 11 {
		t.Fatalf("want 11 golden lines, got %d", len(lines))
	}

	cases := []func() (*Result, error){
		func() (*Result, error) {
			return NewSuccess([]string{"navigator", "search"}, map[string]any{
				"hits": 3, "matched_paths": []any{"agent/identity.md", "agent/workflows/index.md", "README.md"}, "query": "tag:apm",
			})
		},
		func() (*Result, error) {
			cv, _ := NewCaveat("caveats.toolchain.target_skipped", "the rust target was skipped: no rust toolchain on this host",
				RunTool("rustup", "toolchain", "install", "stable"), map[string]any{"target": "rust"})
			return NewCaveats([]string{"language-tools", "build"}, map[string]any{"built": []any{"go"}, "skipped": []any{"rust"}}, []Diagnostic{cv})
		},
		func() (*Result, error) {
			e, _ := NewError("gate_negative.worktree.write_outside_worktree", "the write target is outside the task worktree",
				Triage{Kind: TriageReinvoke, Command: []string{"git-tools", "worktree", "gate", "--path", "/w/task/M2.P3.T1/README.md"}, Instruction: "write to the task worktree instead of the primary checkout"},
				map[string]any{"path": "/w/main/README.md"})
			return NewGateNegative([]string{"git-tools", "worktree", "gate"}, map[string]any{"allowed": false, "task_worktree": "/w/task/M2.P3.T1"}, []Diagnostic{e}, nil)
		},
		func() (*Result, error) {
			e, _ := NewError("precondition_unmet.index.not_built", "the discovery index has not been built for this repository",
				Reinvoke("navigator", "index", "build"), map[string]any{"index_path": "var/index/navigator.bm25"})
			return NewPreconditionUnmet([]string{"navigator", "search"}, nil, []Diagnostic{e}, nil)
		},
		func() (*Result, error) {
			e, _ := NewError("not_found.worktree.no_such_worktree", "no worktree named 'feat/y'",
				Reinvoke("git-tools", "worktree", "list"), map[string]any{"name": "feat/y"})
			return NewNotFound([]string{"git-tools", "worktree", "remove"}, nil, []Diagnostic{e}, nil)
		},
		func() (*Result, error) {
			e, _ := NewError("conflict.worktree.branch_checked_out", "branch 'feat/x' is already checked out at '/w/a'",
				Reinvoke("git-tools", "worktree", "create", "--branch", "feat/x-2"), map[string]any{"branch": "feat/x", "worktree": "/w/a"})
			return NewConflict([]string{"git-tools", "worktree", "create"}, nil, []Diagnostic{e}, nil)
		},
		func() (*Result, error) {
			e, _ := NewError("usage.flags.mutually_exclusive", "--branch and --detach cannot be combined",
				Reinvoke("git-tools", "worktree", "create", "--branch", "feat/x"), map[string]any{"flags": []any{"--branch", "--detach"}})
			return NewUsage([]string{"git-tools", "worktree", "create"}, nil, []Diagnostic{e}, nil)
		},
		func() (*Result, error) {
			e, _ := NewError("transient.http.rate_limited", "the upstream rate limited this request",
				ReinvokeAfter(30, "anthropic-tools", "rates", "fetch"), map[string]any{"http_status": 429, "url": "https://api.anthropic.com/v1/models"})
			return NewTransient([]string{"anthropic-tools", "rates", "fetch"}, nil, []Diagnostic{e}, nil)
		},
		func() (*Result, error) {
			e, _ := NewError("permission.fs.write_denied", "write denied by filesystem permissions",
				Manual("grant write access to /etc/hosts, or choose a path this user owns"), map[string]any{"mode": "0444", "path": "/etc/hosts", "uid": 1000})
			return NewPermission([]string{"claude-tools", "fs", "write"}, nil, []Diagnostic{e}, nil)
		},
		func() (*Result, error) {
			e, _ := NewError("unsupported.archive.format_not_implemented", "rar archives are not handled by this tool",
				RunTool("unar", "-o", "var/out", "var/in/bundle.rar"), map[string]any{"format": "rar", "path": "var/in/bundle.rar"})
			return NewUnsupported([]string{"claude-tools", "archive", "extract"}, nil, []Diagnostic{e}, nil)
		},
		func() (*Result, error) {
			e, _ := NewError("internal.state.invariant_violated", "a node reached a state its predecessors do not permit",
				Manual("report this with the command line and .anoikis/state.json attached; re-invocation cannot repair it"),
				map[string]any{"node": "M2.P3.T1", "predecessor_states": []any{"pending"}, "state": "done"})
			return NewInternal([]string{"anoikis-tools", "plan", "validate"}, nil, []Diagnostic{e}, nil)
		},
	}

	for i, build := range cases {
		r, err := build()
		if err != nil {
			t.Fatalf("case %d: build: %v", i, err)
		}
		got, err := r.MarshalCanonical()
		if err != nil {
			t.Fatalf("case %d: marshal: %v", i, err)
		}
		// The golden lines are themselves canonical (RFC 8785), so this is a
		// byte-for-byte check: key ordering, number formatting and escaping
		// must all match, not merely the decoded structure.
		if !bytes.Equal(got, lines[i]) {
			t.Errorf("case %d not byte-equal to golden:\n got  %s\n want %s", i, got, lines[i])
		}
	}
}

// TestSanityAllElevenExitCodesReachable checks that every status in the
// closed set maps to its pinned exit code and that StatusForExitCode
// inverts the mapping correctly.
func TestSanityAllElevenExitCodesReachable(t *testing.T) {
	statuses := []Status{
		StatusSuccess, StatusCaveats, StatusGateNegative, StatusPreconditionUnmet,
		StatusNotFound, StatusConflict, StatusUsage, StatusTransient,
		StatusPermission, StatusUnsupported, StatusInternal,
	}
	wantCodes := []int{0, 10, 20, 30, 40, 41, 50, 60, 70, 80, 90}
	if len(statuses) != 11 {
		t.Fatalf("want 11 statuses, got %d", len(statuses))
	}
	for i, s := range statuses {
		if s.ExitCode() != wantCodes[i] {
			t.Errorf("%s.ExitCode() = %d, want %d", s, s.ExitCode(), wantCodes[i])
		}
		got, ok := StatusForExitCode(wantCodes[i])
		if !ok || got != s {
			t.Errorf("StatusForExitCode(%d) = %q, %v, want %q, true", wantCodes[i], got, ok, s)
		}
	}
}

// TestSanityLogTerminatingUsesLogkit checks that LogTerminating produces an
// actual logkit record - built and written entirely by logkit.Logger - with
// the diagnostic mapped onto it per the contract: fields.clikit carries
// exit_code, status and error_code; error.message carries the diagnostic
// message verbatim; a context member lands as a same-named fields member.
func TestSanityLogTerminatingUsesLogkit(t *testing.T) {
	var buf bytes.Buffer
	logger, err := logkit.New("git-tools", logkit.WithJSON(&buf))
	if err != nil {
		t.Fatalf("logkit.New: %v", err)
	}

	e, _ := NewError("conflict.worktree.branch_checked_out", "branch 'feat/x' is already checked out at '/w/a'",
		Reinvoke("git-tools", "worktree", "create", "--branch", "feat/x-2"), map[string]any{"branch": "feat/x", "worktree": "/w/a"})
	r, err := NewConflict([]string{"git-tools", "worktree", "create"}, nil, []Diagnostic{e}, nil)
	if err != nil {
		t.Fatalf("NewConflict: %v", err)
	}
	if err := LogTerminating(logger, r, ""); err != nil {
		t.Fatalf("LogTerminating: %v", err)
	}

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	fields := rec["fields"].(map[string]any)
	clikitField := fields["clikit"].(map[string]any)
	if clikitField["exit_code"].(float64) != 41 {
		t.Errorf("fields.clikit.exit_code = %v, want 41", clikitField["exit_code"])
	}
	if clikitField["status"] != "conflict" {
		t.Errorf("fields.clikit.status = %v, want conflict", clikitField["status"])
	}
	if clikitField["error_code"] != "conflict.worktree.branch_checked_out" {
		t.Errorf("fields.clikit.error_code = %v, want the diagnostic code", clikitField["error_code"])
	}
	errObj := rec["error"].(map[string]any)
	if errObj["message"] != "branch 'feat/x' is already checked out at '/w/a'" {
		t.Errorf("error.message = %v, want the diagnostic message verbatim", errObj["message"])
	}
	if fields["branch"] != "feat/x" {
		t.Errorf("fields.branch = %v, want the context member merged verbatim", fields["branch"])
	}
	if rec["level"] != "error" {
		t.Errorf("level = %v, want error", rec["level"])
	}
}
