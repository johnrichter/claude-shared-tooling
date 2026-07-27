//! The one seam clikit owns at the logging boundary
//! (`clikit-cli-contract.spec.md`, "Streams and logkit delegation"): how a
//! [`Diagnostic`] maps onto a logkit record, and the terminating record
//! every clikit CLI writes immediately before exit. Everything else about
//! logging — levels, timestamps, canonical serialization, rendering — is
//! logkit's, consumed here through its public API and never re-implemented.

use logkit::{Level, LogError, Logger};
use serde_json::{Map, Value};

use crate::diagnostic::Diagnostic;
use crate::record::ResultRecord;

/// The reserved `fields.clikit` key logkit records carry for this contract.
const RESERVED_FIELD: &str = "clikit";

/// Logs `diagnostic` at `level` per `clikit.contract.json`'s
/// `logkit.error_mapping`: `error.message` crosses verbatim into logkit's
/// own `error.message` (via [`logkit::EventBuilder::error`], not the log
/// line's own top-level `message` — that stays `summary`, the human
/// sentence describing this log event, distinct from the diagnostic's own
/// wording), `code` becomes `fields.clikit.error_code` (never logkit's
/// `error.kind`, which names the failure's type in the emitting language
/// and may be present alongside), and each `context` member becomes a
/// `fields` member of the same name — nested under the reserved `clikit`
/// key instead when it would collide with a logkit root field name.
/// `triage` is never logged: a directive is for the caller reading the
/// result, not the log stream.
///
/// # Errors
/// Whatever [`logkit::EventBuilder::emit`] returns — a field, `summary` or
/// `error.message` failing logkit's own shape rule.
pub fn log_diagnostic(
    logger: &Logger,
    level: Level,
    summary: impl Into<String>,
    diagnostic: &Diagnostic,
) -> Result<(), LogError> {
    let summary = summary.into();
    let mut event = match level {
        Level::Debug => logger.debug(summary),
        Level::Info => logger.info(summary),
        Level::Warn => logger.warn(summary),
        Level::Error => logger.error(summary),
        Level::Fatal => logger.fatal(summary),
    };
    event = event.error(
        diagnostic.message.clone(),
        None::<String>,
        Vec::<String>::new(),
    );

    let mut clikit_fields = Map::new();
    clikit_fields.insert(
        "error_code".to_string(),
        Value::String(diagnostic.code.clone()),
    );

    if let Some(context) = &diagnostic.context {
        for (key, value) in context {
            if logkit::ROOT_FIELD_NAMES.contains(&key.as_str()) || key == RESERVED_FIELD {
                clikit_fields.insert(key.clone(), value.clone());
            } else {
                event = event.field(key.clone(), value.clone());
            }
        }
    }

    event = event.field(RESERVED_FIELD, Value::Object(clikit_fields));
    event.emit()
}

/// Writes the terminating log record every clikit CLI emits immediately
/// before exit: `fields.clikit.exit_code` and `fields.clikit.status`, at
/// `record.status.log_level()`. Class `internal` logs at logkit's `fatal`,
/// which has no side effect of its own — the process still exits through
/// [`crate::ResultRecord::exit_code`], not through this call.
///
/// # Errors
/// Whatever [`logkit::EventBuilder::emit`] returns.
pub fn log_terminating(
    logger: &Logger,
    record: &ResultRecord,
    message: impl Into<String>,
) -> Result<(), LogError> {
    let level = record.status.log_level();
    let mut event = match level {
        Level::Debug => logger.debug(message.into()),
        Level::Info => logger.info(message.into()),
        Level::Warn => logger.warn(message.into()),
        Level::Error => logger.error(message.into()),
        Level::Fatal => logger.fatal(message.into()),
    };

    let mut clikit_fields = Map::new();
    clikit_fields.insert("exit_code".to_string(), Value::from(record.exit_code));
    clikit_fields.insert(
        "status".to_string(),
        Value::String(record.status.as_str().to_string()),
    );
    if let Some(governing) = record.errors.as_ref().and_then(|errors| errors.first()) {
        clikit_fields.insert(
            "error_code".to_string(),
            Value::String(governing.code.clone()),
        );
    }

    event = event.field(RESERVED_FIELD, Value::Object(clikit_fields));
    event.emit()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::status::Status;
    use crate::triage::Triage;
    use logkit::Sink;
    use std::io::Write;
    use std::sync::{Arc, Mutex};

    #[derive(Clone, Default)]
    struct SharedBuffer(Arc<Mutex<Vec<u8>>>);

    impl Write for SharedBuffer {
        fn write(&mut self, buf: &[u8]) -> std::io::Result<usize> {
            self.0.lock().unwrap().write(buf)
        }
        fn flush(&mut self) -> std::io::Result<()> {
            Ok(())
        }
    }

    impl SharedBuffer {
        fn last_line(&self) -> serde_json::Value {
            let bytes = self.0.lock().unwrap().clone();
            let text = String::from_utf8(bytes).unwrap();
            serde_json::from_str(text.lines().last().unwrap()).unwrap()
        }
    }

    #[test]
    fn terminating_record_carries_exit_code_and_status() {
        let buf = SharedBuffer::default();
        let logger = Logger::builder("navigator")
            .json_writer(Some(Sink::writer(buf.clone())))
            .build()
            .unwrap();
        let record = ResultRecord::builder(Status::NotFound, ["navigator", "search"])
            .error(Diagnostic::new(
                "not_found.index.missing",
                "no such index",
                Triage::manual("build one"),
            ))
            .build()
            .unwrap();

        log_terminating(&logger, &record, "clikit result").unwrap();

        let entry = buf.last_line();
        assert_eq!(entry["fields"]["clikit"]["exit_code"], 40);
        assert_eq!(entry["fields"]["clikit"]["status"], "not_found");
        assert_eq!(
            entry["fields"]["clikit"]["error_code"],
            "not_found.index.missing"
        );
        assert_eq!(entry["level"], "error");
    }

    #[test]
    fn a_colliding_context_key_nests_under_clikit_instead_of_shadowing_a_root_field() {
        let buf = SharedBuffer::default();
        let logger = Logger::builder("navigator")
            .json_writer(Some(Sink::writer(buf.clone())))
            .build()
            .unwrap();
        let diagnostic = Diagnostic::new("usage.flags.bad", "bad flag", Triage::manual("fix it"))
            .context("service", "shadow-attempt");

        log_diagnostic(&logger, Level::Error, "invocation rejected", &diagnostic).unwrap();

        let entry = buf.last_line();
        assert!(entry["fields"].get("service").is_none());
        assert_eq!(entry["fields"]["clikit"]["service"], "shadow-attempt");
        assert_eq!(entry["fields"]["clikit"]["error_code"], "usage.flags.bad");
        assert_eq!(entry["error"]["message"], "bad flag");
        assert_eq!(entry["message"], "invocation rejected");
    }
}
