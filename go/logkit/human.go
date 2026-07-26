package logkit

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// levelPad is the human level token, upper-cased and right-padded to 5, per
// logkit.contract.json human_rendering.level_token.
func levelPad(l Level) string {
	return fmt.Sprintf("%-5s", strings.ToUpper(string(l)))
}

// RenderHuman renders r as the human line(s) defined by logkit.contract.json
// human_rendering: "<timestamp> <LEVEL> <service> <message>[ <attribute>]...",
// attributes in the record's own canonical key order with fields expanded in
// place, followed by one indented line per error stack frame. It never
// parses back and is not a wire format.
func RenderHuman(r *Record) (string, error) {
	var b strings.Builder
	b.WriteString(r.Timestamp)
	b.WriteByte(' ')
	b.WriteString(levelPad(r.Level))
	b.WriteByte(' ')
	b.WriteString(r.Service)
	b.WriteByte(' ')
	b.WriteString(r.Message)

	if r.Caller != nil {
		fmt.Fprintf(&b, " caller=%s:%d", r.Caller.File, r.Caller.Line)
		if r.Caller.Function != "" {
			if err := writeAttr(&b, "caller_function", r.Caller.Function); err != nil {
				return "", err
			}
		}
	}

	if r.Error != nil {
		if err := writeAttr(&b, "error", r.Error.Message); err != nil {
			return "", err
		}
		if r.Error.Kind != "" {
			if err := writeAttr(&b, "error_kind", r.Error.Kind); err != nil {
				return "", err
			}
		}
	}

	keys := make([]string, 0, len(r.Fields))
	for k := range r.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := writeAttr(&b, k, r.Fields[k]); err != nil {
			return "", err
		}
	}

	if r.ServiceVersion != "" {
		if err := writeAttr(&b, "service_version", r.ServiceVersion); err != nil {
			return "", err
		}
	}

	if r.Error != nil {
		for _, frame := range r.Error.Stack {
			b.WriteByte('\n')
			b.WriteString("  ")
			b.WriteString(frame)
		}
	}

	return b.String(), nil
}

func writeAttr(b *strings.Builder, key string, value any) error {
	rendered, err := renderHumanValue(value)
	if err != nil {
		return err
	}
	b.WriteByte(' ')
	b.WriteString(key)
	b.WriteByte('=')
	b.WriteString(rendered)
	return nil
}

// renderHumanValue applies logkit.contract.json human_rendering.value_quoting:
// a string with no whitespace, quote, `=` or control character renders bare;
// any other value (a quote-needing string, a number, a bool, null, an array
// or an object) renders as its canonical JSON form.
func renderHumanValue(v any) (string, error) {
	if s, ok := v.(string); ok && isBareValue(s) {
		return s, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("logkit: render value %v: %w", v, err)
	}
	canon, err := canonicalizeRaw(raw)
	if err != nil {
		return "", fmt.Errorf("logkit: render value %v: %w", v, err)
	}
	return string(canon), nil
}

func isBareValue(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r <= 0x20 || r == 0x7f || r == '"' || r == '=' {
			return false
		}
	}
	return true
}
