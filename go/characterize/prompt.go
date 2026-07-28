package characterize

import "strings"

// buildPrompt composes the one instruction a characterizing probe runs against: read
// pluginName's own files and reply with nothing but the JSON object parseCandidate expects. The
// probe's working directory is the plugin's own root (Options.ProbeOptions.Dir), so every file
// the prompt tells it to read resolves inside the plugin being characterized and nowhere else;
// pluginPath is the repo-qualified prefix every citation in the reply must carry, so this
// package's own citation check can later resolve each one back to a real file.
func buildPrompt(pluginName, pluginPath string) string {
	tmpl := `You are characterizing the Claude Code plugin "{{NAME}}". Your current working directory is that plugin's own root.

Read the plugin's own files -- .claude-plugin/plugin.json, commands/, agents/, skills/, hooks/, any MCP server config, statusline/output-style config, anything else it ships -- and report every surface it contributes: what it is, what invokes it, and where you read that from. Do not guess at a surface, a trigger, or a weakness you did not read; if you cannot establish something from the files, name it as a could_not_determine gap instead of asserting it as a surface.

Reply with ONLY a single JSON object -- no prose, no markdown code fence -- shaped exactly like this:

{
  "surfaces": [
    {
      "type": "command|agent|skill|hook|mcp-server|statusline|output-style|other",
      "name": "the surface's own declared name",
      "trigger": "what invokes it, stated as the declared condition -- read from the file, never inferred from the name alone",
      "citation": {"path": "{{PATH}}/relative/path/to/the/file/proving/this", "lines": [10, 14], "excerpt": "short verbatim quote, when quoting materially supports the claim"},
      "weak_spots": [
        {"description": "the concrete failure mode, not just \"trigger is unclear\"", "basis": "what in the cited text supports this finding", "citation": {"path": "{{PATH}}/relative/path"}, "severity": "low|medium|high"}
      ],
      "notes": "optional"
    }
  ],
  "could_not_determine": [
    {"area": "what you could not establish", "reason": "why a static read could not settle it -- never just \"unclear\"", "attempted_citation": {"path": "{{PATH}}/relative/path"}}
  ]
}

Every "citation.path" and "attempted_citation.path" must start with "{{PATH}}/" and name a real file under it; omit "lines" when a claim is about a whole file, not a specific span. If the plugin contributes no surfaces at all, "surfaces" is an empty array -- that is a claim, not an omission.`

	r := strings.NewReplacer("{{NAME}}", pluginName, "{{PATH}}", pluginPath)
	return r.Replace(tmpl)
}
