//! Byte-exact conformance against `schemas/clikit/examples/golden-results.jsonl`:
//! this crate's own [`ResultRecord`], built through the public builder,
//! must canonicalize to exactly these bytes — one record per exit class, in
//! taxonomy order. This is the cross-language interop surface the Go
//! implementation is gated against too.

use clikit::{Diagnostic, ResultRecord, Status, Triage};
use serde_json::json;

const GOLDEN_JSONL: &str = include_str!("../../../schemas/clikit/examples/golden-results.jsonl");

// One flat record-per-exit-class data table, not logic: length is inherent
// to covering all eleven classes explicitly rather than a sign the function
// should be split.
#[allow(clippy::too_many_lines)]
fn golden_records() -> Vec<ResultRecord> {
    vec![
        ResultRecord::builder(Status::Success, ["navigator", "search"])
            .data("hits", 3)
            .data(
                "matched_paths",
                json!(["agent/identity.md", "agent/workflows/index.md", "README.md"]),
            )
            .data("query", "tag:apm")
            .build()
            .unwrap(),
        ResultRecord::builder(Status::Caveats, ["language-tools", "build"])
            .data("built", json!(["go"]))
            .data("skipped", json!(["rust"]))
            .caveat(
                Diagnostic::new(
                    "caveats.toolchain.target_skipped",
                    "the rust target was skipped: no rust toolchain on this host",
                    Triage::run_tool(["rustup", "toolchain", "install", "stable"]),
                )
                .context("target", "rust"),
            )
            .build()
            .unwrap(),
        ResultRecord::builder(Status::GateNegative, ["git-tools", "worktree", "gate"])
            .data("allowed", false)
            .data("task_worktree", "/w/task/M2.P3.T1")
            .error(
                Diagnostic::new(
                    "gate_negative.worktree.write_outside_worktree",
                    "the write target is outside the task worktree",
                    Triage::reinvoke(["git-tools", "worktree", "gate", "--path", "/w/task/M2.P3.T1/README.md"])
                        .instruction("write to the task worktree instead of the primary checkout"),
                )
                .context("path", "/w/main/README.md"),
            )
            .build()
            .unwrap(),
        ResultRecord::builder(Status::PreconditionUnmet, ["navigator", "search"])
            .error(
                Diagnostic::new(
                    "precondition_unmet.index.not_built",
                    "the discovery index has not been built for this repository",
                    Triage::reinvoke(["navigator", "index", "build"]),
                )
                .context("index_path", "var/index/navigator.bm25"),
            )
            .build()
            .unwrap(),
        ResultRecord::builder(Status::NotFound, ["git-tools", "worktree", "remove"])
            .error(
                Diagnostic::new(
                    "not_found.worktree.no_such_worktree",
                    "no worktree named 'feat/y'",
                    Triage::reinvoke(["git-tools", "worktree", "list"]),
                )
                .context("name", "feat/y"),
            )
            .build()
            .unwrap(),
        ResultRecord::builder(Status::Conflict, ["git-tools", "worktree", "create"])
            .error(
                Diagnostic::new(
                    "conflict.worktree.branch_checked_out",
                    "branch 'feat/x' is already checked out at '/w/a'",
                    Triage::reinvoke(["git-tools", "worktree", "create", "--branch", "feat/x-2"]),
                )
                .context("branch", "feat/x")
                .context("worktree", "/w/a"),
            )
            .build()
            .unwrap(),
        ResultRecord::builder(Status::Usage, ["git-tools", "worktree", "create"])
            .error(
                Diagnostic::new(
                    "usage.flags.mutually_exclusive",
                    "--branch and --detach cannot be combined",
                    Triage::reinvoke(["git-tools", "worktree", "create", "--branch", "feat/x"]),
                )
                .context("flags", json!(["--branch", "--detach"])),
            )
            .build()
            .unwrap(),
        ResultRecord::builder(Status::Transient, ["anthropic-tools", "rates", "fetch"])
            .error(
                Diagnostic::new(
                    "transient.http.rate_limited",
                    "the upstream rate limited this request",
                    Triage::reinvoke(["anthropic-tools", "rates", "fetch"]).after_seconds(30),
                )
                .context("http_status", 429)
                .context("url", "https://api.anthropic.com/v1/models"),
            )
            .build()
            .unwrap(),
        ResultRecord::builder(Status::Permission, ["claude-tools", "fs", "write"])
            .error(
                Diagnostic::new(
                    "permission.fs.write_denied",
                    "write denied by filesystem permissions",
                    Triage::manual("grant write access to /etc/hosts, or choose a path this user owns"),
                )
                .context("mode", "0444")
                .context("path", "/etc/hosts")
                .context("uid", 1000),
            )
            .build()
            .unwrap(),
        ResultRecord::builder(Status::Unsupported, ["claude-tools", "archive", "extract"])
            .error(
                Diagnostic::new(
                    "unsupported.archive.format_not_implemented",
                    "rar archives are not handled by this tool",
                    Triage::run_tool(["unar", "-o", "var/out", "var/in/bundle.rar"]),
                )
                .context("format", "rar")
                .context("path", "var/in/bundle.rar"),
            )
            .build()
            .unwrap(),
        ResultRecord::builder(Status::Internal, ["anoikis-tools", "plan", "validate"])
            .error(
                Diagnostic::new(
                    "internal.state.invariant_violated",
                    "a node reached a state its predecessors do not permit",
                    Triage::manual(
                        "report this with the command line and .anoikis/state.json attached; re-invocation cannot repair it",
                    ),
                )
                .context("node", "M2.P3.T1")
                .context("predecessor_states", json!(["pending"]))
                .context("state", "done"),
            )
            .build()
            .unwrap(),
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
fn every_exit_class_is_covered_exactly_once() {
    let records = golden_records();
    let mut codes: Vec<u16> = records.iter().map(|r| r.exit_code).collect();
    codes.sort_unstable();
    assert_eq!(codes, vec![0, 10, 20, 30, 40, 41, 50, 60, 70, 80, 90]);
}
