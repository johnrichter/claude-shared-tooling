package toolchain

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/johnrichter/claude-shared-tooling/go/fsx"
)

// logDetail is the complete, uncapped record of one run: every diagnostic
// Parse produced and the tool's raw output. RunResult never carries this
// itself — LogRef only ever points to where it landed.
type logDetail struct {
	Tool        string       `json:"tool"`
	Command     []string     `json:"command"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Stdout      string       `json:"stdout"`
	Stderr      string       `json:"stderr"`
}

// writeLog persists detail under logDir/<id>.json and returns that path —
// the value RunResult.LogRef carries. A caller reads this file for anything
// the capped verdict can't hold: the diagnostics past MaxDiagnostics, or the
// tool's raw output behind a failure Parse could not turn into a diagnostic
// at all.
func writeLog(logDir, id string, detail logDetail) (string, error) {
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return "", fmt.Errorf("toolchain: create log dir %s: %w", logDir, err)
	}
	data, err := json.MarshalIndent(detail, "", "  ")
	if err != nil {
		return "", fmt.Errorf("toolchain: encode log for %s: %w", id, err)
	}
	data = append(data, '\n')
	path := filepath.Join(logDir, id+".json")
	if err := fsx.WriteAtomic(path, data, 0o644); err != nil {
		return "", fmt.Errorf("toolchain: write log %s: %w", path, err)
	}
	return path, nil
}
