package logkit

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goldenRecords parses schemas/logkit/examples/golden-records.jsonl into
// Records, one per non-empty line, in file order.
func goldenRecords(t *testing.T) []*Record {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "logkit", "examples", "golden-records.jsonl"))
	if err != nil {
		t.Fatalf("read golden jsonl: %v", err)
	}
	var recs []*Record
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if line == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("unmarshal golden line %q: %v", line, err)
		}
		recs = append(recs, &rec)
	}
	return recs
}

// TestGoldenJSONRoundTrip checks that every golden record, decoded then
// re-canonicalized, reproduces the exact golden bytes - the byte-exact target
// every logkit implementation is conformance-gated against.
func TestGoldenJSONRoundTrip(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "logkit", "examples", "golden-records.jsonl"))
	if err != nil {
		t.Fatalf("read golden jsonl: %v", err)
	}
	goldenLines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")

	for i, rec := range goldenRecords(t) {
		if err := rec.Validate(); err != nil {
			t.Fatalf("record %d fails Validate: %v", i+1, err)
		}
		canon, err := canonicalize(rec)
		if err != nil {
			t.Fatalf("record %d canonicalize: %v", i+1, err)
		}
		if string(canon) != goldenLines[i] {
			t.Errorf("record %d:\n got %s\nwant %s", i+1, canon, goldenLines[i])
		}
	}
}

// TestGoldenHumanRendering checks that every golden record renders to the
// matching line(s) of golden-records.human.txt.
func TestGoldenHumanRendering(t *testing.T) {
	wantRaw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "logkit", "examples", "golden-records.human.txt"))
	if err != nil {
		t.Fatalf("read golden human: %v", err)
	}

	var got bytes.Buffer
	for _, rec := range goldenRecords(t) {
		line, err := RenderHuman(rec)
		if err != nil {
			t.Fatalf("RenderHuman: %v", err)
		}
		got.WriteString(line)
		got.WriteByte('\n')
	}

	if got.String() != string(wantRaw) {
		t.Errorf("human rendering mismatch:\n got %q\nwant %q", got.String(), string(wantRaw))
	}
}

// TestAllLevelsKnown checks the closed level set and that Known rejects
// case, whitespace and alias variants rather than folding them.
func TestAllLevelsKnown(t *testing.T) {
	for _, l := range []Level{LevelDebug, LevelInfo, LevelWarn, LevelError, LevelFatal} {
		if !l.Known() {
			t.Errorf("%q should be Known", l)
		}
	}
	for _, tok := range []string{"INFO", " info", "warning", "trace", "panic", "", "off"} {
		if Level(tok).Known() {
			t.Errorf("%q should not be Known", tok)
		}
	}
}

// TestNormalizeLevel exercises the equivalence and lossy-alias branches of
// the inbound level map.
func TestNormalizeLevel(t *testing.T) {
	cases := []struct {
		token           string
		wantLevel       Level
		wantNativeLevel string
	}{
		{"INFO", LevelInfo, ""},
		{" warning ", LevelWarn, ""},
		{"critical", LevelFatal, ""},
		{"trace", LevelDebug, "trace"},
		{"panic", LevelFatal, "panic"},
	}
	for _, c := range cases {
		level, native, err := NormalizeLevel(c.token, "test")
		if err != nil {
			t.Errorf("NormalizeLevel(%q): %v", c.token, err)
			continue
		}
		if level != c.wantLevel || native != c.wantNativeLevel {
			t.Errorf("NormalizeLevel(%q) = (%q, %q), want (%q, %q)", c.token, level, native, c.wantLevel, c.wantNativeLevel)
		}
	}
	if _, _, err := NormalizeLevel("bogus", "test"); err == nil {
		t.Error("NormalizeLevel(bogus) should fail, not default")
	}
}

// TestLoggerDualOutput checks that one Emit call produces both a valid
// canonical JSON line and a human line, off the same record, and that the
// threshold gate suppresses a below-threshold level on both sinks.
func TestLoggerDualOutput(t *testing.T) {
	var jsonBuf, humanBuf bytes.Buffer
	l, err := New("test-service",
		WithJSON(&jsonBuf),
		WithHuman(&humanBuf),
		WithThreshold(LevelDebug),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := l.Info("hello there", Fields{"count": 3}); err != nil {
		t.Fatalf("Info: %v", err)
	}

	var rec Record
	if err := json.Unmarshal(bytes.TrimRight(jsonBuf.Bytes(), "\n"), &rec); err != nil {
		t.Fatalf("emitted JSON does not parse: %v\n%s", err, jsonBuf.String())
	}
	if rec.Level != LevelInfo || rec.Message != "hello there" || rec.Service != "test-service" {
		t.Errorf("unexpected record: %+v", rec)
	}
	if !strings.Contains(humanBuf.String(), "hello there") || !strings.Contains(humanBuf.String(), "count=3") {
		t.Errorf("unexpected human line: %q", humanBuf.String())
	}

	jsonBuf.Reset()
	humanBuf.Reset()
	l2, err := New("test-service", WithJSON(&jsonBuf), WithHuman(&humanBuf), WithThreshold(LevelWarn))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := l2.Info("suppressed", nil); err != nil {
		t.Fatalf("Info: %v", err)
	}
	if jsonBuf.Len() != 0 || humanBuf.Len() != 0 {
		t.Errorf("below-threshold record was emitted: json=%q human=%q", jsonBuf.String(), humanBuf.String())
	}
}

// TestFatalHasNoSideEffect checks that emitting at fatal returns normally
// instead of exiting the process.
func TestFatalHasNoSideEffect(t *testing.T) {
	var jsonBuf bytes.Buffer
	l, err := New("test-service", WithJSON(&jsonBuf))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := l.Fatal(nil, "process cannot continue", nil); err != nil {
		t.Fatalf("Fatal: %v", err)
	}
	if !bytes.Contains(jsonBuf.Bytes(), []byte(`"level":"fatal"`)) {
		t.Errorf("fatal record missing level: %s", jsonBuf.String())
	}
}
