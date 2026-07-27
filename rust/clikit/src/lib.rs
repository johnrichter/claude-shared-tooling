//! `clikit` — the fleet's CLI output, error and exit contract, Rust
//! implementation.
//!
//! One invocation builds **one** [`ResultRecord`] through
//! [`ResultRecordBuilder`], writes it to stdout as canonical JSON, and exits
//! with the paired [`Status::exit_code`] from the closed eleven-member
//! taxonomy. Logging is not this crate's concern: `logkit` owns every log
//! line, and [`log_diagnostic`]/[`log_terminating`] are the one seam this
//! crate defines onto it.
//!
//! Normative spec and schema: `schemas/clikit/` in this repository —
//! `clikit-cli-contract.spec.md` (semantics), `result-record.schema.json`
//! (the record's shape) and `clikit.contract.json` (the taxonomy, triage
//! kinds, reserved codes, bounds and logkit mapping). Any divergence
//! between this crate and those files is a crate defect, not a schema
//! defect.
//!
//! # Example
//! ```
//! use clikit::{Diagnostic, ResultRecord, Status, Triage};
//!
//! let record = ResultRecord::builder(Status::NotFound, ["navigator", "search"])
//!     .error(Diagnostic::new(
//!         "not_found.index.missing",
//!         "no discovery index for this repository",
//!         Triage::reinvoke(["navigator", "index", "build"]),
//!     ))
//!     .build()
//!     .expect("every field here satisfies the schema");
//!
//! assert_eq!(record.exit_code, 40);
//! println!("{}", record.canonical_json().expect("a built record always serializes"));
//! ```

#![forbid(unsafe_code)]

mod diagnostic;
mod error;
mod logging;
mod record;
mod status;
mod triage;
mod validate;

pub use diagnostic::Diagnostic;
pub use error::ClikitError;
pub use logging::{log_diagnostic, log_terminating};
pub use record::{ResultRecord, ResultRecordBuilder, SCHEMA_VERSION};
pub use status::Status;
pub use triage::Triage;

/// This crate's semantic version, read from `Cargo.toml` at compile time.
#[must_use]
pub fn version() -> &'static str {
    env!("CARGO_PKG_VERSION")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn version_matches_cargo_toml() {
        assert_eq!(version(), "0.1.0");
    }

    #[test]
    fn every_status_reaches_a_buildable_record() {
        for status in Status::ALL {
            let mut builder = ResultRecord::builder(status, ["clikit-selftest"]);
            if status.is_failure() {
                builder = builder.error(Diagnostic::new(
                    format!("{}.selftest.x", status.as_str()),
                    "selftest",
                    Triage::manual("n/a"),
                ));
            } else if status == Status::Caveats {
                builder = builder.caveat(Diagnostic::new(
                    "caveats.selftest.x",
                    "selftest",
                    Triage::manual("n/a"),
                ));
            }
            let record = builder
                .build()
                .unwrap_or_else(|e| panic!("{status:?}: {e}"));
            assert_eq!(record.exit_code, status.exit_code());
            record.canonical_json().unwrap();
        }
    }
}
