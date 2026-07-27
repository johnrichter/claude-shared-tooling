package clikit

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
// implementation and every other implementation of the contract alike, and its
// goldens are the bytes all of them must produce.
const conformanceRoot = "../../conformance/clikit"

// artifactEnv names an existing directory to write this implementation's
// rendered output into, one subdirectory per language, so the suite runner can
// diff the languages against each other and not only against the goldens. It
// is set by the runner and unset otherwise; the goldens are compared either way.
const artifactEnv = "CLIKIT_CONFORMANCE_OUT"

// conformanceCase is one case file: the outcome as a command hands it over -
// a status name, a command path and whatever diagnostics it produced - before
// any of it is a record. Building the record from that is what the suite pins.
type conformanceCase struct {
	ID      string           `json:"id"`
	Purpose string           `json:"purpose"`
	Command []string         `json:"command"`
	Status  Status           `json:"status"`
	Data    map[string]any   `json:"data"`
	Errors  []caseDiagnostic `json:"errors"`
	Caveats []caseDiagnostic `json:"caveats"`
}

type caseDiagnostic struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Triage  caseTriage     `json:"triage"`
	Context map[string]any `json:"context"`
}

// caseTriage mirrors the triage shape a case file may declare. AfterSeconds is
// a pointer so an absent member stays distinguishable from a declared 0, which
// the two implementations render differently and which the suite excludes.
type caseTriage struct {
	Kind         TriageKind `json:"kind"`
	Command      []string   `json:"command"`
	Instruction  string     `json:"instruction"`
	AfterSeconds *int       `json:"after_seconds"`
}

// conformanceConstructors is the status -> constructor table the suite builds
// through: every record comes from the same public entry point a CLI uses, so a
// case cannot reach a shape the constructors would refuse.
var conformanceConstructors = map[Status]func(command []string, data map[string]any, errs, caveats []Diagnostic) (*Result, error){
	StatusSuccess: func(command []string, data map[string]any, errs, caveats []Diagnostic) (*Result, error) {
		if len(errs) != 0 || len(caveats) != 0 {
			return nil, fmt.Errorf("success forbids errors and caveats")
		}
		return NewSuccess(command, data)
	},
	StatusCaveats: func(command []string, data map[string]any, errs, caveats []Diagnostic) (*Result, error) {
		if len(errs) != 0 {
			return nil, fmt.Errorf("caveats status forbids errors")
		}
		return NewCaveats(command, data, caveats)
	},
	StatusGateNegative:      NewGateNegative,
	StatusPreconditionUnmet: NewPreconditionUnmet,
	StatusNotFound:          NewNotFound,
	StatusConflict:          NewConflict,
	StatusUsage:             NewUsage,
	StatusTransient:         NewTransient,
	StatusPermission:        NewPermission,
	StatusUnsupported:       NewUnsupported,
	StatusInternal:          NewInternal,
}

// loadConformanceCases reads every case file in the suite's inputs directory,
// in file-name order. An absent or empty directory fails the test: the suite is
// a checked-in corpus and is never generated on the fly.
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

// result builds one case into the Result both implementations must agree on,
// byte for byte, through the constructor its status names.
func (c conformanceCase) result(t *testing.T) *Result {
	t.Helper()
	construct, ok := conformanceConstructors[c.Status]
	if !ok {
		t.Fatalf("case %s declares unknown status %q", c.ID, string(c.Status))
	}

	var errs, caveats []Diagnostic
	for i, d := range c.Errors {
		e, err := NewError(d.Code, d.Message, d.triage(), d.Context)
		if err != nil {
			t.Fatalf("case %s errors[%d]: %v", c.ID, i, err)
		}
		errs = append(errs, e)
	}
	for i, d := range c.Caveats {
		cv, err := NewCaveat(d.Code, d.Message, d.triage(), d.Context)
		if err != nil {
			t.Fatalf("case %s caveats[%d]: %v", c.ID, i, err)
		}
		caveats = append(caveats, cv)
	}

	r, err := construct(c.Command, c.Data, errs, caveats)
	if err != nil {
		t.Fatalf("case %s: %v", c.ID, err)
	}
	return r
}

// triage rebuilds the directive a case declared. An absent after_seconds stays
// 0, which the record omits - the same thing the case file said.
func (d caseDiagnostic) triage() Triage {
	t := Triage{Kind: d.Triage.Kind, Command: d.Triage.Command, Instruction: d.Triage.Instruction}
	if d.Triage.AfterSeconds != nil {
		t.AfterSeconds = *d.Triage.AfterSeconds
	}
	return t
}

// TestConformanceSuiteMatchesGolden renders the whole shared input set - the
// canonical record and the exit code for every case - and compares both against
// the suite's goldens byte for byte. The rendered output is written out before
// the comparison so the runner can report a cross-language difference even on a
// run that fails here.
func TestConformanceSuiteMatchesGolden(t *testing.T) {
	var ids, results, exitCodes strings.Builder
	for _, c := range loadConformanceCases(t) {
		r := c.result(t)
		canonical, err := r.MarshalCanonical()
		if err != nil {
			t.Fatalf("case %s: marshal canonical: %v", c.ID, err)
		}
		fmt.Fprintf(&ids, "%s\n", c.ID)
		fmt.Fprintf(&results, "%s\n", canonical)
		// The integer the process exits with, taken from the record itself -
		// the same value a CLI hands os.Exit immediately after emitting it.
		fmt.Fprintf(&exitCodes, "%s %s %d\n", c.ID, r.Status, r.ExitCode)
	}

	writeConformanceArtifacts(t, map[string]string{
		"cases.txt":      ids.String(),
		"results.jsonl":  results.String(),
		"exit-codes.txt": exitCodes.String(),
	})

	assertMatchesGolden(t, "results.jsonl", results.String())
	assertMatchesGolden(t, "exit-codes.txt", exitCodes.String())
}

// assertMatchesGolden compares got against the named golden file. A missing
// golden fails: the suite reads its goldens and never writes them, so an absent
// one is a gap in the corpus, not something to fill in silently.
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

// writeConformanceArtifacts drops this implementation's rendered output into the
// runner's directory, under a per-language subdirectory. A directory the runner
// named but did not create is an error rather than a skip, so a misconfigured
// run never looks like a clean one.
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
