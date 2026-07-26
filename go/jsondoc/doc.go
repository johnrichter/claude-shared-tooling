// Package jsondoc is a small toolkit for treating JSON as a content-addressed
// artifact: canonical serialization (RFC 8785 — sorted keys, ECMA-262 number
// formatting, no incidental whitespace), a sha256 content hash over a
// caller-chosen field set, and a set diff (carried/changed/added/removed)
// over keyed collections of JSON documents such as a dependency set or a
// task plan. Canonicalization underlies both: a document is always
// canonicalized before it is hashed or compared, so key order, whitespace,
// and float formatting never register as a change.
package jsondoc
