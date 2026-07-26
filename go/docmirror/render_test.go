package docmirror

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
)

func TestRender_DeterministicAcrossKeyOrder(t *testing.T) {
	tmpl, err := Parse("mirror", "# {{.title}}\n\nowner: {{.owner}}\n")
	if err != nil {
		t.Fatal(err)
	}

	a := map[string]any{"title": "Widget", "owner": "team-a"}
	b := map[string]any{"owner": "team-a", "title": "Widget"}

	outA, err := Render(a, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	outB, err := Render(b, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	if outA != outB {
		t.Fatalf("Render not deterministic across map key order:\nA: %q\nB: %q", outA, outB)
	}
}

func TestRender_IdempotentOnRepeatedCalls(t *testing.T) {
	tmpl, err := Parse("mirror", "# {{.title}}\n")
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{"title": "Widget"}

	first, err := Render(doc, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Render(doc, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("Render not idempotent:\nfirst:  %q\nsecond: %q", first, second)
	}
}

func TestRender_MarksOutputGenerated(t *testing.T) {
	tmpl, err := Parse("mirror", "# {{.title}}\n")
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render(map[string]any{"title": "Widget"}, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, Marker) {
		t.Fatalf("rendered mirror missing generated marker, got: %q", out)
	}
}

func TestWritePair_WritesBothFilesTogether(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "doc.json")
	mdPath := filepath.Join(dir, "doc.md")

	tmpl, err := Parse("mirror", "# {{.title}}\n")
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{"title": "Widget"}

	if err := WritePair(jsonPath, mdPath, doc, tmpl, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(jsonPath); err != nil {
		t.Fatalf("json file missing: %v", err)
	}
	mdBytes, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("markdown mirror missing: %v", err)
	}
	if !strings.HasPrefix(string(mdBytes), Marker) {
		t.Fatalf("mirror written by WritePair missing generated marker")
	}
}

func TestWritePair_FailedRenderLeavesNoOrphanJSON(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "doc.json")
	mdPath := filepath.Join(dir, "doc.md")

	// A template referencing a field absent from the doc executes without error under
	// text/template's default (non-"missingkey=error") option, so force a genuine
	// execution failure via a call to a template func that always errors.
	tmpl := template.New("mirror").Funcs(map[string]any{
		"fail": func() (string, error) { return "", os.ErrInvalid },
	})
	tmpl, err := tmpl.Parse(`{{fail}}`)
	if err != nil {
		t.Fatal(err)
	}

	if err := WritePair(jsonPath, mdPath, map[string]any{"title": "Widget"}, tmpl, 0o644); err == nil {
		t.Fatal("expected WritePair to fail on template execution error")
	}
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Fatalf("expected no orphan json file after failed render, stat err: %v", err)
	}
}
