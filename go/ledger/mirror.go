package ledger

import (
	"text/template"

	"github.com/johnrichter/claude-shared-tooling/go/docmirror"
)

// mirrorTemplateText renders a ledger's canonical JSON as a criticality-sorted Markdown
// table. It is executed against the decoded canonical document (see docmirror.Render), so
// every field is a plain map value — entries here appear in the document's stored (append)
// order; List is what a reader wanting the ranked order calls.
const mirrorTemplateText = `# Ledger

| ID | Criticality | Impact | Urgency | Statement | Added |
| --- | --- | --- | --- | --- | --- |
{{range .entries}}| {{.id}} | {{.criticality}} | {{.impact}} | {{.urgency}} | {{.statement}} | {{.added}} |
{{end}}`

// mirrorTemplate is parsed once at init and reused by every Open call — a text/template.
// Template is safe for concurrent Execute calls and holds no per-ledger state.
var mirrorTemplate = mustParseMirrorTemplate()

func mustParseMirrorTemplate() *template.Template {
	t, err := docmirror.Parse("ledger-mirror", mirrorTemplateText)
	if err != nil {
		panic(err)
	}
	return t
}
