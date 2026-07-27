// Package docmirror renders a canonical JSON document to a human-readable Markdown mirror
// through a text/template, and pairs the two files so neither is ever written without the
// other. The canonical JSON document stays the single source of truth; the Markdown mirror
// is a generated, non-hand-editable view of it.
package docmirror
