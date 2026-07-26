package cost

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ccToolUseLine is the slice of a Claude Code transcript line this package reads that
// transcript.Turn does not carry: the assistant message's tool_use content blocks. transcript
// stays format-generic across turn accounting; which tool a turn invoked is this package's own,
// narrower concern for the "tool" attribution dimension.
type ccToolUseLine struct {
	Message *struct {
		Content []struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"content"`
	} `json:"message"`
}

// toolNamesByLine scans path (Claude Code transcript JSONL) and returns, for every line
// carrying one or more tool_use content blocks, that line's 1-based number mapped to its tool
// names joined with "+" in call order (e.g. "Bash+Read" for a turn that called both in
// parallel). A line invoking no tool is absent from the map rather than mapped to "" — the
// caller's zero value already means "no tool" without a lookup. A line that fails to parse as
// JSON is skipped: this scan only supplements turns transcript.Turn already parsed successfully,
// so a line transcript.Turn itself flags Malformed contributes nothing here either.
func toolNamesByLine(path string) (map[int]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cost: open transcript %s: %w", path, err)
	}
	defer f.Close()

	out := map[int]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var l ccToolUseLine
		if err := json.Unmarshal(line, &l); err != nil || l.Message == nil {
			continue
		}
		var names []string
		for _, block := range l.Message.Content {
			if block.Type == "tool_use" && block.Name != "" {
				names = append(names, block.Name)
			}
		}
		if len(names) > 0 {
			out[lineNo] = strings.Join(names, "+")
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("cost: scan transcript %s: %w", path, err)
	}
	return out, nil
}
