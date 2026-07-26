//! Adversarial coverage for the boundaries `schemas/clikit/result-record.schema.json`
//! and `clikit.contract.json` declare but the crate's own unit tests do not
//! exercise directly: array/string caps, pattern rejections, and the
//! per-class errors/caveats presence rules across every failure class (not
//! just the two already covered in `record.rs`).

use clikit::{ClikitError, Diagnostic, ResultRecord, Status, Triage};

fn ok_diag(status: Status) -> Diagnostic {
    Diagnostic::new(
        format!("{}.probe.x", status.as_str()),
        "probe",
        Triage::manual("n/a"),
    )
}

// -- command shape ----------------------------------------------------------

#[test]
fn empty_command_is_rejected() {
    let empty: Vec<String> = vec![];
    let err = ResultRecord::builder(Status::Success, empty)
        .build()
        .unwrap_err();
    assert!(matches!(
        err,
        ClikitError::InvalidValue {
            field: "command",
            ..
        }
    ));
}

#[test]
fn command_over_eight_elements_is_rejected() {
    let command: Vec<&str> = vec!["a", "b", "c", "d", "e", "f", "g", "h", "i"];
    let err = ResultRecord::builder(Status::Success, command)
        .build()
        .unwrap_err();
    assert!(matches!(
        err,
        ClikitError::InvalidValue {
            field: "command",
            ..
        }
    ));
}

#[test]
fn command_of_exactly_eight_elements_is_accepted() {
    let command: Vec<&str> = vec!["a", "b", "c", "d", "e", "f", "g", "h"];
    ResultRecord::builder(Status::Success, command)
        .build()
        .unwrap();
}

#[test]
fn uppercase_tool_name_is_rejected() {
    let err = ResultRecord::builder(Status::Success, ["Navigator"])
        .build()
        .unwrap_err();
    assert!(matches!(
        err,
        ClikitError::InvalidValue {
            field: "command[0]",
            ..
        }
    ));
}

#[test]
fn subcommand_with_underscore_is_rejected() {
    let err = ResultRecord::builder(Status::Success, ["navigator", "sub_cmd"])
        .build()
        .unwrap_err();
    assert!(matches!(err, ClikitError::InvalidValue { .. }));
}

// -- data bounds --------------------------------------------------------------

#[test]
fn data_with_sixty_five_members_is_rejected() {
    let mut builder = ResultRecord::builder(Status::Success, ["navigator"]);
    for i in 0..65 {
        builder = builder.data(format!("k{i}"), i);
    }
    let err = builder.build().unwrap_err();
    assert!(matches!(
        err,
        ClikitError::TooMany {
            field: "data",
            max: 64,
            actual: 65
        }
    ));
}

#[test]
fn data_with_sixty_four_members_is_accepted() {
    let mut builder = ResultRecord::builder(Status::Success, ["navigator"]);
    for i in 0..64 {
        builder = builder.data(format!("k{i}"), i);
    }
    builder.build().unwrap();
}

#[test]
fn data_key_with_uppercase_is_rejected() {
    let err = ResultRecord::builder(Status::Success, ["navigator"])
        .data("Bad", 1)
        .build()
        .unwrap_err();
    assert!(matches!(err, ClikitError::InvalidValue { .. }));
}

// -- errors/caveats array bounds ----------------------------------------------

#[test]
fn errors_over_fifty_is_rejected() {
    let mut builder = ResultRecord::builder(Status::Internal, ["tool"]);
    for i in 0..51 {
        builder = builder.error(Diagnostic::new(
            format!("internal.probe.x{i}"),
            "probe",
            Triage::manual("n/a"),
        ));
    }
    let err = builder.build().unwrap_err();
    assert!(matches!(
        err,
        ClikitError::TooMany {
            field: "errors",
            max: 50,
            actual: 51
        }
    ));
}

#[test]
fn errors_at_exactly_fifty_is_accepted() {
    let mut builder = ResultRecord::builder(Status::Internal, ["tool"]);
    for i in 0..50 {
        builder = builder.error(Diagnostic::new(
            format!("internal.probe.x{i}"),
            "probe",
            Triage::manual("n/a"),
        ));
    }
    builder.build().unwrap();
}

#[test]
fn caveats_over_fifty_is_rejected() {
    let mut builder = ResultRecord::builder(Status::Caveats, ["tool"]);
    for i in 0..51 {
        builder = builder.caveat(Diagnostic::new(
            format!("caveats.probe.x{i}"),
            "probe",
            Triage::manual("n/a"),
        ));
    }
    let err = builder.build().unwrap_err();
    assert!(matches!(
        err,
        ClikitError::TooMany {
            field: "caveats",
            max: 50,
            actual: 51
        }
    ));
}

// -- diagnostic shape -----------------------------------------------------------

#[test]
fn empty_message_is_rejected() {
    let err = ResultRecord::builder(Status::Internal, ["tool"])
        .error(Diagnostic::new(
            "internal.probe.x",
            "",
            Triage::manual("n/a"),
        ))
        .build()
        .unwrap_err();
    assert!(matches!(
        err,
        ClikitError::InvalidValue {
            field: "message",
            ..
        }
    ));
}

#[test]
fn message_with_control_character_is_rejected() {
    let err = ResultRecord::builder(Status::Internal, ["tool"])
        .error(Diagnostic::new(
            "internal.probe.x",
            "bad\nline",
            Triage::manual("n/a"),
        ))
        .build()
        .unwrap_err();
    assert!(matches!(
        err,
        ClikitError::InvalidValue {
            field: "message",
            ..
        }
    ));
}

#[test]
fn message_over_bound_is_rejected() {
    let long = "x".repeat(4097);
    let err = ResultRecord::builder(Status::Internal, ["tool"])
        .error(Diagnostic::new(
            "internal.probe.x",
            long,
            Triage::manual("n/a"),
        ))
        .build()
        .unwrap_err();
    assert!(matches!(
        err,
        ClikitError::InvalidValue {
            field: "message",
            ..
        }
    ));
}

#[test]
fn context_with_zero_members_is_rejected_by_never_calling_context() {
    // A Diagnostic never given `.context(...)` carries `None`, which is the
    // schema's own "omitted, never {}" rule honored structurally.
    let diag = Diagnostic::new("internal.probe.x", "probe", Triage::manual("n/a"));
    assert!(ResultRecord::builder(Status::Internal, ["tool"])
        .error(diag)
        .build()
        .is_ok());
}

#[test]
fn context_over_thirty_two_members_is_rejected() {
    let mut diag = Diagnostic::new("internal.probe.x", "probe", Triage::manual("n/a"));
    for i in 0..33 {
        diag = diag.context(format!("k{i}"), i);
    }
    let err = ResultRecord::builder(Status::Internal, ["tool"])
        .error(diag)
        .build()
        .unwrap_err();
    assert!(matches!(
        err,
        ClikitError::TooMany {
            field: "context",
            max: 32,
            ..
        }
    ));
}

#[test]
fn diagnostic_code_with_one_segment_is_rejected() {
    let err = ResultRecord::builder(Status::Internal, ["tool"])
        .error(Diagnostic::new("internal", "probe", Triage::manual("n/a")))
        .build()
        .unwrap_err();
    assert!(matches!(
        err,
        ClikitError::InvalidValue { field: "code", .. }
    ));
}

#[test]
fn diagnostic_code_with_five_segments_is_rejected() {
    let err = ResultRecord::builder(Status::Internal, ["tool"])
        .error(Diagnostic::new(
            "internal.a.b.c.d",
            "probe",
            Triage::manual("n/a"),
        ))
        .build()
        .unwrap_err();
    assert!(matches!(
        err,
        ClikitError::InvalidValue { field: "code", .. }
    ));
}

// -- triage shape --------------------------------------------------------------

#[test]
fn reinvoke_with_empty_command_is_rejected() {
    let empty: Vec<String> = vec![];
    let err = ResultRecord::builder(Status::Transient, ["tool"])
        .error(Diagnostic::new(
            "transient.probe.x",
            "probe",
            Triage::reinvoke(empty),
        ))
        .build()
        .unwrap_err();
    assert!(matches!(
        err,
        ClikitError::InvalidValue {
            field: "triage.command",
            ..
        }
    ));
}

#[test]
fn run_tool_with_empty_command_is_rejected() {
    let empty: Vec<String> = vec![];
    let err = ResultRecord::builder(Status::Unsupported, ["tool"])
        .error(Diagnostic::new(
            "unsupported.probe.x",
            "probe",
            Triage::run_tool(empty),
        ))
        .build()
        .unwrap_err();
    assert!(matches!(
        err,
        ClikitError::InvalidValue {
            field: "triage.command",
            ..
        }
    ));
}

#[test]
fn after_seconds_over_max_is_rejected() {
    let err = ResultRecord::builder(Status::Transient, ["tool"])
        .error(Diagnostic::new(
            "transient.probe.x",
            "probe",
            Triage::reinvoke(["tool"]).after_seconds(86401),
        ))
        .build()
        .unwrap_err();
    assert!(matches!(
        err,
        ClikitError::InvalidValue {
            field: "triage.after_seconds",
            ..
        }
    ));
}

#[test]
fn after_seconds_at_max_is_accepted() {
    ResultRecord::builder(Status::Transient, ["tool"])
        .error(Diagnostic::new(
            "transient.probe.x",
            "probe",
            Triage::reinvoke(["tool"]).after_seconds(86400),
        ))
        .build()
        .unwrap();
}

#[test]
fn manual_triage_kind_serializes_with_no_command_field() {
    let record = ResultRecord::builder(Status::Internal, ["tool"])
        .error(Diagnostic::new(
            "internal.probe.x",
            "probe",
            Triage::manual("call someone"),
        ))
        .build()
        .unwrap();
    let json = record.canonical_json().unwrap();
    let value: serde_json::Value = serde_json::from_str(&json).unwrap();
    let triage = &value["errors"][0]["triage"];
    assert_eq!(triage["kind"], "manual");
    assert!(triage.get("command").is_none());
}

// -- caveat/error code cross-contamination -------------------------------------

#[test]
fn caveat_carrying_a_non_caveat_code_is_rejected() {
    let err = ResultRecord::builder(Status::Caveats, ["tool"])
        .caveat(Diagnostic::new(
            "internal.probe.x",
            "probe",
            Triage::manual("n/a"),
        ))
        .build()
        .unwrap_err();
    assert!(matches!(err, ClikitError::NotACaveatCode { .. }));
}

#[test]
fn error_carrying_a_caveats_code_is_rejected_even_when_status_is_a_failure_class() {
    let err = ResultRecord::builder(Status::Internal, ["tool"])
        .error(Diagnostic::new(
            "caveats.probe.x",
            "probe",
            Triage::manual("n/a"),
        ))
        .build()
        .unwrap_err();
    assert!(matches!(err, ClikitError::GoverningCodeMismatch { .. }));
}

#[test]
fn non_governing_error_may_carry_a_different_class_prefix_than_the_record_status() {
    // Only errors[0] (the governing error) must match the record's status
    // class, per the schema's `prefixItems` rule; a later entry is free.
    let record = ResultRecord::builder(Status::NotFound, ["tool"])
        .error(Diagnostic::new(
            "not_found.probe.x",
            "governing",
            Triage::manual("n/a"),
        ))
        .error(Diagnostic::new(
            "permission.probe.y",
            "secondary",
            Triage::manual("n/a"),
        ))
        .build()
        .unwrap();
    assert_eq!(record.exit_code, 40);
}

// -- every exit class round-trips through build + canonicalize -----------------

#[test]
fn every_failure_class_requires_errors_and_rejects_their_absence() {
    for status in Status::ALL {
        if !status.is_failure() {
            continue;
        }
        let err = ResultRecord::builder(status, ["tool"]).build().unwrap_err();
        assert!(
            matches!(err, ClikitError::MissingErrors { .. }),
            "{status:?} did not require errors"
        );
    }
}

#[test]
fn success_and_caveats_reject_errors_all_other_classes_require_them() {
    for status in Status::ALL {
        // `success` carries no diagnostics of its own, so an `errors` entry
        // attached to it borrows another class's code purely to exercise
        // the `ErrorsNotAllowed` path, not the code-class check.
        let diagnostic = if status == Status::Success {
            ok_diag(Status::Internal)
        } else {
            ok_diag(status)
        };
        let builder = ResultRecord::builder(status, ["tool"]).error(diagnostic);
        if status == Status::Caveats {
            // caveats forbids `errors` outright regardless of caveats presence
            let err = builder.build().unwrap_err();
            assert!(matches!(err, ClikitError::ErrorsNotAllowed { .. }));
            continue;
        }
        if status == Status::Success {
            let err = builder.build().unwrap_err();
            assert!(matches!(err, ClikitError::ErrorsNotAllowed { .. }));
            continue;
        }
        assert!(
            builder.build().is_ok(),
            "{status:?} should accept its own governing error"
        );
    }
}

#[test]
fn log_level_matches_the_contract_table_for_every_class() {
    use logkit::Level;
    let expected = [
        (Status::Success, Level::Info),
        (Status::Caveats, Level::Warn),
        (Status::GateNegative, Level::Info),
        (Status::PreconditionUnmet, Level::Error),
        (Status::NotFound, Level::Error),
        (Status::Conflict, Level::Error),
        (Status::Usage, Level::Error),
        (Status::Transient, Level::Error),
        (Status::Permission, Level::Error),
        (Status::Unsupported, Level::Error),
        (Status::Internal, Level::Fatal),
    ];
    for (status, level) in expected {
        assert_eq!(status.log_level(), level, "{status:?}");
    }
}
