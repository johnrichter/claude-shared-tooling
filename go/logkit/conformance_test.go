package logkit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// conformanceRoot is the cross-language conformance suite, relative to this
// package's directory (where the test binary runs). Its inputs drive this
// implementation and every other implementation of the standard alike, and its
// goldens are the bytes all of them must produce.
const conformanceRoot = "../../conformance/logkit"

// artifactEnv names an existing directory to write this implementation's
// rendered output into, one subdirectory per language, so the suite runner can
// diff the languages against each other and not only against the goldens. It
// is set by the runner and unset otherwise; the goldens are compared either way.
const artifactEnv = "LOGKIT_CONFORMANCE_OUT"

// conformanceCase is one case file: the event as a call site would hand it
// over, before normalization. levelToken is a raw inbound token rather than a
// Level, so level normalization is part of what the suite pins.
type conformanceCase struct {
	ID             string  `json:"id"`
	Purpose        string  `json:"purpose"`
	LevelToken     string  `json:"level_token"`
	Timestamp      string  `json:"timestamp"`
	Service        string  `json:"service"`
	ServiceVersion string  `json:"service_version"`
	Message        string  `json:"message"`
	Fields         Fields  `json:"fields"`
	Error          *Error  `json:"error"`
	Caller         *Caller `json:"caller"`
}

// loadConformanceCases reads every case file in the suite's inputs directory,
// in file-name order. An absent or empty directory fails the test: the suite
// is a checked-in corpus and is never generated on the fly.
func loadConformanceCases(t *testing.T) []conformanceCase {
	t.Helper()
	dir := filepath.Join(conformanceRoot, "inputs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read conformance inputs %s: %v", dir, err)
	}

	var cases []conformanceCase
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read conformance case %s: %v", name, err)
		}
		// Unknown keys are an error rather than a shrug: a key one language
		// reads and another silently drops is exactly the drift this gate exists
		// to catch, and it would otherwise hide inside a passing run.
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		// Numbers keep their literal form until the canonicalizer decides it,
		// so a case can pin an exact numeric rendering instead of whatever a
		// float64 round trip happens to reproduce.
		decoder.UseNumber()
		var c conformanceCase
		if err := decoder.Decode(&c); err != nil {
			t.Fatalf("decode conformance case %s: %v", name, err)
		}
		if want := strings.TrimSuffix(name, ".json"); c.ID != want {
			t.Fatalf("conformance case %s declares id %q, want %q", name, c.ID, want)
		}
		cases = append(cases, c)
	}

	if len(cases) == 0 {
		t.Fatalf("no case files under %s: the shared input set is missing", dir)
	}
	return cases
}

// record normalizes one case into the Record both implementations must agree
// on, byte for byte.
func (c conformanceCase) record(t *testing.T) *Record {
	t.Helper()
	level, nativeLevel, err := NormalizeLevel(c.LevelToken, "conformance case "+c.ID)
	if err != nil {
		t.Fatalf("case %s: %v", c.ID, err)
	}

	fields := c.Fields
	if nativeLevel != "" {
		if _, taken := fields["native_level"]; taken {
			t.Fatalf("case %s sets the reserved native_level key itself", c.ID)
		}
		fields = mergeFields(fields, Fields{"native_level": nativeLevel})
	}
	if len(fields) == 0 {
		fields = nil
	}

	rec := &Record{
		Caller:         c.Caller,
		Error:          c.Error,
		Fields:         fields,
		Level:          level,
		Message:        c.Message,
		SchemaVersion:  SchemaVersion,
		Service:        c.Service,
		ServiceVersion: c.ServiceVersion,
		Timestamp:      c.Timestamp,
	}
	if err := rec.Validate(); err != nil {
		t.Fatalf("case %s is not a valid record: %v", c.ID, err)
	}
	return rec
}

// TestConformanceSuiteMatchesGolden renders the whole shared input set and
// compares both renderings against the suite's goldens byte for byte. The
// rendered output is written out before the comparison so the runner can
// report a cross-language difference even on a run that fails here.
func TestConformanceSuiteMatchesGolden(t *testing.T) {
	var ids, records, human strings.Builder
	for _, c := range loadConformanceCases(t) {
		rec := c.record(t)
		canonical, err := canonicalize(rec)
		if err != nil {
			t.Fatalf("case %s: canonicalize: %v", c.ID, err)
		}
		line, err := RenderHuman(rec)
		if err != nil {
			t.Fatalf("case %s: render human: %v", c.ID, err)
		}
		fmt.Fprintf(&ids, "%s\n", c.ID)
		fmt.Fprintf(&records, "%s\n", canonical)
		fmt.Fprintf(&human, "%s\n", line)
	}

	writeConformanceArtifacts(t, map[string]string{
		"cases.txt":         ids.String(),
		"records.jsonl":     records.String(),
		"records.human.txt": human.String(),
	})

	assertMatchesGolden(t, "records.jsonl", records.String())
	assertMatchesGolden(t, "records.human.txt", human.String())
}

// assertMatchesGolden compares got against the named golden file. A missing
// golden fails: the suite reads its goldens and never writes them, so an
// absent one is a gap in the corpus, not something to fill in silently.
func assertMatchesGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join(conformanceRoot, "golden", name)
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (goldens are recorded deliberately, never by a test run)", path, err)
	}
	if got == string(want) {
		return
	}
	gotLines, wantLines := strings.Split(got, "\n"), strings.Split(string(want), "\n")
	for i := range max(len(gotLines), len(wantLines)) {
		gotLine, wantLine := lineAt(gotLines, i), lineAt(wantLines, i)
		if gotLine != wantLine {
			t.Fatalf("%s line %d diverges from the golden:\n got %q\nwant %q", name, i+1, gotLine, wantLine)
		}
	}
}

func lineAt(lines []string, i int) string {
	if i >= len(lines) {
		return "<no line>"
	}
	return lines[i]
}

// writeConformanceArtifacts drops this implementation's rendered output into
// the runner's directory, under a per-language subdirectory. A directory the
// runner named but did not create is an error rather than a skip, so a
// misconfigured run never looks like a clean one.
func writeConformanceArtifacts(t *testing.T, artifacts map[string]string) {
	t.Helper()
	root := os.Getenv(artifactEnv)
	if root == "" {
		return
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Fatalf("%s=%q is not an existing directory", artifactEnv, root)
	}
	dir := filepath.Join(root, "go")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create artifact directory %s: %v", dir, err)
	}
	for name, content := range artifacts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write artifact %s: %v", name, err)
		}
	}
}
