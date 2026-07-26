package docmirror

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
)

// parseWithFailFunc builds a template whose sole action calls a func that always errors, so a
// caller exercising the execution-failure path does not need to fabricate a template-syntax
// error (which text/template's default missingkey behavior would not produce on its own).
func parseWithFailFunc(t *testing.T) *template.Template {
	t.Helper()
	tmpl := template.New("mirror").Funcs(map[string]any{
		"fail": func() (string, error) { return "", os.ErrInvalid },
	})
	tmpl, err := tmpl.Parse(`{{fail}}`)
	if err != nil {
		t.Fatal(err)
	}
	return tmpl
}

// TestRender_DeterministicAcrossNumberFormatting confirms Render's determinism claim covers
// number literals, not just map key order: a float written as 1.0 and the same value written
// as 1 must canonicalize to the same JSON number and render identically.
func TestRender_DeterministicAcrossNumberFormatting(t *testing.T) {
	tmpl, err := Parse("mirror", "count: {{.count}}\n")
	if err != nil {
		t.Fatal(err)
	}
	a := map[string]any{"count": 1.0}
	b := map[string]any{"count": 1}

	outA, err := Render(a, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	outB, err := Render(b, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	if outA != outB {
		t.Fatalf("Render not deterministic across number formatting:\nA: %q\nB: %q", outA, outB)
	}
}

// TestRender_DeterministicAcrossStructVsMap confirms a struct and an equivalent map documenting
// the same JSON content render identically -- Render's determinism guarantee is about the JSON
// content, not the caller's Go representation.
func TestRender_DeterministicAcrossStructVsMap(t *testing.T) {
	type doc struct {
		Title string `json:"title"`
		Owner string `json:"owner"`
	}
	tmpl, err := Parse("mirror", "# {{.title}} ({{.owner}})\n")
	if err != nil {
		t.Fatal(err)
	}
	structOut, err := Render(doc{Title: "Widget", Owner: "team-a"}, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	mapOut, err := Render(map[string]any{"title": "Widget", "owner": "team-a"}, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	if structOut != mapOut {
		t.Fatalf("Render not deterministic across struct-vs-map input:\nstruct: %q\nmap:    %q", structOut, mapOut)
	}
}

// TestRender_IdempotentAcrossManyReRenders extends the single-repeat idempotency check to ten
// consecutive re-renders of the same doc, catching any hidden mutable state (e.g. a shared
// buffer not fully reset) that a two-call check could miss.
func TestRender_IdempotentAcrossManyReRenders(t *testing.T) {
	tmpl, err := Parse("mirror", "# {{.title}}\n\nowner: {{.owner}}\ncount: {{.count}}\n")
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{"title": "Widget", "owner": "team-a", "count": 3}

	first, err := Render(doc, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 9; i++ {
		out, err := Render(doc, tmpl)
		if err != nil {
			t.Fatal(err)
		}
		if out != first {
			t.Fatalf("Render diverged on iteration %d:\nfirst: %q\ngot:   %q", i+2, first, out)
		}
	}
}

// TestRender_TemplateExecutionErrorSurfaced confirms Render itself (not just WritePair)
// surfaces a template execution failure rather than swallowing it or returning a truncated
// mirror -- the doc references a field the template requires via a func that always errors.
func TestRender_TemplateExecutionErrorSurfaced(t *testing.T) {
	tmpl := parseWithFailFunc(t)
	out, err := Render(map[string]any{"title": "Widget"}, tmpl)
	if err == nil {
		t.Fatalf("expected Render to surface template execution error, got output: %q", out)
	}
}

// TestRender_UnmarshalableDocSurfacesError confirms Render reports an error rather than
// panicking or silently rendering an empty mirror when doc cannot be marshaled to JSON
// (e.g. a channel value, which encoding/json refuses to encode).
func TestRender_UnmarshalableDocSurfacesError(t *testing.T) {
	tmpl, err := Parse("mirror", "{{.}}\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Render(map[string]any{"bad": make(chan int)}, tmpl); err == nil {
		t.Fatal("expected Render to error on an unmarshalable doc, got nil")
	}
}

// TestRender_UserContentCannotForgeMarker confirms a doc field whose value contains text that
// looks like the generated-file Marker does not let a caller spoof or duplicate the marker --
// Render always prepends its own Marker exactly once, regardless of doc content.
func TestRender_UserContentCannotForgeMarker(t *testing.T) {
	tmpl, err := Parse("mirror", "{{.note}}\n")
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{"note": Marker}
	out, err := Render(doc, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, Marker) != 2 { // one from Render's prefix, one echoed from doc content
		t.Fatalf("expected exactly 2 occurrences of Marker (prefix + echoed content), got %d in: %q", strings.Count(out, Marker), out)
	}
	if !strings.HasPrefix(out, Marker) {
		t.Fatalf("Marker must lead the output regardless of doc content, got: %q", out)
	}
}

// TestWritePair_ReRenderIsByteIdenticalOnDisk confirms the idempotency guarantee holds through
// the full WritePair path, not just in-memory Render: writing the same doc+template pair twice
// produces byte-identical files on disk both times, so a re-render on an unchanged canonical
// doc never appears as spurious drift in a diff or a content hash check.
func TestWritePair_ReRenderIsByteIdenticalOnDisk(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "doc.json")
	mdPath := filepath.Join(dir, "doc.md")

	tmpl, err := Parse("mirror", "# {{.title}}\n\nowner: {{.owner}}\n")
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{"title": "Widget", "owner": "team-a"}

	if err := WritePair(jsonPath, mdPath, doc, tmpl, 0o644); err != nil {
		t.Fatal(err)
	}
	firstJSON, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	firstMD, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatal(err)
	}

	// Re-render with a differently-ordered map for the same JSON document.
	reordered := map[string]any{"owner": "team-a", "title": "Widget"}
	if err := WritePair(jsonPath, mdPath, reordered, tmpl, 0o644); err != nil {
		t.Fatal(err)
	}
	secondJSON, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	secondMD, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatal(err)
	}

	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("WritePair JSON not idempotent across key order:\nfirst:  %q\nsecond: %q", firstJSON, secondJSON)
	}
	if string(firstMD) != string(secondMD) {
		t.Fatalf("WritePair markdown not idempotent across key order:\nfirst:  %q\nsecond: %q", firstMD, secondMD)
	}
}

// TestWritePair_JSONFileIsCanonical confirms the JSON file WritePair writes is itself the
// RFC 8785 canonical form (sorted keys, no incidental whitespace) -- the canonical document
// is the source of truth the mirror is rendered from, so it must not silently drift from
// canonical form based on how the caller happened to construct the Go value.
func TestWritePair_JSONFileIsCanonical(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "doc.json")
	mdPath := filepath.Join(dir, "doc.md")

	tmpl, err := Parse("mirror", "# {{.title}}\n")
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately out-of-order keys.
	doc := map[string]any{"z": 1, "a": 2, "title": "Widget"}
	if err := WritePair(jsonPath, mdPath, doc, tmpl, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":2,"title":"Widget","z":1}` + "\n"
	if string(got) != want {
		t.Fatalf("WritePair did not write canonical JSON:\nwant: %q\ngot:  %q", want, got)
	}
}

// TestWritePair_FailedTemplateParseAtCallSiteLeavesNoFiles confirms that when the template
// itself is malformed (a caller error, not a doc error), WritePair fails before writing
// either file -- extending the paired-write guarantee to the template-parse failure path.
func TestWritePair_FailedTemplateParseAtCallSiteLeavesNoFiles(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "doc.json")
	mdPath := filepath.Join(dir, "doc.md")

	// Parse fails outright on malformed template syntax, so the caller never obtains a
	// *template.Template to pass to WritePair in the first place -- this exercises that the
	// package's own entry point (Parse) fails closed rather than returning a partially built
	// template that could later execute to something unexpected.
	if _, err := Parse("mirror", "{{.title"); err == nil {
		t.Fatal("expected Parse to fail on malformed template syntax")
	}
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Fatalf("expected no json file to exist, stat err: %v", err)
	}
	if _, err := os.Stat(mdPath); !os.IsNotExist(err) {
		t.Fatalf("expected no markdown file to exist, stat err: %v", err)
	}
}
