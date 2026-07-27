//! The human rendering: one line per record, for a terminal, derived from
//! the same [`Record`] the JSON rendering comes from. Per the spec, nothing
//! parses this — it exists only for a person reading a log.
//!
//! Layout, from `schemas/logkit/logkit.contract.json`'s `human_rendering`:
//! `<timestamp> <LEVEL padded to 5> <service> <message>[ <attribute>]...`,
//! with attributes in the record's own canonical (alphabetical) root key
//! order, `fields` expanded in place and `schema_version` omitted.

use std::fmt::Write as _;

use serde_json::Value;

use crate::level::Level;
use crate::record::Record;

/// SGR (ANSI) code for a level token, applied only when a caller has opted
/// into color (TTY sink, `NO_COLOR` unset, configuration allows it — that
/// decision belongs to [`crate::Logger`], not this function).
fn sgr(level: Level) -> &'static str {
    match level {
        Level::Debug => "90",
        Level::Info => "36",
        Level::Warn => "33",
        Level::Error => "31",
        Level::Fatal => "1;31",
    }
}

/// Renders `record` as one human line (plus, when `error.stack` is present,
/// one further indented line per frame). `color` wraps the level token in
/// its SGR code and resets after — no other character changes, so stripping
/// the escapes recovers this same string with `color: false`.
#[must_use]
pub fn render(record: &Record, color: bool) -> String {
    let value = record.to_value();
    let root = value
        .as_object()
        .expect("Record::to_value always serializes to a JSON object");

    let level_token = format!("{:<5}", record.level.as_str().to_uppercase());
    let level_token = if color {
        format!("\x1b[{}m{level_token}\x1b[0m", sgr(record.level))
    } else {
        level_token
    };

    let mut line = format!(
        "{} {} {} {}",
        record.timestamp, level_token, record.service, record.message
    );
    let mut stack_lines: Vec<String> = Vec::new();

    // `root` is a BTreeMap (this crate never enables serde_json's
    // preserve_order feature), so this iterates every ASCII root key in
    // the same alphabetical order JCS itself sorts by: caller, error,
    // fields, level, message, schema_version, service, service_version,
    // timestamp. The four already in the header, plus schema_version,
    // are skipped below; what's left is exactly the attribute order the
    // contract calls for.
    for (key, val) in root {
        match key.as_str() {
            "caller" => render_caller(&mut line, val),
            "error" => render_error(&mut line, &mut stack_lines, val),
            "fields" => render_fields(&mut line, val),
            "service_version" => {
                line.push_str(" service_version=");
                line.push_str(&render_value(val));
            }
            _ => {}
        }
    }

    for frame in stack_lines {
        line.push('\n');
        line.push_str("  ");
        line.push_str(&frame);
    }

    line
}

fn render_caller(line: &mut String, caller: &Value) {
    let file = caller["file"].as_str().unwrap_or_default();
    let call_line = caller["line"].as_u64().unwrap_or_default();
    let _ = write!(line, " caller={file}:{call_line}");
    if let Some(function) = caller.get("function").and_then(Value::as_str) {
        line.push_str(" caller_function=");
        line.push_str(&render_value(&Value::String(function.to_string())));
    }
}

fn render_error(line: &mut String, stack_lines: &mut Vec<String>, error: &Value) {
    let message = error["message"].as_str().unwrap_or_default();
    line.push_str(" error=");
    line.push_str(&render_value(&Value::String(message.to_string())));
    if let Some(kind) = error.get("kind").and_then(Value::as_str) {
        line.push_str(" error_kind=");
        line.push_str(&render_value(&Value::String(kind.to_string())));
    }
    if let Some(stack) = error.get("stack").and_then(Value::as_array) {
        stack_lines.extend(stack.iter().filter_map(|f| f.as_str()).map(str::to_string));
    }
}

fn render_fields(line: &mut String, fields: &Value) {
    let Some(map) = fields.as_object() else {
        return;
    };
    for (key, val) in map {
        line.push(' ');
        line.push_str(key);
        line.push('=');
        line.push_str(&render_value(val));
    }
}

/// A scalar string renders bare when it has no whitespace, `"`, `=` or
/// control character; every other value — a quoted string, a number, a
/// bool, `null`, an array or object — renders as its own canonical JSON
/// form, which for a non-string scalar already carries no such character.
fn render_value(value: &Value) -> String {
    match value {
        Value::String(s) if is_bare(s) => s.clone(),
        other => serde_jcs::to_string(other).expect("field value is already valid JSON"),
    }
}

fn is_bare(s: &str) -> bool {
    !s.is_empty()
        && s.chars()
            .all(|c| !c.is_whitespace() && c != '"' && c != '=' && !c.is_control())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::record::{CallerInfo, ErrorInfo, SCHEMA_VERSION};
    use serde_json::json;

    fn base() -> Record {
        Record {
            schema_version: SCHEMA_VERSION,
            timestamp: "2026-07-26T09:41:07.480Z".to_string(),
            level: Level::Info,
            service: "navigator".to_string(),
            message: "index rebuilt".to_string(),
            service_version: None,
            fields: None,
            error: None,
            caller: None,
        }
    }

    #[test]
    fn header_only_record_pads_the_level_token() {
        assert_eq!(
            render(&base(), false),
            "2026-07-26T09:41:07.480Z INFO  navigator index rebuilt"
        );
    }

    #[test]
    fn a_value_with_whitespace_renders_quoted() {
        let mut record = base();
        let mut fields = serde_json::Map::new();
        fields.insert("note".to_string(), json!("two words"));
        record.fields = Some(fields);
        assert!(render(&record, false).ends_with(r#"note="two words""#));
    }

    #[test]
    fn error_and_caller_and_service_version_follow_alphabetical_root_order() {
        let mut record = base();
        record.service_version = Some("0.4.2".to_string());
        record.caller = Some(CallerInfo {
            file: "rust/bm25/src/index.rs".to_string(),
            line: 312,
            function: Some("OkapiIndex::rebuild".to_string()),
        });
        record.error = Some(ErrorInfo {
            message: "boom".to_string(),
            kind: None,
            stack: None,
        });
        let rendered = render(&record, false);
        let caller_pos = rendered.find("caller=").unwrap();
        let error_pos = rendered.find("error=").unwrap();
        let version_pos = rendered.find("service_version=").unwrap();
        assert!(caller_pos < error_pos && error_pos < version_pos);
    }

    #[test]
    fn stack_frames_render_on_indented_lines_after_the_record() {
        let mut record = base();
        record.error = Some(ErrorInfo {
            message: "boom".to_string(),
            kind: None,
            stack: Some(vec!["a.rs:1 f".to_string(), "b.rs:2 g".to_string()]),
        });
        let rendered = render(&record, false);
        let lines: Vec<&str> = rendered.lines().collect();
        assert_eq!(lines.len(), 3);
        assert_eq!(lines[1], "  a.rs:1 f");
        assert_eq!(lines[2], "  b.rs:2 g");
    }

    #[test]
    fn color_wraps_only_the_level_token() {
        let colored = render(&base(), true);
        let colorless = render(&base(), false);
        let stripped = colored.replace("\x1b[36m", "").replace("\x1b[0m", "");
        assert_eq!(stripped, colorless);
    }
}
