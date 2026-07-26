package logkit

import "fmt"

// Level is a logkit severity. It is string-backed so a hand-built value can
// carry an unknown token; Known reports whether it is one of the five
// canonical names, and is the only gate through which a level is accepted.
type Level string

// The closed level set, per schemas/logkit/log-record.schema.json $defs/level.
// Adding or removing a member is a MAJOR change to the record contract.
const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
	LevelFatal Level = "fatal"
)

// severity holds the ordinal each level carries, per $defs/severity. Ordinals
// are spaced by ten and never serialized; they exist only to compare levels.
var severity = map[Level]int{
	LevelDebug: 10,
	LevelInfo:  20,
	LevelWarn:  30,
	LevelError: 40,
	LevelFatal: 50,
}

// Known reports whether l is byte-equal to one of the five canonical level
// names. It does not trim, case-fold or accept an alias: normalize a foreign
// token with NormalizeLevel first.
func (l Level) Known() bool {
	_, ok := severity[l]
	return ok
}

// Severity returns l's ordinal. Panics if !l.Known(), so a caller normalizes
// and checks Known before relying on the ordinal.
func (l Level) Severity() int {
	s, ok := severity[l]
	if !ok {
		panic(fmt.Sprintf("logkit: Severity called on unknown level %q", string(l)))
	}
	return s
}

func (l Level) String() string { return string(l) }

// levelAlias is the inbound normalization map from logkit.contract.json
// level_normalization.map: source token -> canonical level, plus whether the
// mapping is a true equivalence or a lossy rename that must preserve the
// original token in fields.native_level.
type levelAlias struct {
	level      Level
	equivalent bool
}

var levelAliases = map[string]levelAlias{
	"trace":    {LevelDebug, false},
	"debug":    {LevelDebug, true},
	"info":     {LevelInfo, true},
	"warn":     {LevelWarn, true},
	"warning":  {LevelWarn, true},
	"error":    {LevelError, true},
	"fatal":    {LevelFatal, true},
	"critical": {LevelFatal, true},
	"panic":    {LevelFatal, false},
}

// UnknownLevelError names an out-of-band level token that failed
// normalization: the offending token and where it came from.
type UnknownLevelError struct {
	Token  string
	Source string
}

func (e *UnknownLevelError) Error() string {
	return fmt.Sprintf("logkit: unknown level %q from %s", e.Token, e.Source)
}

// NormalizeLevel runs the inbound normalization procedure on a token from
// outside the process (a flag, an environment variable, a foreign record):
// trim, ASCII-lowercase, then look up in the alias map. It never defaults and
// never guesses; an unmapped token fails naming itself and source.
//
// nativeLevel is the token to preserve in fields.native_level when the
// mapping is lossy (trace, panic); it is empty for an equivalent mapping.
func NormalizeLevel(token, source string) (level Level, nativeLevel string, err error) {
	trimmed := trimASCIISpace(token)
	lower := asciiLower(trimmed)
	alias, ok := levelAliases[lower]
	if !ok {
		return "", "", &UnknownLevelError{Token: token, Source: source}
	}
	if !alias.equivalent {
		return alias.level, token, nil
	}
	return alias.level, "", nil
}

func trimASCIISpace(s string) string {
	start := 0
	for start < len(s) && isASCIISpace(s[start]) {
		start++
	}
	end := len(s)
	for end > start && isASCIISpace(s[end-1]) {
		end--
	}
	return s[start:end]
}

func isASCIISpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}

// asciiLower lowercases only A-Z. A locale-sensitive lowercase (strings.ToLower
// under a Turkish locale) maps I to the dotless i and leaves INFO unequal to
// info; this never does that.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
