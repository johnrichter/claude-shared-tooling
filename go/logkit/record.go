package logkit

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SchemaVersion is the record contract's MAJOR, carried on every record so a
// line found on its own is self-describing.
const SchemaVersion = 1

// Fields is structured context for an event: the variable data that would
// otherwise be interpolated into Message. A key must not collide with a root
// field name; Record.Validate rejects the collision.
type Fields map[string]any

// Error is the log-side view of a failure. It is independent of Level and is
// not clikit's Error contract.
type Error struct {
	Message string   `json:"message"`
	Kind    string   `json:"kind,omitempty"`
	Stack   []string `json:"stack,omitempty"`
}

// Caller is the source location of a log call, module-relative.
type Caller struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Function string `json:"function,omitempty"`
}

// Record is one log event in its normalized form: the exact field set of
// schemas/logkit/log-record.schema.json. Fields are declared in the schema's
// own alphabetical root order so a direct struct marshal already carries
// canonical key order at the root; Marshal still runs the result through an
// RFC 8785 canonicalizer for the nested values (fields, error, caller) and
// for numbers and escaping.
type Record struct {
	Caller         *Caller `json:"caller,omitempty"`
	Error          *Error  `json:"error,omitempty"`
	Fields         Fields  `json:"fields,omitempty"`
	Level          Level   `json:"level"`
	Message        string  `json:"message"`
	SchemaVersion  int     `json:"schema_version"`
	Service        string  `json:"service"`
	ServiceVersion string  `json:"service_version,omitempty"`
	Timestamp      string  `json:"timestamp"`
}

// FormatTimestamp renders t per the contract: UTC, truncated (never rounded)
// to the millisecond, exactly three fractional digits, RFC 3339 with a literal
// Z offset.
func FormatTimestamp(t time.Time) string {
	return t.UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z")
}

// isLine reports whether s satisfies the schema's $defs/line: non-empty, at
// most 4096 characters, and free of control characters (so it survives both
// the one-line JSON rendering and the human rendering unchanged).
func isLine(s string) bool {
	if s == "" || len(s) > 4096 {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

var rootFieldNames = map[string]bool{
	"caller": true, "error": true, "fields": true, "level": true,
	"message": true, "schema_version": true, "service": true,
	"service_version": true, "timestamp": true,
}

var servicePattern = func(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	first := s[0]
	if !(first >= 'a' && first <= 'z' || first >= '0' && first <= '9') {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-'
		if !ok {
			return false
		}
	}
	return true
}

// Validate checks r against the rules log-record.schema.json cannot express
// as types alone: the service pattern, line fields, the fields/root-name
// collision, and the presence of the required fields. It is the last gate
// before a record is canonicalized and emitted.
func (r *Record) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("logkit: schema_version must be %d, got %d", SchemaVersion, r.SchemaVersion)
	}
	if !r.Level.Known() {
		return fmt.Errorf("logkit: unknown level %q", string(r.Level))
	}
	if !servicePattern(r.Service) {
		return fmt.Errorf("logkit: invalid service %q", r.Service)
	}
	if !isLine(r.Message) {
		return fmt.Errorf("logkit: invalid message %q", r.Message)
	}
	if r.Timestamp == "" {
		return fmt.Errorf("logkit: timestamp is required")
	}
	for key := range r.Fields {
		if key == "" || strings.ContainsAny(key, "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f\x7f") {
			return fmt.Errorf("logkit: invalid fields key %q", key)
		}
		if rootFieldNames[key] {
			return fmt.Errorf("logkit: fields key %q collides with a root field name", key)
		}
	}
	// NaN and the infinities are not JSON and are rejected here, never coerced
	// (contract canonical_json.numbers). This also rejects any other value that
	// cannot serialize, which the byte writer would otherwise swallow into a
	// placeholder string.
	if len(r.Fields) > 0 {
		if _, err := json.Marshal(r.Fields); err != nil {
			return fmt.Errorf("logkit: invalid fields value: %w", err)
		}
	}
	if r.Error != nil && !isLine(r.Error.Message) {
		return fmt.Errorf("logkit: invalid error.message %q", r.Error.Message)
	}
	if r.Caller != nil {
		if !isLine(r.Caller.File) || strings.HasPrefix(r.Caller.File, "/") || strings.HasPrefix(r.Caller.File, `\`) {
			return fmt.Errorf("logkit: invalid caller.file %q", r.Caller.File)
		}
		if r.Caller.Line < 1 {
			return fmt.Errorf("logkit: invalid caller.line %d", r.Caller.Line)
		}
	}
	return nil
}
