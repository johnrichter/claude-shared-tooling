//! Why a [`crate::ResultRecordBuilder`] refused to build: every rejection is
//! a named variant here, never a panic and never a silently-dropped field —
//! the same fail-loud posture as logkit's own `LogError`.

use std::fmt;

use crate::status::Status;

/// A record that would not validate against `result-record.schema.json`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ClikitError {
    /// A string field failed its schema shape rule (empty, too long, a
    /// control character, or the wrong pattern).
    InvalidValue {
        /// The failing field's name, e.g. `"command[1]"` or `"error.message"`.
        field: &'static str,
        /// Why it was rejected.
        reason: &'static str,
        /// The offending value, exactly as received.
        value: String,
    },
    /// An array exceeded the schema's member cap.
    TooMany {
        /// The array's field name.
        field: &'static str,
        /// The schema's cap.
        max: usize,
        /// How many were given.
        actual: usize,
    },
    /// `status` is a failure class (20 and above) but `errors` is empty.
    MissingErrors {
        /// The record's status.
        status: Status,
    },
    /// `status` is `caveats` but `caveats` is empty.
    MissingCaveats,
    /// `errors` was given for `success` or `caveats`, which forbid it.
    ErrorsNotAllowed {
        /// The record's status.
        status: Status,
    },
    /// `caveats` was given for `success`, which forbids it.
    CaveatsNotAllowed,
    /// `errors[0]`'s code does not start with the record's own status —
    /// the schema's per-class `prefixItems` rule on the governing error.
    GoverningCodeMismatch {
        /// The record's status.
        status: Status,
        /// The offending code.
        code: String,
    },
    /// A `caveats` member's code does not carry the `caveats.` prefix.
    NotACaveatCode {
        /// The offending code.
        code: String,
    },
}

impl fmt::Display for ClikitError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            ClikitError::InvalidValue {
                field,
                reason,
                value,
            } => write!(f, "invalid {field} {value:?}: {reason}"),
            ClikitError::TooMany { field, max, actual } => {
                write!(f, "{field} carries {actual} members, over the cap of {max}")
            }
            ClikitError::MissingErrors { status } => {
                write!(
                    f,
                    "status {:?} requires a non-empty errors array",
                    status.as_str()
                )
            }
            ClikitError::MissingCaveats => {
                write!(f, "status \"caveats\" requires a non-empty caveats array")
            }
            ClikitError::ErrorsNotAllowed { status } => {
                write!(f, "status {:?} forbids an errors array", status.as_str())
            }
            ClikitError::CaveatsNotAllowed => {
                write!(f, "status \"success\" forbids a caveats array")
            }
            ClikitError::GoverningCodeMismatch { status, code } => write!(
                f,
                "governing error code {code:?} does not start with status {:?}",
                status.as_str()
            ),
            ClikitError::NotACaveatCode { code } => {
                write!(f, "caveat code {code:?} does not start with \"caveats.\"")
            }
        }
    }
}

impl std::error::Error for ClikitError {}
