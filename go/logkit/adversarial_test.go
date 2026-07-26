package logkit

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestEmitRejectsUnknownLevel: the emit path must refuse a hand-built Level
// outside the closed set rather than passing it through (spec: "the emit path
// asserts [Known] rather than trusting the type alone").
func TestEmitRejectsUnknownLevel(t *testing.T) {
	var jsonBuf bytes.Buffer
	l, err := New("svc", WithJSON(&jsonBuf))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := l.Emit(Level("bogus"), nil, "x", nil); err == nil {
		t.Error("Emit with unknown level should fail, not emit")
	}
	if jsonBuf.Len() != 0 {
		t.Errorf("unknown-level emit wrote bytes: %q", jsonBuf.String())
	}
}

// TestFatalNeverFiltered: "fatal cannot be filtered out - no threshold ranks
// above it," even with an above-fatal-severity threshold request.
func TestFatalNeverFilteredByThreshold(t *testing.T) {
	var jsonBuf bytes.Buffer
	// There is no level above fatal to construct via the public API, so this
	// pins the boundary case: threshold == fatal itself must still emit fatal.
	l, err := New("svc", WithJSON(&jsonBuf), WithThreshold(LevelFatal))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := l.Fatal(nil, "down", nil); err != nil {
		t.Fatalf("Fatal: %v", err)
	}
	if jsonBuf.Len() == 0 {
		t.Error("fatal record suppressed by threshold==fatal")
	}
}

// TestFieldsCollisionWithRootNameRejected: a fields key equal to a root field
// name must fail Validate/Emit, never silently shadow the root field.
func TestFieldsCollisionWithRootNameRejected(t *testing.T) {
	var jsonBuf bytes.Buffer
	l, err := New("svc", WithJSON(&jsonBuf))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, key := range []string{"service", "timestamp", "level", "message", "schema_version", "caller", "error", "fields", "service_version"} {
		jsonBuf.Reset()
		if err := l.Info("msg", Fields{key: "x"}); err == nil {
			t.Errorf("fields key %q should collide and fail", key)
		}
		if jsonBuf.Len() != 0 {
			t.Errorf("colliding fields key %q still emitted: %q", key, jsonBuf.String())
		}
	}
}

// TestInvalidServiceRejectedAtConstruction: New must refuse a service string
// outside the schema's service pattern rather than emitting a non-conformant
// record later.
func TestInvalidServiceRejectedAtConstruction(t *testing.T) {
	cases := []string{"", "UpperCase", "-leading-dash", "has space", strings.Repeat("a", 65)}
	for _, svc := range cases {
		if _, err := New(svc); err == nil {
			t.Errorf("New(%q) should fail service pattern", svc)
		}
	}
}

// TestMessageControlCharacterRejected: message must be a single line per
// $defs/line - a newline or other control character fails Validate.
func TestMessageControlCharacterRejected(t *testing.T) {
	var jsonBuf bytes.Buffer
	l, err := New("svc", WithJSON(&jsonBuf))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := l.Info("line one\nline two", nil); err == nil {
		t.Error("multi-line message should fail Validate")
	}
	if err := l.Info("", nil); err == nil {
		t.Error("empty message should fail Validate (minLength 1)")
	}
	if jsonBuf.Len() != 0 {
		t.Errorf("invalid-message record still emitted: %q", jsonBuf.String())
	}
}

// TestErrorFieldFromGoError: Error() populates error.message (first line,
// truncated) and error.kind (concrete Go type), independent of level.
func TestErrorFieldFromGoError(t *testing.T) {
	var jsonBuf bytes.Buffer
	l, err := New("svc", WithJSON(&jsonBuf))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	longMsg := strings.Repeat("x", 5000) + "\nsecond line ignored"
	if err := l.Warn("degraded", nil); err != nil { // warn may carry no error
		t.Fatalf("Warn: %v", err)
	}
	jsonBuf.Reset()
	werr := errors.New(longMsg)
	if e := l.Warn("degraded but with error", nil); e != nil {
		t.Fatalf("Warn: %v", e)
	}
	_ = werr

	jsonBuf.Reset()
	if e := l.Error(errors.New("boom\nsecond"), "op failed", nil); e != nil {
		t.Fatalf("Error: %v", e)
	}
	var rec Record
	if err := json.Unmarshal(bytes.TrimRight(jsonBuf.Bytes(), "\n"), &rec); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, jsonBuf.String())
	}
	if rec.Error == nil || rec.Error.Message != "boom" {
		t.Errorf("error.message not truncated to first line: %+v", rec.Error)
	}
	if rec.Error.Kind != "*errors.errorString" {
		t.Errorf("error.kind = %q, want *errors.errorString", rec.Error.Kind)
	}
}

// TestErrorFieldMessageTruncatedTo4096: error.message must respect the
// schema's 4096-char line cap.
func TestErrorFieldMessageTruncatedTo4096(t *testing.T) {
	var jsonBuf bytes.Buffer
	l, err := New("svc", WithJSON(&jsonBuf))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	huge := strings.Repeat("a", 5000)
	if err := l.Error(errors.New(huge), "op failed", nil); err != nil {
		t.Fatalf("Error: %v", err)
	}
	var rec Record
	if err := json.Unmarshal(bytes.TrimRight(jsonBuf.Bytes(), "\n"), &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rec.Error.Message) != 4096 {
		t.Errorf("error.message length = %d, want 4096", len(rec.Error.Message))
	}
}

// TestWarnErrorDebugCoverAllFiveLevels: exercise all five level methods (the
// existing suite only directly calls Info and Fatal) and check severity/level
// on the wire for each, including the debug/warn/error paths.
func TestWarnErrorDebugCoverAllFiveLevels(t *testing.T) {
	var jsonBuf bytes.Buffer
	l, err := New("svc", WithJSON(&jsonBuf), WithThreshold(LevelDebug))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	calls := []struct {
		name string
		call func() error
		want Level
	}{
		{"Debug", func() error { return l.Debug("d", nil) }, LevelDebug},
		{"Info", func() error { return l.Info("i", nil) }, LevelInfo},
		{"Warn", func() error { return l.Warn("w", nil) }, LevelWarn},
		{"Error", func() error { return l.Error(nil, "e", nil) }, LevelError},
		{"Fatal", func() error { return l.Fatal(nil, "f", nil) }, LevelFatal},
	}
	for _, c := range calls {
		jsonBuf.Reset()
		if err := c.call(); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var rec Record
		if err := json.Unmarshal(bytes.TrimRight(jsonBuf.Bytes(), "\n"), &rec); err != nil {
			t.Fatalf("%s: unmarshal: %v\n%s", c.name, err, jsonBuf.String())
		}
		if rec.Level != c.want {
			t.Errorf("%s: level = %q, want %q", c.name, rec.Level, c.want)
		}
	}
}

// TestWithMergesAndCallSiteWins: With() carries context fields into every
// subsequent record, and a call-site key of the same name wins.
func TestWithMergesAndCallSiteWins(t *testing.T) {
	var jsonBuf bytes.Buffer
	base, err := New("svc", WithJSON(&jsonBuf))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	child := base.With(Fields{"request_id": "abc", "shared": "base"})
	if err := child.Info("done", Fields{"shared": "override"}); err != nil {
		t.Fatalf("Info: %v", err)
	}
	var rec Record
	if err := json.Unmarshal(bytes.TrimRight(jsonBuf.Bytes(), "\n"), &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rec.Fields["request_id"] != "abc" {
		t.Errorf("With field missing: %+v", rec.Fields)
	}
	if rec.Fields["shared"] != "override" {
		t.Errorf("call-site field should win over context field: %+v", rec.Fields)
	}

	// Receiver unchanged: emitting from base afterward must not carry request_id.
	jsonBuf.Reset()
	if err := base.Info("base call", nil); err != nil {
		t.Fatalf("Info: %v", err)
	}
	var baseRec Record
	if err := json.Unmarshal(bytes.TrimRight(jsonBuf.Bytes(), "\n"), &baseRec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := baseRec.Fields["request_id"]; present {
		t.Errorf("With mutated the receiver: base record carries request_id: %+v", baseRec.Fields)
	}
}

// TestCaptureCallerEndToEnd: WithCaller populates caller.file (relative, not
// absolute) and caller.line on an emitted record.
func TestCaptureCallerEndToEnd(t *testing.T) {
	var jsonBuf bytes.Buffer
	l, err := New("svc", WithJSON(&jsonBuf), WithCaller())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := l.Info("here", nil); err != nil {
		t.Fatalf("Info: %v", err)
	}
	var rec Record
	if err := json.Unmarshal(bytes.TrimRight(jsonBuf.Bytes(), "\n"), &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rec.Caller == nil {
		t.Fatal("caller not captured")
	}
	if strings.HasPrefix(rec.Caller.File, "/") {
		t.Errorf("caller.file must not be absolute: %q", rec.Caller.File)
	}
	if !strings.HasSuffix(rec.Caller.File, "adversarial_test.go") {
		t.Errorf("caller.file = %q, want adversarial_test.go", rec.Caller.File)
	}
	if rec.Caller.Line <= 0 {
		t.Errorf("caller.line = %d, want > 0", rec.Caller.Line)
	}
}

// TestTimestampTruncatesNotRounds: a sub-millisecond reading must truncate
// toward zero, never round - rounding can misorder events (spec: "truncation
// cannot" place an event after one that happened later).
func TestTimestampTruncatesNotRounds(t *testing.T) {
	// .999999900 should truncate to .999, not round to 1.000 (which would roll
	// the second, minute, even the day on a boundary instant).
	ts := time.Date(2026, 7, 26, 23, 59, 59, 999999900, time.UTC)
	got := FormatTimestamp(ts)
	want := "2026-07-26T23:59:59.999Z"
	if got != want {
		t.Errorf("FormatTimestamp truncation: got %q, want %q", got, want)
	}
}

// TestTimestampConvertsNonUTCToUTC: an offset-bearing wall-clock reading must
// convert to UTC, never be assumed already UTC.
func TestTimestampConvertsNonUTCToUTC(t *testing.T) {
	loc := time.FixedZone("UTC-5", -5*60*60)
	ts := time.Date(2026, 7, 26, 10, 0, 0, 0, loc)
	got := FormatTimestamp(ts)
	want := "2026-07-26T15:00:00.000Z"
	if got != want {
		t.Errorf("FormatTimestamp UTC conversion: got %q, want %q", got, want)
	}
}

// TestZeroMillisecondAlwaysThreeDigits: an exact-second reading still emits
// ".000", never a shortened form - fixed width is required for lexicographic
// == chronological ordering.
func TestZeroMillisecondAlwaysThreeDigits(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := FormatTimestamp(ts)
	if !strings.HasSuffix(got, ".000Z") || len(got) != 24 {
		t.Errorf("FormatTimestamp zero-ms: got %q, want 24 chars ending .000Z", got)
	}
}

// TestValidateCallerFileAbsoluteRejected: caller.file must never be absolute
// (embeds the build machine's directory layout).
func TestValidateCallerFileAbsoluteRejected(t *testing.T) {
	rec := &Record{
		SchemaVersion: SchemaVersion, Timestamp: FormatTimestamp(time.Now()),
		Level: LevelInfo, Service: "svc", Message: "m",
		Caller: &Caller{File: "/abs/path.go", Line: 1},
	}
	if err := rec.Validate(); err == nil {
		t.Error("absolute caller.file should fail Validate")
	}
}

// TestValidateCallerLineNonPositiveRejected: line is 1-based; 0 or negative
// must fail.
func TestValidateCallerLineNonPositiveRejected(t *testing.T) {
	for _, line := range []int{0, -1} {
		rec := &Record{
			SchemaVersion: SchemaVersion, Timestamp: FormatTimestamp(time.Now()),
			Level: LevelInfo, Service: "svc", Message: "m",
			Caller: &Caller{File: "pkg/file.go", Line: line},
		}
		if err := rec.Validate(); err == nil {
			t.Errorf("caller.line=%d should fail Validate", line)
		}
	}
}

// TestValidateWrongSchemaVersionRejected: schema_version is a const 1; a
// hand-built record declaring another value must fail before it can be
// emitted as a false MAJOR.
func TestValidateWrongSchemaVersionRejected(t *testing.T) {
	rec := &Record{
		SchemaVersion: 2, Timestamp: FormatTimestamp(time.Now()),
		Level: LevelInfo, Service: "svc", Message: "m",
	}
	if err := rec.Validate(); err == nil {
		t.Error("schema_version=2 should fail Validate")
	}
}

// TestJSONHumanIdenticalFieldValuesOffOneCall: when both sinks are enabled,
// their field values must be identical by construction (one record, two
// renderings) - not just individually well-formed.
func TestJSONHumanIdenticalFieldValuesOffOneCall(t *testing.T) {
	var jsonBuf, humanBuf bytes.Buffer
	l, err := New("navigator", WithJSON(&jsonBuf), WithHuman(&humanBuf), WithCaller())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := l.Error(errors.New("boom"), "op failed", Fields{"n": 3, "ok": false}); err != nil {
		t.Fatalf("Error: %v", err)
	}
	var rec Record
	if err := json.Unmarshal(bytes.TrimRight(jsonBuf.Bytes(), "\n"), &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	human := humanBuf.String()
	if !strings.Contains(human, rec.Timestamp) {
		t.Errorf("human line timestamp mismatch: %q vs record %q", human, rec.Timestamp)
	}
	if !strings.Contains(human, "n=3") || !strings.Contains(human, "ok=false") {
		t.Errorf("human line missing field values from same record: %q", human)
	}
	if !strings.Contains(human, `error="boom"`) && !strings.Contains(human, "error=boom") {
		t.Errorf("human line missing error value: %q", human)
	}
}

// TestConcurrentEmitNoRace: Logger must be safe for concurrent use, and no
// two lines interleave (write_atomicity). Run with -race.
func TestConcurrentEmitNoRace(t *testing.T) {
	var jsonBuf syncBuffer
	l, err := New("svc", WithJSON(&jsonBuf))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_ = l.Info("concurrent", Fields{"n": n})
		}(i)
	}
	wg.Wait()

	lines := strings.Split(strings.TrimRight(jsonBuf.String(), "\n"), "\n")
	if len(lines) != 50 {
		t.Fatalf("expected 50 lines, got %d", len(lines))
	}
	for _, line := range lines {
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Errorf("interleaved/corrupt line: %q: %v", line, err)
		}
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestNaNInfinityRejected: NaN/Infinity are not JSON and must be refused at
// the API boundary, never coerced (logkit.contract.json canonical_json.numbers).
//
// BUG FOUND: Emit currently returns nil (no error) and writes a corrupted
// wire record: zerolog's Interface() swallows the json.Marshal failure on the
// "fields" object internally and substitutes the string
// `"marshaling error: json: unsupported value: NaN"` as the "fields" value,
// so the emitted line has fields as a string instead of an object (and thus
// fails log-record.schema.json) while Emit reports success. Repro: call
// Emit/Info with a Fields value containing math.NaN() or +/-Inf; observe
// jsonBuf contains `"fields":"marshaling error: ..."` and err == nil.
func TestNaNInfinityRejected(t *testing.T) {
	var jsonBuf bytes.Buffer
	l, err := New("svc", WithJSON(&jsonBuf))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = l.Info("bad float", Fields{"x": mathNaN()})
	if err == nil {
		t.Error("NaN field value should be rejected, not coerced")
	}
}

func mathNaN() float64 {
	var zero float64
	return zero / zero
}

// TestEmptyFieldsOmittedNotEmptyObject: fields must be entirely absent from
// the wire record when nil/empty, never {}.
func TestEmptyFieldsOmittedNotEmptyObject(t *testing.T) {
	var jsonBuf bytes.Buffer
	l, err := New("svc", WithJSON(&jsonBuf))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := l.Info("no fields", nil); err != nil {
		t.Fatalf("Info: %v", err)
	}
	if strings.Contains(jsonBuf.String(), `"fields"`) {
		t.Errorf("empty fields should be omitted entirely: %q", jsonBuf.String())
	}
}

// TestHumanRenderingNoColorByDefault: color is opt-in (TTY + not NO_COLOR +
// config); a plain io.Writer sink (not a TTY) must render colorless SGR-free
// output by default.
func TestHumanRenderingNoColorByDefault(t *testing.T) {
	var jsonBuf, humanBuf bytes.Buffer
	l, err := New("svc", WithJSON(&jsonBuf), WithHuman(&humanBuf))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := l.Error(nil, "failure", nil); err != nil {
		t.Fatalf("Error: %v", err)
	}
	if strings.Contains(humanBuf.String(), "\x1b[") {
		t.Errorf("non-TTY sink should not receive SGR color codes: %q", humanBuf.String())
	}
}
