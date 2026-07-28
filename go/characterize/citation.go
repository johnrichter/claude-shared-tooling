package characterize

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveCitation checks c against the plugin's real files on disk: its path must sit inside
// pluginRepoPath (the manifest's own plugin.path), and the file it names -- pluginDir joined
// with the path's remainder after that prefix -- must exist and be a regular file. When c also
// cites a line range, that range must fit inside the file's actual line count.
//
// This is the one gate every surface and weak spot citation passes through before it is allowed
// into a manifest. A citation that fails it names a real problem (the agent's own claim), never
// this package's; the caller's job is to fold that claim into could_not_determine instead of
// discarding it.
func resolveCitation(pluginRepoPath, pluginDir string, c Citation) error {
	if strings.TrimSpace(c.Path) == "" {
		return fmt.Errorf("citation has no path")
	}
	rel, err := repoRelative(pluginRepoPath, c.Path)
	if err != nil {
		return err
	}
	if rel == "" {
		return fmt.Errorf("citation path %q names the plugin's own root, not a file", c.Path)
	}

	full := filepath.Join(pluginDir, rel)
	info, err := os.Stat(full)
	if err != nil {
		return fmt.Errorf("citation path %q does not resolve to a real file: %w", c.Path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("citation path %q names a directory, not a file", c.Path)
	}

	if len(c.Lines) == 0 {
		return nil
	}
	if len(c.Lines) != 2 || c.Lines[0] < 1 || c.Lines[1] < c.Lines[0] {
		return fmt.Errorf("citation path %q carries an invalid line range %v", c.Path, c.Lines)
	}
	total, err := countLines(full)
	if err != nil {
		return fmt.Errorf("citation path %q: counting lines: %w", c.Path, err)
	}
	if c.Lines[1] > total {
		return fmt.Errorf("citation path %q cites line %d, past the file's %d lines", c.Path, c.Lines[1], total)
	}
	return nil
}

// repoRelative strips pluginRepoPath from path and returns the remainder, or an error if path
// does not sit inside pluginRepoPath at all -- a citation outside the characterized plugin's own
// directory is never a legitimate surface citation, characterization only reads the one plugin
// it was asked to.
func repoRelative(pluginRepoPath, path string) (string, error) {
	if path == pluginRepoPath {
		return "", nil
	}
	prefix := pluginRepoPath + "/"
	if !strings.HasPrefix(path, prefix) {
		return "", fmt.Errorf("citation path %q is outside the plugin's own path %q", path, pluginRepoPath)
	}
	return strings.TrimPrefix(path, prefix), nil
}

// countLines counts path's lines the same way a line-numbered citation is meant to be read:
// every newline-terminated line, plus a final unterminated line if the file has trailing content
// with no closing newline.
func countLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	n := 0
	for scanner.Scan() {
		n++
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return n, nil
}
