//! Failure modes that stop a record from being built or emitted. Per the
//! spec's "Normalization" section: failure is loud and named, never a
//! default and never a silently dropped key.

use std::fmt;

use crate::level::UnknownLevel;

/// Why an event was refused before it ever reached a writer.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum LogError {
    /// A level token from outside the process didn't survive normalization.
    UnknownLevel(UnknownLevel),
    /// `service`, `message`, `error.message`, `error.kind`, a stack frame,
    /// `caller.file`, `caller.function` or a `fields` key/value failed the
    /// schema's own shape rule for that field (empty, too long, or carrying
    /// a control character that would break the one-line rendering).
    InvalidValue {
        /// The failing field's name, e.g. `"message"` or `"caller.file"`.
        field: &'static str,
        /// Why it was rejected.
        reason: &'static str,
        /// The offending value, exactly as received.
        value: String,
    },
    /// A `fields` key is one of the record's own root field names, which
    /// the schema forbids because a consumer that flattens the record into
    /// one namespace would silently shadow the root field.
    ReservedFieldName {
        /// The colliding key.
        key: String,
    },
}

impl fmt::Display for LogError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            LogError::UnknownLevel(e) => write!(f, "{e}"),
            LogError::InvalidValue {
                field,
                reason,
                value,
            } => {
                write!(f, "invalid {field} {value:?}: {reason}")
            }
            LogError::ReservedFieldName { key } => {
                write!(f, "fields key {key:?} collides with a root field name")
            }
        }
    }
}

impl std::error::Error for LogError {}

impl From<UnknownLevel> for LogError {
    fn from(e: UnknownLevel) -> Self {
        LogError::UnknownLevel(e)
    }
}
