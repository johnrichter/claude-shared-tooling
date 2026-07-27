//! The closed eleven-member outcome class: `schemas/clikit/result-record.schema.json`'s
//! `status`/`exit_code` enums given one Rust type, so the pairing the schema
//! pins can never drift in this crate's own output.

use logkit::Level;
use serde::{Serialize, Serializer};

/// One of the eleven outcome classes, in the taxonomy's own order.
///
/// A `Status` value is unconditionally one of the eleven — the closed-enum
/// pattern also used by logkit's `Level` — so matching it exhaustively (as
/// every caller must, per the contract's versioning rules) is enforced by
/// the compiler, not by a runtime check.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum Status {
    /// Did what was asked; the result is complete and unqualified.
    Success,
    /// Did what was asked and the result is usable, but qualified.
    Caveats,
    /// An expected negative: asked a question about something that exists,
    /// and the answer is no.
    GateNegative,
    /// The state the operation requires is not in place; the operation was
    /// not attempted.
    PreconditionUnmet,
    /// A subject the caller named does not exist.
    NotFound,
    /// The subject exists in a state incompatible with the request.
    Conflict,
    /// The invocation itself is wrong. Nothing was attempted.
    Usage,
    /// An identical re-invocation may resolve it, with no change by anyone.
    Transient,
    /// Access is refused, and an identical re-invocation will be refused
    /// again.
    Permission,
    /// A well-formed request this tool does not serve, by scope, platform
    /// or version.
    Unsupported,
    /// The tool itself failed, or produced an outcome it cannot classify.
    Internal,
}

impl Status {
    /// Every class, in the schema's `status`/`exit_code` enum order —
    /// walking this array is how a caller (or this crate's own tests)
    /// reaches all eleven classes without hand-listing them twice.
    pub const ALL: [Status; 11] = [
        Status::Success,
        Status::Caveats,
        Status::GateNegative,
        Status::PreconditionUnmet,
        Status::NotFound,
        Status::Conflict,
        Status::Usage,
        Status::Transient,
        Status::Permission,
        Status::Unsupported,
        Status::Internal,
    ];

    /// The wire token — the record's own `status` value and a diagnostic
    /// code's required first segment (`success` excepted: class 0 carries
    /// no diagnostics).
    #[must_use]
    pub const fn as_str(self) -> &'static str {
        match self {
            Status::Success => "success",
            Status::Caveats => "caveats",
            Status::GateNegative => "gate_negative",
            Status::PreconditionUnmet => "precondition_unmet",
            Status::NotFound => "not_found",
            Status::Conflict => "conflict",
            Status::Usage => "usage",
            Status::Transient => "transient",
            Status::Permission => "permission",
            Status::Unsupported => "unsupported",
            Status::Internal => "internal",
        }
    }

    /// The paired integer, per the schema's root `allOf` (one branch per
    /// class, pinning `status` to `exit_code`). Never compared with `<` or
    /// `>` — see the contract's "classification, not an ordering" rule.
    #[must_use]
    pub const fn exit_code(self) -> u16 {
        match self {
            Status::Success => 0,
            Status::Caveats => 10,
            Status::GateNegative => 20,
            Status::PreconditionUnmet => 30,
            Status::NotFound => 40,
            Status::Conflict => 41,
            Status::Usage => 50,
            Status::Transient => 60,
            Status::Permission => 70,
            Status::Unsupported => 80,
            Status::Internal => 90,
        }
    }

    /// Whether this class is `success` or `caveats` — the two classes with
    /// no `errors` array — versus a failure class, all nine of which
    /// require one.
    #[must_use]
    pub const fn is_failure(self) -> bool {
        !matches!(self, Status::Success | Status::Caveats)
    }

    /// The level a CLI's terminating logkit record logs at for this class
    /// (`clikit.contract.json`'s `exit_taxonomy.classes[].log_level`): an
    /// expected negative is `info`, a qualified success is `warn`, every
    /// caller- or environment-caused failure is `error`, and the tool
    /// failing outright is `fatal`.
    #[must_use]
    pub const fn log_level(self) -> Level {
        match self {
            Status::Success | Status::GateNegative => Level::Info,
            Status::Caveats => Level::Warn,
            Status::Internal => Level::Fatal,
            _ => Level::Error,
        }
    }
}

impl Serialize for Status {
    fn serialize<S: Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        serializer.serialize_str(self.as_str())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn exit_codes_match_the_schema_enum_order() {
        let codes: Vec<u16> = Status::ALL.iter().map(|s| s.exit_code()).collect();
        assert_eq!(codes, vec![0, 10, 20, 30, 40, 41, 50, 60, 70, 80, 90]);
    }

    #[test]
    fn only_success_and_caveats_are_non_failure() {
        for status in Status::ALL {
            assert_eq!(
                !status.is_failure(),
                matches!(status, Status::Success | Status::Caveats)
            );
        }
    }
}
