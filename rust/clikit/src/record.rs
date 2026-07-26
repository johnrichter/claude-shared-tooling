//! [`ResultRecord`]: `result-record.schema.json` given a Rust shape, and
//! [`ResultRecordBuilder`], the only way to build one — every path runs the
//! schema's own validators, so a `ResultRecord` that exists is always
//! schema-valid, the same guarantee logkit's `Record` makes for its type.

use serde::Serialize;
use serde_json::{Map, Value};

use crate::diagnostic::Diagnostic;
use crate::error::ClikitError;
use crate::status::Status;
use crate::validate;

/// MAJOR of the record contract this crate emits. Per the schema: a
/// consumer built against this crate refuses a record declaring more.
pub const SCHEMA_VERSION: u32 = 1;

/// One CLI invocation's result, in its normalized form — the single record
/// a clikit CLI writes to stdout, whatever the outcome.
#[derive(Debug, Clone, Serialize)]
pub struct ResultRecord {
    /// Always [`SCHEMA_VERSION`].
    pub schema_version: u32,
    /// The resolved command path, root command first. Canonical names
    /// only: no alias, no flag, no operand.
    pub command: Vec<String>,
    /// The outcome class.
    pub status: Status,
    /// The integer the process exits with. Always `status.exit_code()`.
    pub exit_code: u16,
    /// The command's own answer — the only extension point. Omitted when
    /// there's nothing to report.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub data: Option<Map<String, Value>>,
    /// Why the outcome is not plain success. Present for every failure
    /// class, absent for `success` and `caveats`.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub errors: Option<Vec<Diagnostic>>,
    /// Qualifications on a result that's still usable. Required for
    /// `caveats`, forbidden for `success`.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub caveats: Option<Vec<Diagnostic>>,
}

impl ResultRecord {
    /// Starts building a record for `status` and `command` (argv-free
    /// command path, root command first).
    pub fn builder<I, S>(status: Status, command: I) -> ResultRecordBuilder
    where
        I: IntoIterator<Item = S>,
        S: Into<String>,
    {
        ResultRecordBuilder {
            status,
            command: command.into_iter().map(Into::into).collect(),
            data: None,
            errors: Vec::new(),
            caveats: Vec::new(),
        }
    }

    /// RFC 8785 (JCS) canonical serialization — the wire form, and the byte
    /// sequence the Go and Rust implementations of this contract agree on.
    ///
    /// # Errors
    /// Only if `data` or a diagnostic's `context` holds a structurally
    /// invalid JSON value, which this crate's own `serde_json::Value`
    /// values never are — a caller sees this as effectively infallible.
    pub fn canonical_json(&self) -> Result<String, serde_json::Error> {
        serde_jcs::to_string(self)
    }
}

/// Builds one [`ResultRecord`]. Every path through [`Self::build`] runs the
/// schema's per-class rules, so construction fails loud rather than
/// producing a record that would fail schema validation downstream.
#[must_use = "a record is only built once `.build()` is called"]
pub struct ResultRecordBuilder {
    status: Status,
    command: Vec<String>,
    data: Option<Map<String, Value>>,
    errors: Vec<Diagnostic>,
    caveats: Vec<Diagnostic>,
}

impl ResultRecordBuilder {
    /// Adds one `data` entry — the command's own answer.
    pub fn data(mut self, key: impl Into<String>, value: impl Into<Value>) -> Self {
        self.data
            .get_or_insert_with(Map::new)
            .insert(key.into(), value.into());
        self
    }

    /// Appends one `errors` member, most-actionable-first: the first one
    /// added is the governing error, whose code's class must equal this
    /// record's `status`.
    pub fn error(mut self, diagnostic: Diagnostic) -> Self {
        self.errors.push(diagnostic);
        self
    }

    /// Appends one `caveats` member.
    pub fn caveat(mut self, diagnostic: Diagnostic) -> Self {
        self.caveats.push(diagnostic);
        self
    }

    /// Validates and finishes the record.
    ///
    /// # Errors
    /// A named [`ClikitError`] when `command`, `data`, an `errors`/`caveats`
    /// member, or the `errors`/`caveats` presence rule for this record's
    /// `status` fails the schema. Never defaults, never drops a diagnostic
    /// to make the rest pass.
    pub fn build(self) -> Result<ResultRecord, ClikitError> {
        validate_command(&self.command)?;

        if let Some(data) = &self.data {
            if data.is_empty() || data.len() > 64 {
                return Err(ClikitError::TooMany {
                    field: "data",
                    max: 64,
                    actual: data.len(),
                });
            }
            for key in data.keys() {
                validate::validate_member_key("data key", key)?;
            }
        }

        for diagnostic in self.errors.iter().chain(&self.caveats) {
            diagnostic.validate()?;
        }

        match self.status {
            Status::Success => {
                if !self.errors.is_empty() {
                    return Err(ClikitError::ErrorsNotAllowed {
                        status: self.status,
                    });
                }
                if !self.caveats.is_empty() {
                    return Err(ClikitError::CaveatsNotAllowed);
                }
            }
            Status::Caveats => {
                if !self.errors.is_empty() {
                    return Err(ClikitError::ErrorsNotAllowed {
                        status: self.status,
                    });
                }
                if self.caveats.is_empty() {
                    return Err(ClikitError::MissingCaveats);
                }
            }
            _ => {
                if self.errors.is_empty() {
                    return Err(ClikitError::MissingErrors {
                        status: self.status,
                    });
                }
            }
        }

        if self.errors.len() > 50 {
            return Err(ClikitError::TooMany {
                field: "errors",
                max: 50,
                actual: self.errors.len(),
            });
        }
        if self.caveats.len() > 50 {
            return Err(ClikitError::TooMany {
                field: "caveats",
                max: 50,
                actual: self.caveats.len(),
            });
        }

        for caveat in &self.caveats {
            if !caveat.code.starts_with("caveats.") {
                return Err(ClikitError::NotACaveatCode {
                    code: caveat.code.clone(),
                });
            }
        }
        for error in &self.errors {
            if error.code.starts_with("caveats.") {
                return Err(ClikitError::GoverningCodeMismatch {
                    status: self.status,
                    code: error.code.clone(),
                });
            }
        }
        if let Some(governing) = self.errors.first() {
            let prefix = format!("{}.", self.status.as_str());
            if self.status.is_failure() && !governing.code.starts_with(&prefix) {
                return Err(ClikitError::GoverningCodeMismatch {
                    status: self.status,
                    code: governing.code.clone(),
                });
            }
        }

        Ok(ResultRecord {
            schema_version: SCHEMA_VERSION,
            command: self.command,
            exit_code: self.status.exit_code(),
            status: self.status,
            data: self.data,
            errors: (!self.errors.is_empty()).then_some(self.errors),
            caveats: (!self.caveats.is_empty()).then_some(self.caveats),
        })
    }
}

fn validate_command(command: &[String]) -> Result<(), ClikitError> {
    if command.is_empty() || command.len() > 8 {
        return Err(ClikitError::InvalidValue {
            field: "command",
            reason: "must carry 1-8 elements",
            value: format!("{} elements", command.len()),
        });
    }
    validate::validate_tool_name(&command[0])?;
    for element in &command[1..] {
        validate::validate_subcommand_name("command element", element)?;
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::triage::Triage;

    #[test]
    fn success_forbids_errors_and_caveats() {
        let err = ResultRecord::builder(Status::Success, ["navigator"])
            .error(Diagnostic::new(
                "internal.state.x",
                "oops",
                Triage::manual("report it"),
            ))
            .build()
            .unwrap_err();
        assert!(matches!(err, ClikitError::ErrorsNotAllowed { .. }));
    }

    #[test]
    fn caveats_requires_a_caveat() {
        let err = ResultRecord::builder(Status::Caveats, ["navigator"])
            .build()
            .unwrap_err();
        assert_eq!(err, ClikitError::MissingCaveats);
    }

    #[test]
    fn failure_class_requires_a_governing_error_with_matching_prefix() {
        let err = ResultRecord::builder(Status::NotFound, ["git-tools", "worktree", "remove"])
            .error(Diagnostic::new(
                "conflict.worktree.locked",
                "wrong class",
                Triage::manual("x"),
            ))
            .build()
            .unwrap_err();
        assert!(matches!(err, ClikitError::GoverningCodeMismatch { .. }));
    }

    #[test]
    fn a_valid_success_record_builds_and_canonicalizes() {
        let record = ResultRecord::builder(Status::Success, ["navigator", "search"])
            .data("hits", 3)
            .build()
            .unwrap();
        assert_eq!(record.exit_code, 0);
        let json = record.canonical_json().unwrap();
        assert!(json.contains("\"status\":\"success\""));
        assert!(!json.contains("errors"));
    }
}
