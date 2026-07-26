//! [`Diagnostic`]: one entry of `errors` or `caveats` — `result-record.schema.json`'s
//! `$defs/error`, shared by both arrays. Which array it sits in, not
//! anything on the value itself, says whether it's a reason the outcome
//! isn't plain success or a qualification on a usable one.

use serde::Serialize;
use serde_json::{Map, Value};

use crate::error::ClikitError;
use crate::triage::Triage;
use crate::validate;

/// One structured, actionable statement — an error or a caveat, depending
/// on which array a [`crate::ResultRecordBuilder`] call places it in.
#[derive(Debug, Clone, Serialize)]
pub struct Diagnostic {
    /// Stable machine identity. Its first segment is the status class this
    /// diagnostic belongs to (`<class>.<domain>.<condition>`); `<class>.clikit.*`
    /// is reserved to this library.
    pub code: String,
    /// What went wrong, in the caller's terms, as one line. No stack trace,
    /// no captured output, no repetition of `code`.
    pub message: String,
    /// What the caller does next. Required — a diagnostic without one is
    /// not finished.
    pub triage: Triage,
    /// Bounded structured detail for this diagnostic. Omitted when there is
    /// none, never `{}`.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub context: Option<Map<String, Value>>,
}

impl Diagnostic {
    /// Starts a diagnostic. `code` is validated at
    /// [`crate::ResultRecordBuilder::build`] time, once the record's status
    /// (and hence which class prefix `code` must carry) is known.
    #[must_use]
    pub fn new(code: impl Into<String>, message: impl Into<String>, triage: Triage) -> Self {
        Diagnostic {
            code: code.into(),
            message: message.into(),
            triage,
            context: None,
        }
    }

    /// Adds one `context` entry. A key written twice keeps the last value.
    #[must_use]
    pub fn context(mut self, key: impl Into<String>, value: impl Into<Value>) -> Self {
        self.context
            .get_or_insert_with(Map::new)
            .insert(key.into(), value.into());
        self
    }

    /// Validates everything about this diagnostic except its class prefix,
    /// which only the containing record's status (and which array it sits
    /// in) can decide.
    pub(crate) fn validate(&self) -> Result<(), ClikitError> {
        validate::validate_code(&self.code)?;
        validate::validate_line("message", &self.message, 4096)?;
        self.triage.validate()?;
        if let Some(context) = &self.context {
            if context.is_empty() || context.len() > 32 {
                return Err(ClikitError::TooMany {
                    field: "context",
                    max: 32,
                    actual: context.len(),
                });
            }
            for key in context.keys() {
                validate::validate_member_key("context key", key)?;
            }
        }
        Ok(())
    }
}
