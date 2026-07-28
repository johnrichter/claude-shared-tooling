package plugin_conform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
)

// componentGlobs are the file globs a plugin's own components ship frontmatter under, per Claude
// Code's default (unconfigured) component layout: one command/agent per file, one skill per
// SKILL.md (multi-skill or single-skill-at-root layout). A plugin.json declaring custom
// component paths is out of this check's reach -- it reads only the default layout, the same
// honest limit CheckRuleGlobs and CheckHooksWellFormed declare for their own default-path reads.
var componentGlobs = []string{
	"commands/*.md",
	"commands/**/*.md",
	"agents/*.md",
	"agents/**/*.md",
	"skills/*/SKILL.md",
	"SKILL.md",
}

// componentFrontmatter is the one field every component type shares regardless of kind.
type componentFrontmatter struct {
	Description string `yaml:"description"`
}

// CheckComponentFrontmatter parses every command/agent/skill file found under pluginDir's
// default component directories and requires each to open with a valid YAML frontmatter block
// declaring a non-empty description. A file matched by more than one glob is checked once,
// deduplicated by path.
func CheckComponentFrontmatter(pluginDir string) ([]clikit.Diagnostic, error) {
	paths, err := expandGlobs(pluginDir, componentGlobs)
	if err != nil {
		return nil, fmt.Errorf("plugin-conform: expand component globs: %w", err)
	}

	var diags []clikit.Diagnostic
	for _, rel := range paths {
		data, err := os.ReadFile(filepath.Join(pluginDir, rel))
		if err != nil {
			return nil, fmt.Errorf("plugin-conform: read component %q: %w", rel, err)
		}

		var fm componentFrontmatter
		found, parseErr := parseFrontmatter(rel, data, &fm)
		if parseErr != nil {
			d, buildErr := clikit.NewError(
				"gate_negative.plugin_conform.frontmatter_invalid",
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
			d, buildErr := clikit.NewError(
				"gate_negative.plugin_conform.frontmatter_missing",
				oneLine(rel+" has no leading YAML frontmatter block"),
				clikit.Manual("add a \"---\"-fenced frontmatter block declaring at least description to "+rel),
				map[string]any{"path": rel},
			)
			if buildErr != nil {
				return nil, buildErr
			}
			diags = append(diags, d)
			continue
		}

		if strings.TrimSpace(fm.Description) == "" {
			d, buildErr := clikit.NewError(
				"gate_negative.plugin_conform.frontmatter_description_missing",
				oneLine(rel+" declares no non-empty description"),
				clikit.Manual("add a non-empty description field to "+rel+"'s frontmatter"),
				map[string]any{"path": rel},
			)
			if buildErr != nil {
				return nil, buildErr
			}
			diags = append(diags, d)
		}
	}
	return diags, nil
}
