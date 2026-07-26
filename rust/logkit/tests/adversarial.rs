//! Adversarial checks beyond the implementer's own suite: fields the
//! schema constrains but that aren't exercised by unit tests, and
//! properties that should hold across every level / concurrent emitters.

use std::io::{self, Write};
use std::sync::{Arc, Mutex};

use logkit::{Level, LogError, Logger, Sink};

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

/// The schema's `service_version` pattern excludes control characters, the
/// same as `service`/`message`/etc. `service_version` is a construction-time
/// value, so — like `service` — `LoggerBuilder::build` validates it and fails
/// closed rather than letting a control-char version reach the wire on every
/// subsequent emit. Fail-loud, never sanitize: a bad version is refused, not
/// stripped.
#[test]
fn invalid_service_version_is_rejected_at_build() {
    let bad_version = "0.4.2\u{0007}has-a-bell-char";
    let result = Logger::builder("navigator")
        .service_version(bad_version)
        .json_writer(Some(Sink::writer(SharedBuffer::default())))
        .build();

    assert!(
        matches!(
            result,
            Err(LogError::InvalidValue {
                field: "service_version",
                ..
            })
        ),
        "a control-char service_version must be refused at build, not silently emitted"
    );
}

/// Every one of the five canonical levels round-trips through
/// `Logger` -> JSON -> parsed `level` field, independent of the golden
/// fixture (which only covers info/debug/error/fatal, not warn).
#[test]
fn all_five_levels_round_trip_through_the_json_line() {
    let json_buf = SharedBuffer::default();
    let logger = Logger::builder("navigator")
        .threshold(Level::Debug)
        .json_writer(Some(Sink::writer(json_buf.clone())))
        .build()
        .unwrap();

    logger.debug("d").emit().unwrap();
    logger.info("i").emit().unwrap();
    logger.warn("w").emit().unwrap();
    logger.error("e").emit().unwrap();
    logger.fatal("f").emit().unwrap();

    let levels: Vec<String> = json_buf
        .lines()
        .iter()
        .map(|line| {
            let v: serde_json::Value = serde_json::from_str(line).unwrap();
            v["level"].as_str().unwrap().to_string()
        })
        .collect();
    assert_eq!(levels, vec!["debug", "info", "warn", "error", "fatal"]);
}

/// Concurrent emitters through one `Logger` never interleave a partial
/// line: every line landing in the sink parses as standalone JSON.
#[test]
fn concurrent_emitters_never_interleave_a_line() {
    let json_buf = SharedBuffer::default();
    let logger = Arc::new(logger_over_plain(json_buf.clone()));

    let handles: Vec<_> = (0..16)
        .map(|i| {
            let logger = Arc::clone(&logger);
            std::thread::spawn(move || {
                for j in 0..50 {
                    logger
                        .info(format!("thread {i} message {j}"))
                        .field("i", i)
                        .field("j", j)
                        .emit()
                        .unwrap();
                }
            })
        })
        .collect();
    for h in handles {
        h.join().unwrap();
    }

    let lines = json_buf.lines();
    assert_eq!(lines.len(), 16 * 50);
    for line in &lines {
        serde_json::from_str::<serde_json::Value>(line)
            .unwrap_or_else(|e| panic!("non-JSON / interleaved line {line:?}: {e}"));
    }
}

fn logger_over_plain(json: SharedBuffer) -> Logger {
    Logger::builder("navigator")
        .json_writer(Some(Sink::writer(json)))
        .build()
        .unwrap()
}

/// `message` at exactly the schema's 4096-character ceiling is accepted;
/// one character past it is rejected. Only the interior boundary was
/// exercised by the implementer's own tests (empty / with-newline).
#[test]
fn message_length_boundary_is_enforced_exactly() {
    let logger = logger_over_plain(SharedBuffer::default());
    let at_limit = "a".repeat(4096);
    let over_limit = "a".repeat(4097);

    assert!(logger.info(at_limit).emit().is_ok());
    assert!(logger.info(over_limit).emit().is_err());
}
