package agentcontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// resolveSchemaPath resolves a brief's declared OutputSchema against the brief's own directory
// first, then against each of roots in order, returning the first path that exists on disk.
func resolveSchemaPath(b *Brief, roots []string) (string, bool) {
	ref := b.Frontmatter.Contract.OutputSchema
	if ref == "" {
		return "", false
	}
	if filepath.IsAbs(ref) {
		if fileExists(ref) {
			return ref, true
		}
		return "", false
	}
	if candidate := filepath.Join(b.Dir, ref); fileExists(candidate) {
		return candidate, true
	}
	for _, root := range roots {
		if candidate := filepath.Join(root, ref); fileExists(candidate) {
			return candidate, true
		}
	}
	return "", false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// looksLikePath reports whether s has the shape of a file reference rather than a prose
// description: no whitespace, and a recognized schema file extension.
func looksLikePath(s string) bool {
	if s == "" || strings.ContainsAny(s, " \t\n") {
		return false
	}
	switch filepath.Ext(s) {
	case ".json", ".yaml", ".yml":
		return true
	default:
		return false
	}
}

// loadSchemaDoc reads and JSON-decodes the schema file at path into a generic tree for the
// structural FB11 checks. It does not validate the document as a JSON Schema — only that it
// parses, and what shape its "required"/"properties" nodes carry.
func loadSchemaDoc(path string) (any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// requiredArrayContains reports whether doc contains, anywhere, a "required" array listing key.
func requiredArrayContains(doc any, key string) bool {
	found := false
	walk(doc, func(node any) {
		obj, ok := node.(map[string]any)
		if !ok {
			return
		}
		req, ok := obj["required"].([]any)
		if !ok {
			return
		}
		for _, item := range req {
			if s, ok := item.(string); ok && s == key {
				found = true
			}
		}
	})
	return found
}

// propertyDefinition returns the schema node defining the named property under any
// "properties" object in doc, if one exists.
func propertyDefinition(doc any, key string) (any, bool) {
	var def any
	found := false
	walk(doc, func(node any) {
		obj, ok := node.(map[string]any)
		if !ok {
			return
		}
		props, ok := obj["properties"].(map[string]any)
		if !ok {
			return
		}
		if v, ok := props[key]; ok {
			def = v
			found = true
		}
	})
	return def, found
}

// subtreeMentions reports whether any string leaf in node contains needle, case-insensitively.
func subtreeMentions(node any, needle string) bool {
	needle = strings.ToLower(needle)
	found := false
	walk(node, func(n any) {
		if s, ok := n.(string); ok && strings.Contains(strings.ToLower(s), needle) {
			found = true
		}
	})
	return found
}

// walk visits every node of a decoded-JSON tree (maps, slices, and leaves) depth-first,
// including node itself.
func walk(node any, visit func(any)) {
	visit(node)
	switch v := node.(type) {
	case map[string]any:
		for _, child := range v {
			walk(child, visit)
		}
	case []any:
		for _, child := range v {
			walk(child, visit)
		}
	}
}
