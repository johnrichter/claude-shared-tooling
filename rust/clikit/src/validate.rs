//! Shape validators shared by [`crate::record`], [`crate::diagnostic`] and
//! [`crate::triage`] — each mirrors one `$defs` entry in
//! `result-record.schema.json`, by name, so a schema pattern change has one
//! place in this crate to follow it.

use crate::error::ClikitError;
use crate::status::Status;

/// `$defs/line` and `$defs/argv_token`: non-empty, bounded at `max_len`
/// (both are 4096 in the schema), and free of the C0 control characters
/// (U+0000-U+001F) and DEL (U+007F) - the schema's pattern excludes exactly
/// that range, not the wider C1 range (U+0080-U+009F) `char::is_control`
/// also rejects, so this matches the schema exactly. Backs `message`,
/// `instruction` and one triage-command element.
pub(crate) fn validate_line(
    field: &'static str,
    value: &str,
    max_len: usize,
) -> Result<(), ClikitError> {
    if value.is_empty() {
        return Err(invalid(field, value, "must not be empty"));
    }
    if value.chars().count() > max_len {
        return Err(invalid(field, value, "exceeds the maximum length"));
    }
    if value
        .chars()
        .any(|c| matches!(c as u32, 0x00..=0x1F | 0x7F))
    {
        return Err(invalid(
            field,
            value,
            "must not contain a control character",
        ));
    }
    Ok(())
}

/// `$defs/tool_name`: `^[a-z0-9][a-z0-9._-]*$`, 1-64 characters — the same
/// pattern as logkit's `service`, since `command[0]` and `service` are the
/// same string by the contract's `service_binding` rule.
pub(crate) fn validate_tool_name(value: &str) -> Result<(), ClikitError> {
    validate_pattern("command[0]", value, 64, |c, first| {
        c.is_ascii_lowercase() || c.is_ascii_digit() || (!first && matches!(c, '.' | '_' | '-'))
    })
}

/// `$defs/subcommand_name`: `^[a-z0-9][a-z0-9-]*$`, 1-64 characters —
/// narrower than `tool_name`: no `.` or `_`.
pub(crate) fn validate_subcommand_name(
    field: &'static str,
    value: &str,
) -> Result<(), ClikitError> {
    validate_pattern("command element", value, 64, |c, first| {
        let _ = field;
        c.is_ascii_lowercase() || c.is_ascii_digit() || (!first && c == '-')
    })
}

fn validate_pattern(
    field: &'static str,
    value: &str,
    max_len: usize,
    accept: impl Fn(char, bool) -> bool,
) -> Result<(), ClikitError> {
    let reason = "must be 1-64 lowercase alphanumeric characters (plus separators after the first)";
    let mut chars = value.chars();
    match chars.next() {
        Some(c) if accept(c, true) => {}
        _ => return Err(invalid(field, value, reason)),
    }
    if value.chars().count() > max_len || !chars.all(|c| accept(c, false)) {
        return Err(invalid(field, value, reason));
    }
    Ok(())
}

/// `$defs/diagnostic_code`: a class prefix — one of the ten class names a
/// diagnostic may carry (every [`Status`] but `success`, which carries no
/// diagnostics) — followed by 1-3 further dot-separated segments, each
/// `[a-z0-9]+(_[a-z0-9]+)*`. Whether that class is legal in the diagnostic's
/// *context* (a governing error, a non-governing error, or a caveat) is a
/// separate, caller-side check ([`crate::record::ResultRecordBuilder::build`]);
/// this only enforces that the class is one of the schema's ten.
pub(crate) fn validate_code(value: &str) -> Result<(), ClikitError> {
    let reason = "must be a class prefix followed by 1-3 dot-separated snake_case segments, 3-128 characters total";
    if value.chars().count() < 3 || value.chars().count() > 128 {
        return Err(invalid("code", value, reason));
    }
    let segments: Vec<&str> = value.split('.').collect();
    if !(2..=4).contains(&segments.len()) {
        return Err(invalid("code", value, reason));
    }
    if !Status::ALL
        .iter()
        .any(|status| !matches!(status, Status::Success) && status.as_str() == segments[0])
    {
        return Err(invalid(
            "code",
            value,
            "the class prefix must be one of the ten diagnostic classes",
        ));
    }
    for segment in &segments[1..] {
        let ok = !segment.is_empty()
            && segment
                .chars()
                .next()
                .is_some_and(|c| c.is_ascii_lowercase() || c.is_ascii_digit())
            && segment
                .chars()
                .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '_')
            && !segment.contains("__")
            && !segment.ends_with('_');
        if !ok {
            return Err(invalid("code", value, reason));
        }
    }
    Ok(())
}

/// `data` and `error.context`'s member-name pattern: `^[a-z][a-z0-9_]*$`,
/// 1-128 characters.
pub(crate) fn validate_member_key(field: &'static str, key: &str) -> Result<(), ClikitError> {
    validate_pattern(field, key, 128, |c, first| {
        (first && c.is_ascii_lowercase())
            || (!first && (c.is_ascii_lowercase() || c.is_ascii_digit() || c == '_'))
    })
}

fn invalid(field: &'static str, value: &str, reason: &'static str) -> ClikitError {
    ClikitError::InvalidValue {
        field,
        reason,
        value: value.to_string(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn code_accepts_two_to_four_segments() {
        validate_code("conflict.worktree.branch_checked_out").unwrap();
        validate_code("usage.clikit.invocation_rejected").unwrap();
        validate_code("caveats.a").unwrap();
        validate_code("internal.a.b.c").unwrap();
    }

    #[test]
    fn code_rejects_bad_shapes() {
        for bad in [
            "",
            "onlyoneseg",
            "conflict.",
            "conflict..x",
            "conflict.X",
            "conflict.a.b.c.d",
        ] {
            assert!(
                validate_code(bad).is_err(),
                "expected {bad:?} to be rejected"
            );
        }
    }

    #[test]
    fn tool_name_rejects_uppercase_and_leading_separator() {
        validate_tool_name("navigator").unwrap();
        validate_tool_name("git-tools").unwrap();
        assert!(validate_tool_name("Navigator").is_err());
        assert!(validate_tool_name("-nav").is_err());
    }

    #[test]
    fn subcommand_name_rejects_dot_and_underscore() {
        validate_subcommand_name("x", "search").unwrap();
        assert!(validate_subcommand_name("x", "search.sub").is_err());
        assert!(validate_subcommand_name("x", "search_sub").is_err());
    }

    #[test]
    fn member_key_matches_data_and_context_pattern() {
        validate_member_key("data", "hits").unwrap();
        validate_member_key("data", "matched_paths").unwrap();
        assert!(validate_member_key("data", "Hits").is_err());
        assert!(validate_member_key("data", "1hits").is_err());
    }
}
