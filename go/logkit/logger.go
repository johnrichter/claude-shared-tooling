package logkit

import (
	"fmt"
	"io"
	"maps"
	"os"
	"time"

	"github.com/rs/zerolog"
)

func init() {
	// logkit sets these explicitly rather than trusting zerolog's defaults:
	// LevelFieldName and MessageFieldName are package-level globals another
	// dependency could otherwise change process-wide. Their values already
	// equal logkit's canonical names; this pins that rather than assuming it.
	zerolog.LevelFieldName = "level"
	zerolog.MessageFieldName = "message"
	zerolog.TimeFieldFormat = time.RFC3339
}

// Logger emits logkit records: one call builds one normalized Record, which
// is rendered to a canonical JSON line (via zerolog as the byte writer, see
// writer.go) and, when configured, a human line - both off the same record.
// A Logger is safe for concurrent use.
type Logger struct {
	service        string
	serviceVersion string
	threshold      Level
	fields         Fields
	captureCaller  bool
	clock          func() time.Time
	jsonSink       io.Writer
	humanSink      io.Writer
	zl             zerolog.Logger
}

// Option configures a Logger at construction.
type Option func(*Logger)

// WithServiceVersion sets the emitting build's version, when one is known.
func WithServiceVersion(v string) Option { return func(l *Logger) { l.serviceVersion = v } }

// WithThreshold sets the minimum level emitted; severity(level) >=
// severity(threshold). Default LevelInfo. fatal is never filtered.
func WithThreshold(level Level) Option { return func(l *Logger) { l.threshold = level } }

// WithJSON sets the canonical-JSON sink. Default os.Stderr; pass nil to
// disable the machine rendering.
func WithJSON(w io.Writer) Option { return func(l *Logger) { l.jsonSink = w } }

// WithHuman sets the human-rendering sink. Disabled by default; both
// renderings may be enabled at once, each to its own sink.
func WithHuman(w io.Writer) Option { return func(l *Logger) { l.humanSink = w } }

// WithCaller enables source-location capture on every emitted record. Off by
// default: capturing it costs a stack walk.
func WithCaller() Option { return func(l *Logger) { l.captureCaller = true } }

// WithClock overrides the wall clock used for Timestamp. Intended for tests;
// production loggers use time.Now.
func WithClock(clock func() time.Time) Option { return func(l *Logger) { l.clock = clock } }

// New builds a Logger for service, which fixes the record's `service` field
// for the logger's lifetime. Returns an error if service fails the schema's
// service pattern.
func New(service string, opts ...Option) (*Logger, error) {
	l := &Logger{
		service:   service,
		threshold: LevelInfo,
		clock:     time.Now,
		jsonSink:  os.Stderr,
	}
	for _, opt := range opts {
		opt(l)
	}
	if !servicePattern(l.service) {
		return nil, fmt.Errorf("logkit: invalid service %q", l.service)
	}
	if l.jsonSink != nil {
		l.zl = zerolog.New(&canonicalWriter{real: l.jsonSink}).Level(zerolog.TraceLevel)
	}
	return l, nil
}

// With returns a child Logger carrying fields merged into every record it
// emits, in addition to whatever a call site passes. The receiver is
// unchanged; a call-site key wins over a context key of the same name.
func (l *Logger) With(fields Fields) *Logger {
	child := *l
	child.fields = mergeFields(l.fields, fields)
	return &child
}

func mergeFields(base, overlay Fields) Fields {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	merged := make(Fields, len(base)+len(overlay))
	maps.Copy(merged, base)
	maps.Copy(merged, overlay)
	return merged
}

// Debug emits a debug-level record. Developer detail for reproducing
// behavior; off in a normal run.
func (l *Logger) Debug(message string, fields Fields) error {
	return l.Emit(LevelDebug, nil, message, fields)
}

// Info emits an info-level record: an expected milestone worth a permanent
// record.
func (l *Logger) Info(message string, fields Fields) error {
	return l.Emit(LevelInfo, nil, message, fields)
}

// Warn emits a warn-level record: a degradation that was handled - the run
// continues and its result is still correct.
func (l *Logger) Warn(message string, fields Fields) error {
	return l.Emit(LevelWarn, nil, message, fields)
}

// Error emits an error-level record: an operation failed. err may be nil;
// when non-nil its message, type name and (if it implements it) stack trace
// populate the record's `error`.
func (l *Logger) Error(err error, message string, fields Fields) error {
	return l.Emit(LevelError, err, message, fields)
}

// Fatal emits a fatal-level record: the process cannot continue. Fatal has
// no side effect of its own - it writes the record and returns; the caller
// terminates through its own exit-code path.
func (l *Logger) Fatal(err error, message string, fields Fields) error {
	return l.Emit(LevelFatal, err, message, fields)
}

// Emit builds and writes one record at level, the general entry point behind
// the five level methods. err may be nil at any level: error and level are
// independent, and a caller with a non-error use for a level outside the
// five convenience methods calls this directly.
func (l *Logger) Emit(level Level, err error, message string, fields Fields) error {
	if !level.Known() {
		return fmt.Errorf("logkit: unknown level %q", string(level))
	}
	if level.Severity() < l.threshold.Severity() {
		return nil
	}

	rec := &Record{
		SchemaVersion:  SchemaVersion,
		Timestamp:      FormatTimestamp(l.clock()),
		Level:          level,
		Service:        l.service,
		Message:        message,
		ServiceVersion: l.serviceVersion,
		Fields:         mergeFields(l.fields, fields),
	}
	if err != nil {
		rec.Error = errorField(err)
	}
	if l.captureCaller {
		rec.Caller = captureCaller(3)
	}
	if verr := rec.Validate(); verr != nil {
		return verr
	}

	if l.jsonSink != nil {
		if err := l.writeJSON(rec); err != nil {
			return err
		}
	}
	if l.humanSink != nil {
		if err := l.writeHuman(rec); err != nil {
			return err
		}
	}
	return nil
}

func (l *Logger) writeJSON(rec *Record) error {
	e := l.zl.WithLevel(zerologLevel(rec.Level))
	e.Str("timestamp", rec.Timestamp)
	e.Int("schema_version", rec.SchemaVersion)
	e.Str("service", rec.Service)
	if rec.ServiceVersion != "" {
		e.Str("service_version", rec.ServiceVersion)
	}
	if len(rec.Fields) > 0 {
		e.Interface("fields", rec.Fields)
	}
	if rec.Error != nil {
		e.Interface("error", rec.Error)
	}
	if rec.Caller != nil {
		e.Interface("caller", rec.Caller)
	}
	e.Msg(rec.Message)
	return nil
}

func (l *Logger) writeHuman(rec *Record) error {
	line, err := RenderHuman(rec)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(l.humanSink, line)
	return err
}

// zerologLevel maps a known logkit Level to its zerolog counterpart. Every
// call site is reached through WithLevel, never Fatal/Panic, so zerolog's own
// process-terminating side effects are never triggered.
func zerologLevel(l Level) zerolog.Level {
	switch l {
	case LevelDebug:
		return zerolog.DebugLevel
	case LevelInfo:
		return zerolog.InfoLevel
	case LevelWarn:
		return zerolog.WarnLevel
	case LevelError:
		return zerolog.ErrorLevel
	case LevelFatal:
		return zerolog.FatalLevel
	default:
		return zerolog.NoLevel
	}
}

// errorField builds the record's `error` from a Go error: its message (first
// line, truncated to the schema's line limit) and concrete type name.
func errorField(err error) *Error {
	return &Error{
		Message: truncateLine(err.Error()),
		Kind:    fmt.Sprintf("%T", err),
	}
}

func truncateLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			s = s[:i]
			break
		}
	}
	const max = 4096
	if len(s) > max {
		s = s[:max]
	}
	return s
}
