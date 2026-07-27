//! `logkit` — the fleet's cross-language structured logging standard, Rust
//! implementation.
//!
//! One [`Logger::info`]/`debug`/`warn`/`error`/`fatal` call builds **one**
//! normalized [`Record`] and renders it as one line of canonical JSON for
//! machines and, optionally, one line for a person — both from the same
//! in-memory record, so their values agree by construction. Go, Rust and
//! Python implementations of this standard emit the same bytes for the
//! same event.
//!
//! Normative spec and schema: `schemas/logkit/` in this repository —
//! `logkit-logging.spec.md` (semantics), `log-record.schema.json` (the
//! record's shape) and `logkit.contract.json` (serialization, timestamp,
//! level-alias and rendering data). Any divergence between this crate and
//! those files is a crate defect, not a schema defect.
//!
//! # `tracing` is a writer, not the record
//! `tracing`/`tracing-subscriber` move the two already-rendered lines from
//! an event to their configured sink; they never decide a field name, a
//! level or a timestamp format. See [`Logger`]'s own module for the exact
//! seam.
//!
//! # Example
//! ```
//! use logkit::{Level, Logger, Sink};
//!
//! let logger = Logger::builder("navigator")
//!     .service_version("0.4.2")
//!     .threshold(Level::Debug)
//!     .human_writer(Some(Sink::writer(std::io::sink())))
//!     .build()
//!     .expect("service name is valid");
//!
//! logger
//!     .debug("index segment written")
//!     .field("documents", 1284)
//!     .field("dry_run", false)
//!     .caller("rust/bm25/src/index.rs", 312, Some("OkapiIndex::rebuild"))
//!     .emit()
//!     .expect("event fields are all schema-valid");
//! ```

#![forbid(unsafe_code)]

mod error;
mod level;
mod logger;
mod record;
mod render;
mod timestamp;

pub use error::LogError;
pub use level::{normalize, Level, NormalizedLevel, UnknownLevel};
pub use logger::{EventBuilder, Logger, LoggerBuilder, Sink};
pub use record::{CallerInfo, ErrorInfo, Record, ROOT_FIELD_NAMES, SCHEMA_VERSION};

/// Renders `record` as its human line, without a [`Logger`] — useful for a
/// caller that already has a [`Record`] (read back from a file, built for a
/// test) and wants the same rendering [`Logger`] would have produced.
#[must_use]
pub fn human_line(record: &Record, color: bool) -> String {
    render::render(record, color)
}

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
}
