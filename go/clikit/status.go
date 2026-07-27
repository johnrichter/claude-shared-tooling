package clikit

import (
	"fmt"

	"github.com/johnrichter/claude-shared-tooling/go/logkit"
)

// SchemaVersion is the record contract's MAJOR, carried on every record so a
// captured record is self-describing without its process context.
const SchemaVersion = 1

// Status is a clikit outcome class. It is string-backed so an unrecognized
// value can be carried and reported rather than silently coerced; Known is
// the only gate through which a status is accepted.
type Status string

// The closed status set, per schemas/clikit/result-record.schema.json
// $defs/status, in taxonomy order. Adding or removing a member is a MAJOR
// change to the record contract.
const (
	StatusSuccess           Status = "success"
	StatusCaveats           Status = "caveats"
	StatusGateNegative      Status = "gate_negative"
	StatusPreconditionUnmet Status = "precondition_unmet"
	StatusNotFound          Status = "not_found"
	StatusConflict          Status = "conflict"
	StatusUsage             Status = "usage"
	StatusTransient         Status = "transient"
	StatusPermission        Status = "permission"
	StatusUnsupported       Status = "unsupported"
	StatusInternal          Status = "internal"
)

// classInfo pins one row of the taxonomy: the exit code a status pairs with
// and the logkit level its terminating log record is written at
// (clikit.contract.json exit_taxonomy.classes[].log_level).
type classInfo struct {
	exitCode int
	logLevel logkit.Level
}

// taxonomy is the closed status -> {exit_code, log_level} table. Every entry
// here is one of the eleven classes; there is no twelfth.
var taxonomy = map[Status]classInfo{
	StatusSuccess:           {0, logkit.LevelInfo},
	StatusCaveats:           {10, logkit.LevelWarn},
	StatusGateNegative:      {20, logkit.LevelInfo},
	StatusPreconditionUnmet: {30, logkit.LevelError},
	StatusNotFound:          {40, logkit.LevelError},
	StatusConflict:          {41, logkit.LevelError},
	StatusUsage:             {50, logkit.LevelError},
	StatusTransient:         {60, logkit.LevelError},
	StatusPermission:        {70, logkit.LevelError},
	StatusUnsupported:       {80, logkit.LevelError},
	StatusInternal:          {90, logkit.LevelFatal},
}

// Known reports whether s is one of the eleven canonical statuses.
func (s Status) Known() bool {
	_, ok := taxonomy[s]
	return ok
}

// ExitCode returns the integer s pairs with. Panics if !s.Known(), so a
// caller checks Known (or goes through a Result constructor, which always
// pairs the two correctly) before relying on it.
func (s Status) ExitCode() int {
	c, ok := taxonomy[s]
	if !ok {
		panic(fmt.Sprintf("clikit: ExitCode called on unknown status %q", string(s)))
	}
	return c.exitCode
}

// LogLevel returns the logkit level s's terminating log record is written
// at by default. A command with more context may log higher, never lower.
func (s Status) LogLevel() logkit.Level {
	c, ok := taxonomy[s]
	if !ok {
		panic(fmt.Sprintf("clikit: LogLevel called on unknown status %q", string(s)))
	}
	return c.logLevel
}

// StatusForExitCode returns the status paired with code, and false if code is
// not one of the eleven. Never compare exit codes with < or > - the taxonomy
// is a classification, not a severity ordering.
func StatusForExitCode(code int) (Status, bool) {
	for s, c := range taxonomy {
		if c.exitCode == code {
			return s, true
		}
	}
	return "", false
}
