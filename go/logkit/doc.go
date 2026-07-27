// Package logkit is the Go implementation of the logkit cross-language
// logging standard (schemas/logkit, logkit@1.0.0): one log call produces one
// normalized record, rendered as canonical JSON for machines and a single
// human line for a terminal. zerolog is used only as the JSON stream's byte
// writer; logkit owns the record shape, the level set, the timestamp and the
// canonical serialization.
package logkit
