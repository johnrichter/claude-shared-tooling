package docmirror

import (
	"bytes"
	"encoding/json"
	"fmt"
	"text/template"

	"github.com/johnrichter/claude-shared-tooling/go/jsondoc"
)

// Marker is prefixed to every rendered mirror, flagging it as generated from a canonical
// JSON document rather than hand-authored. Its presence is what a reviewer or a lint check
// uses to catch a mirror that was hand-edited instead of regenerated.
const Marker = "<!-- GENERATED FILE. DO NOT EDIT BY HAND. Rendered from the canonical JSON document; edit that document and re-render. -->"

// Parse parses text as a named Markdown-mirror template, for passing to Render or
// WritePair. It is a thin wrapper over text/template.New(name).Parse(text) so a caller
// building a mirror does not need a second import just to construct the template value
// Render expects.
func Parse(name, text string) (*template.Template, error) {
	t, err := template.New(name).Parse(text)
	if err != nil {
		return nil, fmt.Errorf("docmirror: parse template %q: %w", name, err)
	}
	return t, nil
}

// Render renders doc through tmpl and returns the resulting Markdown mirror, prefixed with
// Marker. doc is canonicalized (RFC 8785) before execution, so the output depends only on
// the document's JSON content: key order, incidental whitespace, and how a number was
// originally written never register as a change. That is what makes Render both
// deterministic (same doc + template always renders the same bytes) and idempotent
// (re-rendering an unchanged doc reproduces the existing mirror exactly, so a re-render never
// looks like drift on its own).
func Render(doc any, tmpl *template.Template) (string, error) {
	data, err := decodeCanonical(doc)
	if err != nil {
		return "", err
	}

	var body bytes.Buffer
	if err := tmpl.Execute(&body, data); err != nil {
		return "", fmt.Errorf("docmirror: execute template %q: %w", tmpl.Name(), err)
	}

	var out bytes.Buffer
	out.WriteString(Marker)
	out.WriteString("\n\n")
	out.Write(bytes.TrimRight(body.Bytes(), "\n"))
	out.WriteString("\n")
	return out.String(), nil
}

// decodeCanonical canonicalizes doc and decodes it back into a plain any (maps, slices,
// json.Number, string, bool, nil) for template execution. Routing every doc through this
// same canonicalize-then-decode step, rather than executing the template directly against
// whatever Go value the caller passed, is what gives Render its determinism: two callers who
// pass differently-ordered maps or differently-formatted numbers for the same JSON document
// still execute the template against byte-identical decoded data.
func decodeCanonical(doc any) (any, error) {
	canon, err := jsondoc.Canonicalize(doc)
	if err != nil {
		return nil, fmt.Errorf("docmirror: canonicalize: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(canon))
	dec.UseNumber()
	var data any
	if err := dec.Decode(&data); err != nil {
		return nil, fmt.Errorf("docmirror: decode canonical doc: %w", err)
	}
	return data, nil
}
