package docmirror

import (
	"fmt"
	"io/fs"
	"os"
	"text/template"

	"github.com/johnrichter/claude-shared-tooling/go/fsx"
	"github.com/johnrichter/claude-shared-tooling/go/jsondoc"
)

// WritePair writes doc's canonical JSON to jsonPath and its rendered Markdown mirror (via
// Render with tmpl) to mdPath, so no code path can write one file without the other. Both
// writes go through fsx.WriteAtomic (temp file + fsync + rename), and if the mirror render or
// its write fails, WritePair removes the JSON file it just wrote rather than leaving a
// canonical doc on disk with a stale or missing mirror.
func WritePair(jsonPath, mdPath string, doc any, tmpl *template.Template, perm fs.FileMode) error {
	canon, err := jsondoc.Canonicalize(doc)
	if err != nil {
		return fmt.Errorf("docmirror: canonicalize: %w", err)
	}
	canon = append(canon, '\n')

	mirror, err := Render(doc, tmpl)
	if err != nil {
		return err
	}

	if err := fsx.WriteAtomic(jsonPath, canon, perm); err != nil {
		return err
	}
	if err := fsx.WriteAtomic(mdPath, []byte(mirror), perm); err != nil {
		_ = os.Remove(jsonPath)
		return err
	}
	return nil
}
