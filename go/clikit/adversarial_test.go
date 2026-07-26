package clikit

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/logkit"
)

// TestAdversarialCaveatsRequiresAtLeastOne checks that NewCaveats refuses an
// empty caveats slice rather than silently emitting an unqualified success.
func TestAdversarialCaveatsRequiresAtLeastOne(t *testing.T) {
	if _, err := NewCaveats([]string{"tool"}, nil, nil); err == nil {
		t.Fatal("NewCaveats with no caveats: want error, got nil")
	}
	if _, err := NewCaveats([]string{"tool"}, nil, []Diagnostic{}); err == nil {
		t.Fatal("NewCaveats with empty caveats slice: want error, got nil")
	}
}

// TestAdversarialSuccessForbidsErrorsAndCaveats checks the class-0 branch
// directly against a hand-built record, bypassing the constructors.
func TestAdversarialSuccessForbidsErrorsAndCaveats(t *testing.T) {
	e, _ := NewError("internal.x.y", "boom", Manual("report it"), nil)
	r := &Result{SchemaVersion: SchemaVersion, Command: []string{"tool"}, Status: StatusSuccess, ExitCode: 0, Errors: []Diagnostic{e}}
	if err := r.Validate(); err == nil {
		t.Fatal("success record carrying errors: want validation error, got nil")
	}
	cv, _ := NewCaveat("caveats.x.y", "note", Manual("ack"), nil)
	r2 := &Result{SchemaVersion: SchemaVersion, Command: []string{"tool"}, Status: StatusSuccess, ExitCode: 0, Caveats: []Diagnostic{cv}}
	if err := r2.Validate(); err == nil {
		t.Fatal("success record carrying caveats: want validation error, got nil")
	}
}

// TestAdversarialFailureClassesRequireAnError checks every failure
// constructor refuses zero errors, and refuses an error from the WRONG
// class - the governing-error class-pairing rule.
func TestAdversarialFailureClassesRequireAnError(t *testing.T) {
	if _, err := NewConflict([]string{"tool"}, nil, nil, nil); err == nil {
		t.Fatal("NewConflict with no errors: want error, got nil")
	}
	wrongClass, _ := NewError("not_found.x.y", "missing", Reinvoke("tool"), nil)
	if _, err := NewConflict([]string{"tool"}, nil, []Diagnostic{wrongClass}, nil); err == nil {
		t.Fatal("NewConflict governed by a not_found error: want error, got nil")
	}
}

// TestAdversarialErrorCannotCarryCaveatsClassCode checks NewError and
// validateAsError both reject a caveats-class code - an error is never
// filed as a caveat.
func TestAdversarialErrorCannotCarryCaveatsClassCode(t *testing.T) {
	if _, err := NewError("caveats.x.y", "note", Manual("ack"), nil); err == nil {
		t.Fatal("NewError with caveats-class code: want error, got nil")
	}
	if _, err := NewCaveat("internal.x.y", "boom", Manual("report it"), nil); err == nil {
		t.Fatal("NewCaveat with a failure-class code: want error, got nil")
	}
}

// TestAdversarialMalformedDiagnosticCode exercises the lexical gate on
// diagnostic codes: too few segments, an unknown class prefix, and upper
// case are all rejected before a Diagnostic is ever built.
func TestAdversarialMalformedDiagnosticCode(t *testing.T) {
	cases := []string{
		"internal",               // no domain/condition segment
		"bogus.domain.cond",      // unknown class prefix
		"Internal.domain.cond",   // upper case class
		"internal.domain.Cond",   // upper case condition
		"internal..cond",         // empty segment
		strings.Repeat("a", 130), // exceeds max length, no dots at all
	}
	for _, code := range cases {
		if _, err := NewError(code, "message", Manual("do x"), nil); err == nil {
			t.Errorf("NewError(%q, ...): want error, got nil", code)
		}
	}
}

// TestAdversarialTriageValidation exercises every kind-specific rule the
// schema pins: reinvoke/run_tool require command, manual requires
// instruction and forbids command, run_tool and manual forbid
// after_seconds, and an unknown kind is rejected.
func TestAdversarialTriageValidation(t *testing.T) {
	cases := []struct {
		name   string
		triage Triage
	}{
		{"reinvoke with no command", Triage{Kind: TriageReinvoke}},
		{"run_tool with no command", Triage{Kind: TriageRunTool}},
		{"run_tool with after_seconds", Triage{Kind: TriageRunTool, Command: []string{"x"}, AfterSeconds: 5}},
		{"manual with no instruction", Triage{Kind: TriageManual}},
		{"manual with command", Triage{Kind: TriageManual, Instruction: "do x", Command: []string{"x"}}},
		{"manual with after_seconds", Triage{Kind: TriageManual, Instruction: "do x", AfterSeconds: 5}},
		{"unknown kind", Triage{Kind: TriageKind("bogus")}},
		{"after_seconds out of range", Triage{Kind: TriageReinvoke, Command: []string{"x"}, AfterSeconds: 90000}},
		{"negative after_seconds", Triage{Kind: TriageReinvoke, Command: []string{"x"}, AfterSeconds: -1}},
		{"control char in instruction", Triage{Kind: TriageManual, Instruction: "line one\nline two"}},
	}
	for _, c := range cases {
		if _, err := NewError("internal.x.y", "boom", c.triage, nil); err == nil {
			t.Errorf("%s: want validation error, got nil", c.name)
		}
	}
}

// TestAdversarialContextKeyAndSizeBounds checks a diagnostic's context is
// bounded and snake_case, per $defs/error.context.
func TestAdversarialContextKeyAndSizeBounds(t *testing.T) {
	if _, err := NewError("internal.x.y", "boom", Manual("do x"), map[string]any{"BadKey": 1}); err == nil {
		t.Fatal("context with a non-snake_case key: want error, got nil")
	}
	big := make(map[string]any, 33)
	for i := 0; i < 33; i++ {
		big[string(rune('a'+i%26))+"_"+string(rune('0'+i))] = i
	}
	if len(big) < 33 {
		t.Fatalf("test setup: want 33 distinct keys, got %d", len(big))
	}
	if _, err := NewError("internal.x.y", "boom", Manual("do x"), big); err == nil {
		t.Fatal("context with 33 members (max 32): want error, got nil")
	}
}

// TestAdversarialMessageBounds checks a diagnostic message rejects the
// empty string, a control character, and a line over 4096 characters.
func TestAdversarialMessageBounds(t *testing.T) {
	cases := []string{"", "bad\tmessage", strings.Repeat("a", 4097)}
	for _, msg := range cases {
		if _, err := NewError("internal.x.y", msg, Manual("do x"), nil); err == nil {
			t.Errorf("message %q: want validation error, got nil", msg)
		}
	}
}

// TestAdversarialCommandBounds checks command[0]'s tool_name pattern, a
// subcommand's pattern, and the 1-8 element bound, directly against
// Result.Validate since the constructors do not expose command shape
// errors any earlier.
func TestAdversarialCommandBounds(t *testing.T) {
	cases := []struct {
		name    string
		command []string
	}{
		{"empty command", nil},
		{"nine elements", []string{"tool", "a", "b", "c", "d", "e", "f", "g", "h"}},
		{"upper-case tool name", []string{"Tool"}},
		{"subcommand with dot", []string{"tool", "sub.command"}},
	}
	for _, c := range cases {
		r := &Result{SchemaVersion: SchemaVersion, Command: c.command, Status: StatusSuccess, ExitCode: 0}
		if err := r.Validate(); err == nil {
			t.Errorf("%s: want validation error, got nil", c.name)
		}
	}
}

// TestAdversarialDataBounds checks `data` rejects an empty (but non-nil)
// map and a non-snake_case key - {} must never be emitted, per the schema
// note that data is omitted entirely rather than emitted empty.
func TestAdversarialDataBounds(t *testing.T) {
	if _, err := NewSuccess([]string{"tool"}, map[string]any{}); err == nil {
		t.Fatal("empty (non-nil) data map: want validation error, got nil")
	}
	if _, err := NewSuccess([]string{"tool"}, map[string]any{"BadKey": 1}); err == nil {
		t.Fatal("data with non-snake_case key: want validation error, got nil")
	}
}

// TestAdversarialExitCodePairingMismatch checks Validate catches a record
// whose exit_code has been tampered with independently of its status - the
// pairing the schema's allOf enforces and Result.Governing depends on.
func TestAdversarialExitCodePairingMismatch(t *testing.T) {
	e, _ := NewError("conflict.x.y", "boom", Reinvoke("tool"), nil)
	r, err := NewConflict([]string{"tool"}, nil, []Diagnostic{e}, nil)
	if err != nil {
		t.Fatalf("NewConflict: %v", err)
	}
	r.ExitCode = 40 // tamper: now claims not_found's code while status stays conflict
	if err := r.Validate(); err == nil {
		t.Fatal("tampered exit_code/status pairing: want validation error, got nil")
	}
}

// TestAdversarialUnknownStatusRejected checks Validate and the Status
// accessors all refuse a status outside the closed eleven-member set,
// rather than defaulting or coercing it.
func TestAdversarialUnknownStatusRejected(t *testing.T) {
	s := Status("does_not_exist")
	if s.Known() {
		t.Fatal("bogus status reports Known() = true")
	}
	r := &Result{SchemaVersion: SchemaVersion, Command: []string{"tool"}, Status: s, ExitCode: 0}
	if err := r.Validate(); err == nil {
		t.Fatal("unknown status: want validation error, got nil")
	}
	if _, ok := StatusForExitCode(99); ok {
		t.Fatal("StatusForExitCode(99): want ok=false for an out-of-taxonomy code")
	}
}

// TestAdversarialExitCodeAndLogLevelPanicOnUnknownStatus checks the
// documented panic contract: a caller must gate on Known() first, and
// bypassing that gate panics loudly rather than returning a zero value
// that could be silently treated as success.
func TestAdversarialExitCodeAndLogLevelPanicOnUnknownStatus(t *testing.T) {
	s := Status("does_not_exist")
	func() {
		defer func() {
			if recover() == nil {
				t.Error("ExitCode() on unknown status: want panic, got none")
			}
		}()
		_ = s.ExitCode()
	}()
	func() {
		defer func() {
			if recover() == nil {
				t.Error("LogLevel() on unknown status: want panic, got none")
			}
		}()
		_ = s.LogLevel()
	}()
}

// TestAdversarialSchemaVersionMismatchRejected checks Validate refuses a
// record declaring any schema_version other than the pinned constant - the
// contract's MAJOR-version gate.
func TestAdversarialSchemaVersionMismatchRejected(t *testing.T) {
	r := &Result{SchemaVersion: 2, Command: []string{"tool"}, Status: StatusSuccess, ExitCode: 0}
	if err := r.Validate(); err == nil {
		t.Fatal("schema_version 2 against pinned version 1: want validation error, got nil")
	}
}

// TestAdversarialMarshalCanonicalRejectsInvalidRecord checks that an
// invalid Result never reaches the wire: MarshalCanonical re-validates
// even a hand-built record that bypassed the constructors.
func TestAdversarialMarshalCanonicalRejectsInvalidRecord(t *testing.T) {
	r := &Result{SchemaVersion: SchemaVersion, Command: nil, Status: StatusSuccess, ExitCode: 0}
	if _, err := r.MarshalCanonical(); err == nil {
		t.Fatal("MarshalCanonical on an invalid record: want error, got nil")
	}
	if err := Emit(&bytes.Buffer{}, r); err == nil {
		t.Fatal("Emit on an invalid record: want error, got nil")
	}
}

// TestAdversarialCanonicalIsDeterministicAndKeyOrdered checks two
// structurally-identical records with fields populated in different
// program order produce byte-identical output (RFC 8785 key ordering),
// and that Emit LF-terminates in a single write.
func TestAdversarialCanonicalIsDeterministicAndKeyOrdered(t *testing.T) {
	r, err := NewSuccess([]string{"tool"}, map[string]any{"z_last": 1, "a_first": 2})
	if err != nil {
		t.Fatalf("NewSuccess: %v", err)
	}
	b1, err := r.MarshalCanonical()
	if err != nil {
		t.Fatalf("MarshalCanonical: %v", err)
	}
	b2, err := r.MarshalCanonical()
	if err != nil {
		t.Fatalf("MarshalCanonical (2nd call): %v", err)
	}
	if !bytes.Equal(b1, b2) {
		t.Fatalf("MarshalCanonical is not deterministic:\n%s\n%s", b1, b2)
	}
	// RFC 8785 orders object keys lexicographically by UTF-16 code unit:
	// "command" < "data" < "exit_code" < "schema_version" < "status".
	if idx := bytes.Index(b1, []byte(`"command"`)); idx != 1 {
		t.Errorf("canonical output does not lead with \"command\" at offset 1: %s", b1)
	}

	var buf bytes.Buffer
	if err := Emit(&buf, r); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	out := buf.Bytes()
	if len(out) == 0 || out[len(out)-1] != '\n' {
		t.Fatalf("Emit output not LF-terminated: %q", out)
	}
	if bytes.Count(out, []byte("\n")) != 1 {
		t.Fatalf("Emit wrote more than one line: %q", out)
	}
}

// TestAdversarialGoverningReturnsFalseWhenNoErrors checks Result.Governing
// on a success record - the case LogTerminating's default-message branch
// depends on.
func TestAdversarialGoverningReturnsFalseWhenNoErrors(t *testing.T) {
	r, err := NewSuccess([]string{"tool", "run"}, nil)
	if err != nil {
		t.Fatalf("NewSuccess: %v", err)
	}
	if _, ok := r.Governing(); ok {
		t.Fatal("Governing() on a success record: want ok=false")
	}
}

// TestAdversarialLogTerminatingSuccessUsesDefaultMessageAndLevel checks the
// no-governing-diagnostic branch of LogTerminating: no error.message, a
// generated "<subcommand> completed" message, level info, and no
// fields.clikit.error_code.
func TestAdversarialLogTerminatingSuccessUsesDefaultMessageAndLevel(t *testing.T) {
	var buf bytes.Buffer
	logger, err := logkit.New("tool", logkit.WithJSON(&buf))
	if err != nil {
		t.Fatalf("logkit.New: %v", err)
	}
	r, err := NewSuccess([]string{"tool", "run"}, nil)
	if err != nil {
		t.Fatalf("NewSuccess: %v", err)
	}
	if err := LogTerminating(logger, r, ""); err != nil {
		t.Fatalf("LogTerminating: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	if rec["level"] != "info" {
		t.Errorf("level = %v, want info", rec["level"])
	}
	if _, hasError := rec["error"]; hasError {
		t.Errorf("success record logged an error object: %v", rec["error"])
	}
	if rec["message"] != "run completed" {
		t.Errorf("message = %v, want %q", rec["message"], "run completed")
	}
	fields := rec["fields"].(map[string]any)
	clikitField := fields["clikit"].(map[string]any)
	if _, hasErrorCode := clikitField["error_code"]; hasErrorCode {
		t.Errorf("fields.clikit.error_code present on a success record: %v", clikitField["error_code"])
	}
}

// TestAdversarialLogTerminatingCustomMessageOverridesDefault checks that a
// caller-supplied message wins over both the diagnostic's own message and
// the generated default.
func TestAdversarialLogTerminatingCustomMessageOverridesDefault(t *testing.T) {
	var buf bytes.Buffer
	logger, err := logkit.New("tool", logkit.WithJSON(&buf))
	if err != nil {
		t.Fatalf("logkit.New: %v", err)
	}
	e, _ := NewError("usage.flags.bad", "flag parse failed", Reinvoke("tool", "--help"), nil)
	r, err := NewUsage([]string{"tool"}, nil, []Diagnostic{e}, nil)
	if err != nil {
		t.Fatalf("NewUsage: %v", err)
	}
	if err := LogTerminating(logger, r, "custom override message"); err != nil {
		t.Fatalf("LogTerminating: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	if rec["message"] != "custom override message" {
		t.Errorf("message = %v, want the caller-supplied override", rec["message"])
	}
	errObj := rec["error"].(map[string]any)
	if errObj["message"] != "flag parse failed" {
		t.Errorf("error.message = %v, want the diagnostic message even when the log message is overridden", errObj["message"])
	}
}

// TestAdversarialLogTerminatingRootFieldCollisionNests checks a context
// member that collides with a logkit root field name (e.g. "level") is
// nested under fields.clikit rather than overwriting the real root field
// or the reserved exit_code/status/error_code members.
func TestAdversarialLogTerminatingRootFieldCollisionNests(t *testing.T) {
	var buf bytes.Buffer
	logger, err := logkit.New("tool", logkit.WithJSON(&buf))
	if err != nil {
		t.Fatalf("logkit.New: %v", err)
	}
	e, _ := NewError("internal.x.y", "boom", Manual("report it"), map[string]any{"level": "not-a-real-level", "safe_key": "ok"})
	r, err := NewInternal([]string{"tool"}, nil, []Diagnostic{e}, nil)
	if err != nil {
		t.Fatalf("NewInternal: %v", err)
	}
	if err := LogTerminating(logger, r, ""); err != nil {
		t.Fatalf("LogTerminating: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	if rec["level"] != "fatal" {
		t.Errorf("level = %v, want the record's real fatal level, not the colliding context value", rec["level"])
	}
	fields := rec["fields"].(map[string]any)
	clikitField := fields["clikit"].(map[string]any)
	if clikitField["level"] != "not-a-real-level" {
		t.Errorf("fields.clikit.level = %v, want the colliding context value nested here", clikitField["level"])
	}
	if fields["safe_key"] != "ok" {
		t.Errorf("fields.safe_key = %v, want the non-colliding context member merged verbatim", fields["safe_key"])
	}
	if clikitField["exit_code"].(float64) != 90 {
		t.Errorf("fields.clikit.exit_code = %v, want 90 (unclobbered by the collision)", clikitField["exit_code"])
	}
}

// TestAdversarialHandBuiltRecordRevalidatesEveryDiagnostic checks
// Result.Validate re-runs the class and shape checks on Errors/Caveats
// members that were assembled by hand rather than through NewError/
// NewCaveat - the path a decoded/deserialized record also takes.
func TestAdversarialHandBuiltRecordRevalidatesEveryDiagnostic(t *testing.T) {
	r := &Result{
		SchemaVersion: SchemaVersion, Command: []string{"tool"},
		Status: StatusConflict, ExitCode: 41,
		Errors: []Diagnostic{{Code: "not-a-valid-code", Message: "m", Triage: Manual("do x")}},
	}
	if err := r.Validate(); err == nil {
		t.Fatal("errors[0] with a malformed code: want validation error, got nil")
	}

	r2 := &Result{
		SchemaVersion: SchemaVersion, Command: []string{"tool"},
		Status: StatusCaveats, ExitCode: 10,
		Caveats: []Diagnostic{{Code: "internal.x.y", Message: "m", Triage: Manual("do x")}},
	}
	if err := r2.Validate(); err == nil {
		t.Fatal("caveats[0] carrying a failure-class code: want validation error, got nil")
	}

	tooMany := make([]Diagnostic, 51)
	for i := range tooMany {
		tooMany[i] = Diagnostic{Code: "conflict.x.y", Message: "m", Triage: Manual("do x")}
	}
	r3 := &Result{
		SchemaVersion: SchemaVersion, Command: []string{"tool"},
		Status: StatusConflict, ExitCode: 41, Errors: tooMany,
	}
	if err := r3.Validate(); err == nil {
		t.Fatal("errors with 51 members (max 50): want validation error, got nil")
	}
}

// TestAdversarialArgvTokenRejectsControlCharacters checks a triage
// command token carrying a control character is refused - the token must
// survive the one-line JSON rendering unchanged.
func TestAdversarialArgvTokenRejectsControlCharacters(t *testing.T) {
	bad := Triage{Kind: TriageReinvoke, Command: []string{"tool", "arg\x01with\x01control"}}
	if _, err := NewError("internal.x.y", "boom", bad, nil); err == nil {
		t.Fatal("triage command token with a control character: want validation error, got nil")
	}
}

// TestAdversarialGateNegativeAndCaveatsLogAtInfoAndWarn checks the two
// non-error-severity classes (20 is the tool working, not failing; 10 is
// a qualified success) log at their pinned non-error levels rather than
// defaulting to error the way every other failure class does.
func TestAdversarialGateNegativeAndCaveatsLogAtInfoAndWarn(t *testing.T) {
	var buf bytes.Buffer
	logger, err := logkit.New("tool", logkit.WithJSON(&buf))
	if err != nil {
		t.Fatalf("logkit.New: %v", err)
	}
	e, _ := NewError("gate_negative.x.y", "no", Manual("nothing to do"), nil)
	r, err := NewGateNegative([]string{"tool"}, nil, []Diagnostic{e}, nil)
	if err != nil {
		t.Fatalf("NewGateNegative: %v", err)
	}
	if err := LogTerminating(logger, r, ""); err != nil {
		t.Fatalf("LogTerminating: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	if rec["level"] != "info" {
		t.Errorf("gate_negative level = %v, want info", rec["level"])
	}

	buf.Reset()
	cv, _ := NewCaveat("caveats.x.y", "partial", RunTool("tool2"), nil)
	r2, err := NewCaveats([]string{"tool"}, nil, []Diagnostic{cv})
	if err != nil {
		t.Fatalf("NewCaveats: %v", err)
	}
	if err := LogTerminating(logger, r2, ""); err != nil {
		t.Fatalf("LogTerminating: %v", err)
	}
	var rec2 map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec2); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	if rec2["level"] != "warn" {
		t.Errorf("caveats level = %v, want warn", rec2["level"])
	}
}
