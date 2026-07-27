// Package clikit is the Go implementation of the clikit CLI output, error and
// exit contract (schemas/clikit, clikit@1.0.0): one invocation produces one
// normalized result record on stdout and exits with one of eleven codes.
// clikit owns the record, the exit-code taxonomy, and structured errors,
// caveats and triage directives; it defines no logging of its own and
// consumes logkit for every log line a CLI writes to stderr.
package clikit
