//! [`Logger`]: the public entry point. Builds a normalized [`Record`],
//! canonicalizes it and renders the human line, then hands both finished
//! lines to `tracing` purely as the byte-moving pipeline — dispatch, level
//! filtering plumbing, the [`Layer`] trait. `tracing` never sees the
//! record's own field names or level set; it only ever carries two already-
//! rendered strings from the event macro to [`RecordLayer::on_event`].

use std::fmt;
use std::io::{self, Write};
use std::sync::{Arc, Mutex};

use serde_json::{Map, Value};
use tracing::field::{Field, Visit};
use tracing::Subscriber;
use tracing_subscriber::layer::{Context, SubscriberExt};
use tracing_subscriber::Layer;

use crate::error::LogError;
use crate::level::Level;
use crate::record::{self, CallerInfo, ErrorInfo, Record};
use crate::render;
use crate::timestamp;

/// A destination for a rendered line: an `Arc<Mutex<_>>` around anything
/// `Write + Send`, so a [`Logger`] and its writer(s) can be cloned/shared
/// freely and the JSON and human sinks can be two different streams.
#[derive(Clone)]
pub struct Sink(Arc<Mutex<dyn Write + Send>>);

impl Sink {
    /// The contract's default stream — the machine JSON rendering's home so
    /// a log line never lands on stdout, which carries a CLI's structured
    /// result under the separate `clikit` contract.
    #[must_use]
    pub fn stderr() -> Self {
        Self(Arc::new(Mutex::new(io::stderr())))
    }

    /// Rarely correct for a log line (see [`Sink::stderr`]); exists for a
    /// caller with its own reason to route logs to stdout.
    #[must_use]
    pub fn stdout() -> Self {
        Self(Arc::new(Mutex::new(io::stdout())))
    }

    /// Any other destination — a file, an in-memory buffer for tests, a
    /// channel adapter.
    #[must_use]
    pub fn writer(writer: impl Write + Send + 'static) -> Self {
        Self(Arc::new(Mutex::new(writer)))
    }

    /// Writes `line` plus a single trailing `LF` in one `write_all` call,
    /// so concurrent emitters can never interleave a partial line — the
    /// contract's `line_framing`/`write_atomicity` rules. A poisoned lock
    /// (a prior writer panicked mid-write) drops the line rather than
    /// panicking the caller in turn; logging must never be the reason a
    /// process crashes.
    fn write_line(&self, line: &str) {
        let Ok(mut writer) = self.0.lock() else {
            return;
        };
        let mut buf = Vec::with_capacity(line.len() + 1);
        buf.extend_from_slice(line.as_bytes());
        buf.push(b'\n');
        let _ = writer.write_all(&buf);
    }
}

/// Builds a [`Logger`]. Defaults: [`Level::Info`] threshold, JSON to
/// [`Sink::stderr`], no human rendering, no color — a caller opts into the
/// human line and color explicitly.
pub struct LoggerBuilder {
    service: String,
    service_version: Option<String>,
    threshold: Level,
    json: Option<Sink>,
    human: Option<Sink>,
    color: bool,
}

impl LoggerBuilder {
    fn new(service: impl Into<String>) -> Self {
        Self {
            service: service.into(),
            service_version: None,
            threshold: Level::Info,
            json: Some(Sink::stderr()),
            human: None,
            color: false,
        }
    }

    /// Version of the emitting build, carried on every record this
    /// [`Logger`] emits.
    #[must_use]
    pub fn service_version(mut self, version: impl Into<String>) -> Self {
        self.service_version = Some(version.into());
        self
    }

    /// A record is emitted when `severity(level) >= severity(threshold)`.
    /// `Level::Fatal` cannot be filtered out — no threshold ranks above it.
    #[must_use]
    pub fn threshold(mut self, threshold: Level) -> Self {
        self.threshold = threshold;
        self
    }

    /// Overrides the JSON sink (default [`Sink::stderr`]). `None` disables
    /// the JSON rendering entirely.
    #[must_use]
    pub fn json_writer(mut self, sink: Option<Sink>) -> Self {
        self.json = sink;
        self
    }

    /// Enables (or disables, with `None`) the human rendering, to this
    /// sink. Disabled by default: the JSON rendering is the wire contract,
    /// the human rendering is an opt-in for a terminal.
    #[must_use]
    pub fn human_writer(mut self, sink: Option<Sink>) -> Self {
        self.human = sink;
        self
    }

    /// Enables SGR color on the human rendering's level token. A caller
    /// decides TTY-ness and `NO_COLOR`; this crate only applies the escape
    /// codes once asked to.
    #[must_use]
    pub fn color(mut self, enabled: bool) -> Self {
        self.color = enabled;
        self
    }

    /// Validates `service` and, when set, `service_version` against their
    /// schema patterns, then wires the `tracing` dispatch this [`Logger`]
    /// emits through. Both are construction-time values, so validating once
    /// here keeps a control-char or overlong version off every emitted
    /// record without re-checking an immutable value on each emit.
    ///
    /// # Errors
    /// [`LogError::InvalidValue`] if `service` doesn't match
    /// `^[a-z0-9][a-z0-9._-]*$` or is empty/over 64 characters, or if
    /// `service_version` is empty, over 64 characters, or carries a control
    /// character.
    pub fn build(self) -> Result<Logger, LogError> {
        record::validate_service(&self.service)?;
        if let Some(version) = &self.service_version {
            record::validate_line("service_version", version, 64)?;
        }
        let layer = RecordLayer {
            json: self.json,
            human: self.human,
        };
        let subscriber = tracing_subscriber::registry().with(layer);
        Ok(Logger {
            service: self.service,
            service_version: self.service_version,
            threshold: self.threshold,
            color: self.color,
            dispatch: tracing::Dispatch::new(subscriber),
        })
    }
}

/// A configured emitter for one `service`. Cheap to hold: cloning the
/// writers underneath is an `Arc` bump, and `Logger` itself is `Send +
/// Sync` so one instance can be shared across threads.
pub struct Logger {
    service: String,
    service_version: Option<String>,
    threshold: Level,
    color: bool,
    dispatch: tracing::Dispatch,
}

impl Logger {
    /// Starts building a [`Logger`] for `service` — the CLI, daemon or job
    /// name, fixed for this logger's lifetime. A sub-component within it
    /// belongs in a record's `fields`, never as a second logger.
    #[must_use]
    pub fn builder(service: impl Into<String>) -> LoggerBuilder {
        LoggerBuilder::new(service)
    }

    /// Starts a [`Level::Debug`] event.
    pub fn debug(&self, message: impl Into<String>) -> EventBuilder<'_> {
        self.event(Level::Debug, message)
    }

    /// Starts a [`Level::Info`] event.
    pub fn info(&self, message: impl Into<String>) -> EventBuilder<'_> {
        self.event(Level::Info, message)
    }

    /// Starts a [`Level::Warn`] event.
    pub fn warn(&self, message: impl Into<String>) -> EventBuilder<'_> {
        self.event(Level::Warn, message)
    }

    /// Starts a [`Level::Error`] event.
    pub fn error(&self, message: impl Into<String>) -> EventBuilder<'_> {
        self.event(Level::Error, message)
    }

    /// `fatal` has no side effect of its own: this writes the record and
    /// returns like any other level. The caller terminates the process
    /// through its own exit-code taxonomy, not this call.
    pub fn fatal(&self, message: impl Into<String>) -> EventBuilder<'_> {
        self.event(Level::Fatal, message)
    }

    fn event(&self, level: Level, message: impl Into<String>) -> EventBuilder<'_> {
        EventBuilder {
            logger: self,
            level,
            message: message.into(),
            fields: Map::new(),
            error: None,
            caller: None,
        }
    }
}

/// One event under construction. `message` is fixed at the `logger.<level>`
/// call; everything else is added here and finalized by [`EventBuilder::emit`].
#[must_use = "an event is only recorded once `.emit()` is called"]
pub struct EventBuilder<'a> {
    logger: &'a Logger,
    level: Level,
    message: String,
    fields: Map<String, Value>,
    error: Option<ErrorInfo>,
    caller: Option<CallerInfo>,
}

impl EventBuilder<'_> {
    /// Adds one `fields` entry — the variable data that would otherwise be
    /// interpolated into `message`. A key written twice keeps the last
    /// value, matching the contract's last-write-wins rule for a record.
    pub fn field(mut self, key: impl Into<String>, value: impl Into<Value>) -> Self {
        self.fields.insert(key.into(), value.into());
        self
    }

    /// Attaches the failure that caused this event. Independent of level:
    /// call this on a `warn` too, or skip it on an `error`.
    pub fn error<K, S>(mut self, message: impl Into<String>, kind: Option<K>, stack: Vec<S>) -> Self
    where
        K: Into<String>,
        S: Into<String>,
    {
        self.error = Some(ErrorInfo {
            message: message.into(),
            kind: kind.map(Into::into),
            stack: (!stack.is_empty()).then(|| stack.into_iter().map(Into::into).collect()),
        });
        self
    }

    /// Attaches the log call's source location.
    pub fn caller<F>(mut self, file: impl Into<String>, line: u32, function: Option<F>) -> Self
    where
        F: Into<String>,
    {
        self.caller = Some(CallerInfo {
            file: file.into(),
            line,
            function: function.map(Into::into),
        });
        self
    }

    /// Validates, normalizes into a [`Record`] and emits it. A record below
    /// the logger's threshold is dropped here — cheaply, before any
    /// validation or rendering work.
    ///
    /// # Errors
    /// A named [`LogError`] when `message`, a field key, `error.message`,
    /// `error.kind`, a stack frame, `caller.file` or `caller.function`
    /// fails the schema's own shape rule. Never defaults, never drops the
    /// offending key to make the rest pass.
    ///
    /// # Panics
    /// Never in practice: every field reaching [`Record::canonical_json`]
    /// here already passed this function's own validation above.
    pub fn emit(self) -> Result<(), LogError> {
        let EventBuilder {
            logger,
            level,
            message,
            fields,
            error,
            caller,
        } = self;

        if level.severity() < logger.threshold.severity() {
            return Ok(());
        }

        record::validate_line("message", &message, 4096)?;
        for key in fields.keys() {
            record::validate_field_key(key)?;
        }
        if let Some(err) = &error {
            record::validate_line("error.message", &err.message, 4096)?;
            if let Some(kind) = &err.kind {
                record::validate_line("error.kind", kind, 128)?;
            }
            if let Some(stack) = &err.stack {
                for frame in stack {
                    record::validate_line("error.stack frame", frame, 4096)?;
                }
            }
        }
        if let Some(c) = &caller {
            record::validate_caller_file(&c.file)?;
            if let Some(function) = &c.function {
                record::validate_line("caller.function", function, 256)?;
            }
        }

        let built = Record {
            schema_version: record::SCHEMA_VERSION,
            timestamp: timestamp::now(),
            level,
            service: logger.service.clone(),
            message,
            service_version: logger.service_version.clone(),
            fields: (!fields.is_empty()).then_some(fields),
            error,
            caller,
        };

        let json_line = built
            .canonical_json()
            .expect("a validated Record always serializes to canonical JSON");
        let human_line = render::render(&built, logger.color);

        emit_via_tracing(&logger.dispatch, level, &json_line, &human_line);
        Ok(())
    }
}

/// `tracing::event!`'s level argument must be a compile-time constant, so
/// this is a five-way dispatch rather than a single call with `level`
/// passed through. `Level::Fatal` has no `tracing::Level` counterpart (see
/// `schemas/logkit/logkit.contract.json`'s rust `non_equivalences`) and
/// dispatches at `ERROR` — the record's own `level` field, not this choice,
/// is what a consumer reads.
fn emit_via_tracing(dispatch: &tracing::Dispatch, level: Level, json_line: &str, human_line: &str) {
    tracing::dispatcher::with_default(dispatch, || match level {
        Level::Debug => {
            tracing::event!(target: "logkit", tracing::Level::DEBUG, logkit_json = json_line, logkit_human = human_line);
        }
        Level::Info => {
            tracing::event!(target: "logkit", tracing::Level::INFO, logkit_json = json_line, logkit_human = human_line);
        }
        Level::Warn => {
            tracing::event!(target: "logkit", tracing::Level::WARN, logkit_json = json_line, logkit_human = human_line);
        }
        Level::Error | Level::Fatal => {
            tracing::event!(target: "logkit", tracing::Level::ERROR, logkit_json = json_line, logkit_human = human_line);
        }
    });
}

/// The `tracing_subscriber::Layer` that is this crate's entire use of
/// `tracing` as a writer: it reads the two pre-rendered lines off the event
/// and writes each to its configured [`Sink`], verbatim. It formats
/// nothing — [`crate::record`] and [`crate::render`] already did that.
struct RecordLayer {
    json: Option<Sink>,
    human: Option<Sink>,
}

impl<S: Subscriber> Layer<S> for RecordLayer {
    fn on_event(&self, event: &tracing::Event<'_>, _ctx: Context<'_, S>) {
        let mut visitor = LineVisitor::default();
        event.record(&mut visitor);
        if let (Some(sink), Some(line)) = (&self.json, visitor.json) {
            sink.write_line(&line);
        }
        if let (Some(sink), Some(line)) = (&self.human, visitor.human) {
            sink.write_line(&line);
        }
    }
}

#[derive(Default)]
struct LineVisitor {
    json: Option<String>,
    human: Option<String>,
}

impl Visit for LineVisitor {
    fn record_str(&mut self, field: &Field, value: &str) {
        match field.name() {
            "logkit_json" => self.json = Some(value.to_string()),
            "logkit_human" => self.human = Some(value.to_string()),
            _ => {}
        }
    }

    fn record_debug(&mut self, _field: &Field, _value: &dyn fmt::Debug) {}
}

#[cfg(test)]
mod tests {
    use super::*;

    #[derive(Clone, Default)]
    struct SharedBuffer(Arc<Mutex<Vec<u8>>>);

    impl Write for SharedBuffer {
        fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
            self.0.lock().unwrap().write(buf)
        }
        fn flush(&mut self) -> io::Result<()> {
            Ok(())
        }
    }

    impl SharedBuffer {
        fn lines(&self) -> Vec<String> {
            String::from_utf8(self.0.lock().unwrap().clone())
                .unwrap()
                .lines()
                .map(str::to_string)
                .collect()
        }
    }

    fn logger_over(json: SharedBuffer, human: SharedBuffer) -> Logger {
        Logger::builder("navigator")
            .json_writer(Some(Sink::writer(json)))
            .human_writer(Some(Sink::writer(human)))
            .build()
            .unwrap()
    }

    #[test]
    fn one_call_produces_both_renderings_from_the_same_values() {
        let json_buf = SharedBuffer::default();
        let human_buf = SharedBuffer::default();
        let logger = logger_over(json_buf.clone(), human_buf.clone());

        logger.info("index rebuilt").emit().unwrap();

        let json_line = &json_buf.lines()[0];
        let human_line = &human_buf.lines()[0];
        let parsed: serde_json::Value = serde_json::from_str(json_line).unwrap();
        assert_eq!(parsed["message"], "index rebuilt");
        assert_eq!(parsed["service"], "navigator");
        assert!(human_line.contains("index rebuilt"));
        assert!(human_line.contains("navigator"));
        // Same timestamp in both renderings, because both come from one Record.
        assert!(human_line.starts_with(parsed["timestamp"].as_str().unwrap()));
    }

    #[test]
    fn below_threshold_events_are_dropped_before_rendering() {
        let json_buf = SharedBuffer::default();
        let human_buf = SharedBuffer::default();
        let logger = Logger::builder("navigator")
            .threshold(Level::Warn)
            .json_writer(Some(Sink::writer(json_buf.clone())))
            .human_writer(Some(Sink::writer(human_buf.clone())))
            .build()
            .unwrap();

        logger.info("skipped").emit().unwrap();
        logger.warn("kept").emit().unwrap();

        assert_eq!(json_buf.lines().len(), 1);
        assert!(json_buf.lines()[0].contains("kept"));
    }

    #[test]
    fn fatal_writes_and_returns_with_no_process_side_effect() {
        let json_buf = SharedBuffer::default();
        let human_buf = SharedBuffer::default();
        let logger = logger_over(json_buf.clone(), human_buf);

        logger.fatal("state store unreadable").emit().unwrap();

        let parsed: serde_json::Value = serde_json::from_str(&json_buf.lines()[0]).unwrap();
        assert_eq!(parsed["level"], "fatal");
    }

    #[test]
    fn a_field_key_colliding_with_a_root_name_is_a_named_error() {
        let logger = logger_over(SharedBuffer::default(), SharedBuffer::default());
        let err = logger
            .info("oops")
            .field("service", "x")
            .emit()
            .unwrap_err();
        assert!(matches!(err, LogError::ReservedFieldName { .. }));
    }

    #[test]
    fn building_with_an_invalid_service_name_fails_closed() {
        match Logger::builder("Not Valid").build() {
            Err(LogError::InvalidValue {
                field: "service", ..
            }) => {}
            other => panic!("expected an invalid-service error, got {}", other.is_ok()),
        }
    }
}
