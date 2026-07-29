package plugin_conform

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
)

// ruleGlobs are the file globs a plugin's own rules ship under, per the rules/*.md convention
// used for workspace-level .claude/rules/, authored at the plugin's own root instead.
var ruleGlobs = []string{"rules/*.md", "rules/**/*.md"}

// ruleFrontmatter is a rule file's own declared trigger: the paths glob(s) it fires on.
type ruleFrontmatter struct {
	Paths []string `yaml:"paths"`
}

// CheckRuleGlobs reads every rules/*.md file under pluginDir and requires each declared paths:
// glob to resolve to at least one real file inside the plugin's own tree. A glob that matches
// nothing here is either stale -- the file it once governed moved or was renamed -- or was
// authored against a path that was never part of this plugin to begin with; either way the rule
// can never fire as shipped. A rule with no paths: entry (unconditional) has nothing to check.
func CheckRuleGlobs(pluginDir string) ([]clikit.Diagnostic, error) {
	relPaths, err := expandGlobs(pluginDir, ruleGlobs)
	if err != nil {
		return nil, fmt.Errorf("plugin-conform: expand rule globs: %w", err)
	}

	var diags []clikit.Diagnostic
	for _, rel := range relPaths {
		data, err := os.ReadFile(filepath.Join(pluginDir, rel))
		if err != nil {
			return nil, fmt.Errorf("plugin-conform: read rule %q: %w", rel, err)
		}

		var fm ruleFrontmatter
		found, parseErr := parseFrontmatter(rel, data, &fm)
		if parseErr != nil {
			d, buildErr := clikit.NewError(
				"gate_negative.plugin_conform.rule_frontmatter_invalid",
				oneLine(fmt.Sprintf("%s: %v", rel, parseErr)),
				clikit.Manual("fix "+rel+"'s leading YAML frontmatter block so it parses"),
				map[string]any{"path": rel},
			)
			if buildErr != nil {
				return nil, buildErr
			}
			diags = append(diags, d)
			continue
		}
		if !found {
			continue // no frontmatter block at all -- an unconditional rule, nothing to check
		}

		for _, glob := range fm.Paths {
			matches, err := expandGlobs(pluginDir, []string{glob})
			if err != nil {
				return nil, fmt.Errorf("plugin-conform: expand %s's declared glob %q: %w", rel, glob, err)
			}
			if len(matches) == 0 {
				d, buildErr := clikit.NewError(
					"gate_negative.plugin_conform.rule_glob_no_match",
					oneLine(fmt.Sprintf("%s declares paths: %q, which resolves to zero files inside this plugin's own tree", rel, glob)),
					clikit.Manual("fix "+rel+"'s paths: glob to name a real file inside this plugin, or remove the rule if it no longer applies"),
					map[string]any{"path": rel, "glob": glob},
				)
				if buildErr != nil {
					return nil, buildErr
				}
				diags = append(diags, d)
			}
		}
	}
	return diags, nil
}
