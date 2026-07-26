package agentcontract

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// agentsDirName is the one filesystem convention this package trusts to mean "this directory's
// listing is a roster": every plugin in this workspace already ships its Claude Code subagents
// this way. A different directory name is not a roster, however many *.md files it holds — that
// keeps roster membership a filesystem fact, not something a lint config nominates either.
const agentsDirName = "agents"

// DiscoverRosters walks root and returns one Roster per directory literally named "agents"
// that contains at least one Markdown file, briefs sorted by name for deterministic output. A
// *.md file in such a directory that fails to parse as a brief is a hard error, not a skip —
// the whole point of the roster gate is that a broken brief cannot pass silently.
func DiscoverRosters(root string) ([]Roster, error) {
	var rosters []Roster

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() || d.Name() != agentsDirName {
			return nil
		}

		entries, err := os.ReadDir(path)
		if err != nil {
			return fmt.Errorf("agentcontract: reading %s: %w", path, err)
		}

		var briefs []*Brief
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
				continue
			}
			briefPath := filepath.Join(path, e.Name())
			data, err := os.ReadFile(briefPath)
			if err != nil {
				return fmt.Errorf("agentcontract: reading %s: %w", briefPath, err)
			}
			brief, err := ParseBrief(briefPath, data)
			if err != nil {
				return err
			}
			briefs = append(briefs, brief)
		}
		if len(briefs) == 0 {
			return nil
		}

		sort.Slice(briefs, func(i, j int) bool {
			return briefs[i].Frontmatter.Name < briefs[j].Frontmatter.Name
		})
		rosters = append(rosters, Roster{Dir: path, Briefs: briefs})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(rosters, func(i, j int) bool { return rosters[i].Dir < rosters[j].Dir })
	return rosters, nil
}
