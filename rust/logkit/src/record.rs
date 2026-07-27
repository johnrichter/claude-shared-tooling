//! The normalized record: `log-record.schema.json` given a Rust shape.
//!
//! A [`Record`] is always schema-valid by construction — every path that
//! builds one goes through this module's validators — so nothing downstream
//! re-checks it against the schema at runtime. [`Record::canonical_json`]
//! and [`Record::to_value`] are the two ways a finished record leaves this
//! module: the wire form and the form [`crate::render`] renders for people.

use serde::Serialize;
use serde_json::Map;
use serde_json::Value;

use crate::error::LogError;
use crate::level::Level;

/// MAJOR of the record contract this crate emits. Per the schema: a
/// consumer built against this crate refuses a record declaring more.
pub const SCHEMA_VERSION: u32 = 1;

/// Every root field name, in the schema's own order. `fields` keys are
/// checked against this set so a context key can never shadow a record
/// field.
pub const ROOT_FIELD_NAMES: [&str; 9] = [
    "caller",
    "error",
    "fields",
    "level",
    "message",
    "schema_version",
    "service",
    "service_version",
    "timestamp",
];

/// The failure that caused the event, when one did. Independent of `level`.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct ErrorInfo {
    /// The failure's own message, verbatim from the error value.
    pub message: String,
    /// The error's type as Rust names it. Absent when none is known.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub kind: Option<String>,
    /// Stack frames, outermost-call first. Never an empty array — omitted
    /// instead when there is nothing to capture.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub stack: Option<Vec<String>>,
}

/// Source location of the log call.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct CallerInfo {
    /// Path from the module root — never absolute.
    pub file: String,
    /// 1-based line number of the log call.
    pub line: u32,
    /// Enclosing function or method, when the runtime can resolve one.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub function: Option<String>,
}

/// One log event in its normalized form.
///
/// Every field here mirrors `log-record.schema.json`'s `properties`
/// one-for-one, by name. Build one through [`crate::Logger`]'s event
/// builder, which runs the validators below before a `Record` ever exists;
/// there is no public constructor that skips them.
#[derive(Debug, Clone, Serialize)]
pub struct Record {
    /// MAJOR of the record contract; always [`SCHEMA_VERSION`].
    pub schema_version: u32,
    /// RFC 3339 UTC millisecond instant the event happened.
    pub timestamp: String,
    /// Severity, from the closed five-member set.
    pub level: Level,
    /// The logical emitter — the CLI, daemon or job name.
    pub service: String,
    /// What happened, as a short constant phrase.
    pub message: String,
    /// Version of the emitting build, when known.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub service_version: Option<String>,
    /// Structured context. Omitted entirely when empty, never `{}`.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub fields: Option<Map<String, Value>>,
    /// The failure that caused the event, when one did.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<ErrorInfo>,
    /// Source location of the log call, when captured.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub caller: Option<CallerInfo>,
}

impl Record {
    /// RFC 8785 (JCS) canonical serialization: sorted keys at every depth,
    /// no whitespace, ECMA-262 numbers. This is the wire form and the byte
    /// sequence three independent implementations agree on.
    ///
    /// # Errors
    /// Only if a `fields` value is structurally not JSON (this crate's own
    /// `serde_json::Value` values always are), so a caller sees this as
    /// effectively infallible.
    pub fn canonical_json(&self) -> Result<String, serde_json::Error> {
        serde_jcs::to_string(self)
    }

    /// The record as a `serde_json::Value`, keys sorted alphabetically
    /// (this crate never enables `serde_json`'s `preserve_order` feature, so
    /// every `Map` is a `BTreeMap` under the hood). [`crate::human_line`] walks
    /// this to derive the human line's attribute order — the same
    /// alphabetical root order the JCS canonicalizer produces for ASCII
    /// keys, so both renderings agree on "the record's own canonical key
    /// order" without deriving one from the other.
    ///
    /// # Panics
    /// Never in practice: every field on this type already serializes to
    /// JSON, so the underlying `serde_json` call cannot fail.
    #[must_use]
    pub fn to_value(&self) -> Value {
        serde_json::to_value(self).expect("Record serializes to a JSON object by construction")
    }
}

/// A non-empty, single-line, length-bounded string: no control character
/// (U+0000-U+001F or U+007F), so it survives both renderings unchanged.
/// Backs `message`, `error.message`, `error.kind`, each stack frame,
/// `caller.function` and `service_version`.
pub(crate) fn validate_line(
    field: &'static str,
    value: &str,
    max_len: usize,
) -> Result<(), LogError> {
    if value.is_empty() {
        return Err(invalid(field, value, "must not be empty"));
    }
    if value.chars().count() > max_len {
        return Err(invalid(field, value, "exceeds the maximum length"));
    }
    if value.chars().any(is_control) {
        return Err(invalid(
            field,
            value,
            "must not contain a control character",
        ));
    }
    Ok(())
}

/// `service`: `^[a-z0-9][a-z0-9._-]*$`, 1-64 characters.
pub(crate) fn validate_service(value: &str) -> Result<(), LogError> {
    let mut chars = value.chars();
    let reason = "must match ^[a-z0-9][a-z0-9._-]*$ and be 1-64 characters";
    match chars.next() {
        Some(c) if c.is_ascii_lowercase() || c.is_ascii_digit() => {}
        _ => return Err(invalid("service", value, reason)),
    }
    if value.chars().count() > 64
        || !chars
            .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || matches!(c, '.' | '_' | '-'))
    {
        return Err(invalid("service", value, reason));
    }
    Ok(())
}

/// `caller.file`: a control-character-free, module-relative path — no
/// leading `/` or `\`, since an absolute path bakes in the build machine's
/// directory layout and breaks byte-identity across hosts.
pub(crate) fn validate_caller_file(value: &str) -> Result<(), LogError> {
    validate_line("caller.file", value, 256)?;
    if value.starts_with('/') || value.starts_with('\\') {
        return Err(invalid(
            "caller.file",
            value,
            "must be relative, not absolute",
        ));
    }
    Ok(())
}

/// A `fields` key: non-empty, control-character-free, at most 128
/// characters, and not one of [`ROOT_FIELD_NAMES`] (the schema's
/// `not: root_field_name` rule — a context key can never shadow a root
/// field).
pub(crate) fn validate_field_key(key: &str) -> Result<(), LogError> {
    validate_line("fields key", key, 128)?;
    if ROOT_FIELD_NAMES.contains(&key) {
        return Err(LogError::ReservedFieldName {
            key: key.to_string(),
        });
    }
    Ok(())
}

fn is_control(c: char) -> bool {
    c.is_control()
}

fn invalid(field: &'static str, value: &str, reason: &'static str) -> LogError {
    LogError::InvalidValue {
        field,
        reason,
        value: value.to_string(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn service_accepts_the_schema_pattern() {
        for ok in ["navigator", "git-tools", "anoikis-tools", "a", "a0._-z"] {
            validate_service(ok).unwrap();
        }
    }

    #[test]
    fn service_rejects_uppercase_leading_symbol_and_overlong() {
        for bad in ["Navigator", "-nav", "", &"a".repeat(65)] {
            assert!(
                validate_service(bad).is_err(),
                "expected {bad:?} to be rejected"
            );
        }
    }

    #[test]
    fn line_rejects_empty_and_control_characters() {
        assert!(validate_line("message", "", 4096).is_err());
        assert!(validate_line("message", "line one\nline two", 4096).is_err());
        validate_line("message", "index rebuilt", 4096).unwrap();
    }

    #[test]
    fn field_key_rejects_root_field_collision() {
        assert!(validate_field_key("service").is_err());
        assert!(validate_field_key("documents").is_ok());
    }

    #[test]
    fn caller_file_rejects_absolute_paths() {
        assert!(validate_caller_file("/abs/path.rs").is_err());
        validate_caller_file("rust/bm25/src/index.rs").unwrap();
    }

    #[test]
    fn empty_optional_fields_are_omitted_not_null_or_empty() {
        let record = Record {
            schema_version: SCHEMA_VERSION,
            timestamp: "2026-07-26T09:41:07.480Z".to_string(),
            level: Level::Info,
            service: "navigator".to_string(),
            message: "index rebuilt".to_string(),
            service_version: None,
            fields: None,
            error: None,
            caller: None,
        };
        let json = record.canonical_json().unwrap();
        assert!(!json.contains("fields"));
        assert!(!json.contains("null"));
        assert_eq!(
            json,
            r#"{"level":"info","message":"index rebuilt","schema_version":1,"service":"navigator","timestamp":"2026-07-26T09:41:07.480Z"}"#
        );
    }
}
