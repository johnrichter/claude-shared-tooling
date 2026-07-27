//! Cross-language conformance against `conformance/clikit`: the suite's shared
//! input set drives this crate, and every rendered byte — the canonical record
//! and the exit code alike — must equal the suite's goldens, the same goldens
//! every other implementation of the contract is held to. That is what makes
//! the implementations byte-identical rather than merely equivalent.

use std::fmt::Write as _;
use std::fs;
use std::path::{Path, PathBuf};

use serde::de::IgnoredAny;
use serde::Deserialize;
use serde_json::{Map, Value};

use clikit::{Diagnostic, ResultRecord, Status, Triage};

/// Directory the runner asks for this implementation's rendered output in, one
/// subdirectory per language, so it can diff the languages against each other
/// and not only against the goldens. Unset outside the runner.
const ARTIFACT_ENV: &str = "CLIKIT_CONFORMANCE_OUT";

fn suite_root() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR")).join("../../conformance/clikit")
}

/// One case file: the outcome as a command hands it over — a status name, a
/// command path and whatever diagnostics it produced — before any of it is a
/// record. Building the record from that is what the suite pins. Unknown keys
/// are rejected: a key one language reads and another silently drops is exactly
/// the drift this gate exists to catch.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Case {
    id: String,
    #[serde(rename = "purpose")]
    _purpose: IgnoredAny,
    command: Vec<String>,
    status: String,
    #[serde(default)]
    data: Option<Map<String, Value>>,
    #[serde(default)]
    errors: Vec<CaseDiagnostic>,
    #[serde(default)]
    caveats: Vec<CaseDiagnostic>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CaseDiagnostic {
    code: String,
    message: String,
    triage: CaseTriage,
    #[serde(default)]
    context: Option<Map<String, Value>>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CaseTriage {
    kind: String,
    #[serde(default)]
    command: Vec<String>,
    #[serde(default)]
    instruction: Option<String>,
    #[serde(default)]
    after_seconds: Option<u32>,
}

/// Reads every case file in the suite's inputs directory, in file-name order.
/// An absent or empty directory panics: the suite is a checked-in corpus and is
/// never generated on the fly.
fn load_cases() -> Vec<Case> {
    let dir = suite_root().join("inputs");
    let mut paths: Vec<PathBuf> = fs::read_dir(&dir)
        .unwrap_or_else(|err| panic!("read conformance inputs {}: {err}", dir.display()))
        .map(|entry| {
            entry
                .expect("read a conformance input directory entry")
                .path()
        })
        .filter(|path| path.extension().is_some_and(|ext| ext == "json"))
        .collect();
    paths.sort();

    let cases: Vec<Case> = paths
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

/// The status a case names, resolved against the closed eleven-member set.
fn status(case: &Case) -> Status {
    Status::ALL
        .into_iter()
        .find(|status| status.as_str() == case.status)
        .unwrap_or_else(|| panic!("case {} declares unknown status {:?}", case.id, case.status))
}

/// The directive a case declares. `manual` carries an instruction and no
/// command, and the enum makes the other combinations unrepresentable, so an
/// out-of-shape case fails here rather than reaching the builder.
fn triage(case: &Case, declared: &CaseTriage) -> Triage {
    let instruction = || {
        declared
            .instruction
            .clone()
            .unwrap_or_else(|| panic!("case {}: a manual directive needs an instruction", case.id))
    };
    let mut built = match declared.kind.as_str() {
        "reinvoke" => Triage::reinvoke(declared.command.clone()),
        "run_tool" => Triage::run_tool(declared.command.clone()),
        "manual" => return Triage::manual(instruction()),
        other => panic!("case {}: unknown triage kind {other:?}", case.id),
    };
    if let Some(seconds) = declared.after_seconds {
        built = built.after_seconds(seconds);
    }
    if let Some(text) = &declared.instruction {
        built = built.instruction(text.clone());
    }
    built
}

fn diagnostic(case: &Case, declared: &CaseDiagnostic) -> Diagnostic {
    let mut built = Diagnostic::new(
        declared.code.clone(),
        declared.message.clone(),
        triage(case, &declared.triage),
    );
    if let Some(context) = &declared.context {
        for (key, value) in context {
            built = built.context(key.clone(), value.clone());
        }
    }
    built
}

/// Builds one case into the record both implementations must agree on, byte for
/// byte, through the same public builder a CLI uses.
fn record(case: &Case) -> ResultRecord {
    let mut builder = ResultRecord::builder(status(case), case.command.clone());
    if let Some(data) = &case.data {
        for (key, value) in data {
            builder = builder.data(key.clone(), value.clone());
        }
    }
    for declared in &case.errors {
        builder = builder.error(diagnostic(case, declared));
    }
    for declared in &case.caveats {
        builder = builder.caveat(diagnostic(case, declared));
    }
    builder
        .build()
        .unwrap_or_else(|err| panic!("case {}: {err}", case.id))
}

/// Renders the whole shared input set — the canonical record and the exit code
/// for every case — and compares both against the suite's goldens byte for
/// byte. The rendered output is written out before the comparison so the runner
/// can report a cross-language difference even on a run that fails here.
#[test]
fn conformance_suite_matches_golden() {
    let mut ids = String::new();
    let mut results = String::new();
    let mut exit_codes = String::new();

    for case in &load_cases() {
        let built = record(case);
        let canonical = built
            .canonical_json()
            .unwrap_or_else(|err| panic!("case {}: canonicalize: {err}", case.id));
        ids.push_str(&case.id);
        ids.push('\n');
        results.push_str(&canonical);
        results.push('\n');
        // The integer the process exits with, taken from the record itself —
        // the same value a CLI hands `std::process::exit` after emitting it.
        writeln!(
            exit_codes,
            "{} {} {}",
            case.id,
            built.status.as_str(),
            built.exit_code
        )
        .expect("a String never fails to grow");
    }

    write_artifacts(&[
        ("cases.txt", &ids),
        ("results.jsonl", &results),
        ("exit-codes.txt", &exit_codes),
    ]);

    assert_matches_golden("results.jsonl", &results);
    assert_matches_golden("exit-codes.txt", &exit_codes);
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
