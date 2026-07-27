//! Byte-exact conformance against `schemas/logkit/examples/`: this crate's
//! own [`logkit::Record`], built directly (bypassing the wall clock so the
//! timestamp matches the fixture), must canonicalize and render to exactly
//! these bytes. This is the cross-language interop surface: the same four
//! records, built the same way in Go and Python, produce these same lines.

use logkit::{human_line, CallerInfo, ErrorInfo, Level, Record, SCHEMA_VERSION};
use serde_json::{json, Map};

const GOLDEN_JSONL: &str = include_str!("../../../schemas/logkit/examples/golden-records.jsonl");
const GOLDEN_HUMAN: &str =
    include_str!("../../../schemas/logkit/examples/golden-records.human.txt");

fn golden_records() -> Vec<Record> {
    vec![
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
        },
        Record {
            schema_version: SCHEMA_VERSION,
            timestamp: "2026-07-26T09:41:07.612Z".to_string(),
            level: Level::Debug,
            service: "navigator".to_string(),
            message: "index segment written".to_string(),
            service_version: Some("0.4.2".to_string()),
            fields: Some({
                let mut fields = Map::new();
                fields.insert("documents".to_string(), json!(1284));
                fields.insert("dry_run".to_string(), json!(false));
                fields.insert("duration_ms".to_string(), json!(812.5));
                fields.insert("index_path".to_string(), json!("var/index/navigator.bm25"));
                fields.insert("tags".to_string(), json!(["frontmatter", "bm25"]));
                fields.insert(
                    "window".to_string(),
                    json!({"from": "2026-07-01", "to": "2026-07-26"}),
                );
                fields
            }),
            error: None,
            caller: Some(CallerInfo {
                file: "rust/bm25/src/index.rs".to_string(),
                line: 312,
                function: Some("OkapiIndex::rebuild".to_string()),
            }),
        },
        Record {
            schema_version: SCHEMA_VERSION,
            timestamp: "2026-07-26T09:44:12.007Z".to_string(),
            level: Level::Error,
            service: "git-tools".to_string(),
            message: "worktree create refused".to_string(),
            service_version: None,
            fields: Some({
                let mut fields = Map::new();
                fields.insert("branch".to_string(), json!("feat/x"));
                fields.insert("exit_code".to_string(), json!(128));
                fields
            }),
            error: Some(ErrorInfo {
                message: "fatal: 'feat/x' is already checked out at '/w/a'".to_string(),
                kind: Some("git.ExitError".to_string()),
                stack: Some(vec![
                    "git/worktree.go:88 Create".to_string(),
                    "git/cmd.go:41 run".to_string(),
                ]),
            }),
            caller: None,
        },
        Record {
            schema_version: SCHEMA_VERSION,
            timestamp: "2026-07-26T09:45:00.000Z".to_string(),
            level: Level::Fatal,
            service: "anoikis-tools".to_string(),
            message: "state store unreadable".to_string(),
            service_version: None,
            fields: Some({
                let mut fields = Map::new();
                fields.insert("native_level".to_string(), json!("panic"));
                fields.insert("path".to_string(), json!(".anoikis/state.json"));
                fields
            }),
            error: Some(ErrorInfo {
                message: "unexpected end of JSON input".to_string(),
                kind: Some("*json.SyntaxError".to_string()),
                stack: None,
            }),
            caller: None,
        },
    ]
}

#[test]
fn canonical_json_matches_the_golden_jsonl_byte_for_byte() {
    let expected: Vec<&str> = GOLDEN_JSONL.lines().collect();
    let records = golden_records();
    assert_eq!(records.len(), expected.len());
    for (record, expected_line) in records.iter().zip(expected) {
        assert_eq!(record.canonical_json().unwrap(), expected_line);
    }
}

#[test]
fn human_rendering_matches_the_golden_human_file_byte_for_byte() {
    let expected = GOLDEN_HUMAN.trim_end_matches('\n');
    let rendered: Vec<String> = golden_records()
        .iter()
        .map(|r| human_line(r, false))
        .collect();
    assert_eq!(rendered.join("\n"), expected);
}
