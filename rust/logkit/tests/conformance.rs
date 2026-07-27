//! Cross-language conformance against `conformance/logkit`: the suite's shared
//! input set drives this crate, and every rendered byte must equal the suite's
//! goldens — the same goldens every other implementation of the standard is
//! held to, which is what makes the two byte-identical rather than merely
//! equivalent.

use std::fs;
use std::path::{Path, PathBuf};

use serde::de::IgnoredAny;
use serde::Deserialize;
use serde_json::{Map, Value};

use logkit::{human_line, CallerInfo, ErrorInfo, Record, SCHEMA_VERSION};

/// Directory the runner asks for this implementation's rendered output in, one
/// subdirectory per language, so it can diff the languages against each other
/// and not only against the goldens. Unset outside the runner.
const ARTIFACT_ENV: &str = "LOGKIT_CONFORMANCE_OUT";

fn suite_root() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR")).join("../../conformance/logkit")
}

/// One case file: the event as a call site would hand it over, before
/// normalization. `level_token` is a raw inbound token rather than a `Level`,
/// so level normalization is part of what the suite pins. Unknown keys are
/// rejected — a key one language reads and another silently drops is exactly
/// the drift this gate exists to catch.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Case {
    id: String,
    #[serde(rename = "purpose")]
    _purpose: IgnoredAny,
    level_token: String,
    timestamp: String,
    service: String,
    #[serde(default)]
    service_version: Option<String>,
    message: String,
    #[serde(default)]
    fields: Map<String, Value>,
    #[serde(default)]
    error: Option<CaseError>,
    #[serde(default)]
    caller: Option<CaseCaller>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CaseError {
    message: String,
    #[serde(default)]
    kind: Option<String>,
    #[serde(default)]
    stack: Vec<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CaseCaller {
    file: String,
    line: u32,
    #[serde(default)]
    function: Option<String>,
}

/// Reads every case file in the suite's inputs directory, in file-name order.
/// An absent or empty directory panics: the suite is a checked-in corpus and is
/// never generated on the fly.
fn load_cases() -> Vec<Case> {
    let dir = suite_root().join("inputs");
    let mut names: Vec<PathBuf> = fs::read_dir(&dir)
        .unwrap_or_else(|err| panic!("read conformance inputs {}: {err}", dir.display()))
        .map(|entry| {
            entry
                .expect("read a conformance input directory entry")
                .path()
        })
        .filter(|path| path.extension().is_some_and(|ext| ext == "json"))
        .collect();
    names.sort();

    let cases: Vec<Case> = names
        .iter()
        .map(|path| {
            let raw = fs::read_to_string(path)
                .unwrap_or_else(|err| panic!("read conformance case {}: {err}", path.display()));
            let case: Case = serde_json::from_str(&raw)
                .unwrap_or_else(|err| panic!("decode conformance case {}: {err}", path.display()));
            let stem = path
                .file_stem()
                .and_then(|s| s.to_str())
                .unwrap_or_default();
            assert_eq!(
                case.id,
                stem,
                "conformance case {} declares a mismatched id",
                path.display()
            );
            case
        })
        .collect();

    assert!(
        !cases.is_empty(),
        "no case files under {}: the shared input set is missing",
        dir.display()
    );
    cases
}

/// Normalizes one case into the record both implementations must agree on,
/// byte for byte.
fn record(case: &Case) -> Record {
    let normalized = logkit::normalize(&case.level_token)
        .unwrap_or_else(|err| panic!("case {}: {err}", case.id));

    let mut fields = case.fields.clone();
    if let Some(native_level) = normalized.native_level {
        assert!(
            !fields.contains_key("native_level"),
            "case {} sets the reserved native_level key itself",
            case.id
        );
        fields.insert("native_level".to_string(), Value::String(native_level));
    }

    Record {
        schema_version: SCHEMA_VERSION,
        timestamp: case.timestamp.clone(),
        level: normalized.level,
        service: case.service.clone(),
        message: case.message.clone(),
        service_version: case.service_version.clone(),
        fields: (!fields.is_empty()).then_some(fields),
        error: case.error.as_ref().map(|e| ErrorInfo {
            message: e.message.clone(),
            kind: e.kind.clone(),
            stack: (!e.stack.is_empty()).then(|| e.stack.clone()),
        }),
        caller: case.caller.as_ref().map(|c| CallerInfo {
            file: c.file.clone(),
            line: c.line,
            function: c.function.clone(),
        }),
    }
}

/// Renders the whole shared input set and compares both renderings against the
/// suite's goldens byte for byte. The rendered output is written out before the
/// comparison so the runner can report a cross-language difference even on a
/// run that fails here.
#[test]
fn conformance_suite_matches_golden() {
    let mut ids = String::new();
    let mut records = String::new();
    let mut human = String::new();

    for case in &load_cases() {
        let built = record(case);
        let canonical = built
            .canonical_json()
            .unwrap_or_else(|err| panic!("case {}: canonicalize: {err}", case.id));
        ids.push_str(&case.id);
        ids.push('\n');
        records.push_str(&canonical);
        records.push('\n');
        human.push_str(&human_line(&built, false));
        human.push('\n');
    }

    write_artifacts(&[
        ("cases.txt", &ids),
        ("records.jsonl", &records),
        ("records.human.txt", &human),
    ]);

    assert_matches_golden("records.jsonl", &records);
    assert_matches_golden("records.human.txt", &human);
}

/// Compares `got` against the named golden file. A missing golden panics: the
/// suite reads its goldens and never writes them, so an absent one is a gap in
/// the corpus, not something to fill in silently.
fn assert_matches_golden(name: &str, got: &str) {
    let path = suite_root().join("golden").join(name);
    let want = fs::read_to_string(&path).unwrap_or_else(|err| {
        panic!(
            "read golden {}: {err} (goldens are recorded deliberately, never by a test run)",
            path.display()
        )
    });
    if got == want {
        return;
    }
    for (number, (got_line, want_line)) in line_pairs(got, &want).enumerate() {
        assert!(
            got_line == want_line,
            "{name} line {} diverges from the golden:\n got {got_line:?}\nwant {want_line:?}",
            number + 1
        );
    }
    panic!("{name} differs from its golden only in trailing bytes");
}

fn line_pairs<'a>(got: &'a str, want: &'a str) -> impl Iterator<Item = (&'a str, &'a str)> {
    let mut got_lines = got.split('\n');
    let mut want_lines = want.split('\n');
    std::iter::from_fn(move || match (got_lines.next(), want_lines.next()) {
        (None, None) => None,
        (got_line, want_line) => Some((
            got_line.unwrap_or("<no line>"),
            want_line.unwrap_or("<no line>"),
        )),
    })
}

/// Writes this implementation's rendered output into the runner's directory,
/// under a per-language subdirectory. A directory the runner named but did not
/// create is an error rather than a skip, so a misconfigured run never looks
/// like a clean one.
fn write_artifacts(artifacts: &[(&str, &String)]) {
    let Ok(root) = std::env::var(ARTIFACT_ENV) else {
        return;
    };
    let root = PathBuf::from(root);
    assert!(
        root.is_dir(),
        "{ARTIFACT_ENV}={} is not an existing directory",
        root.display()
    );
    let dir = root.join("rust");
    fs::create_dir_all(&dir)
        .unwrap_or_else(|err| panic!("create artifact directory {}: {err}", dir.display()));
    for (name, content) in artifacts {
        let path = dir.join(name);
        fs::write(&path, content)
            .unwrap_or_else(|err| panic!("write artifact {}: {err}", path.display()));
    }
}
