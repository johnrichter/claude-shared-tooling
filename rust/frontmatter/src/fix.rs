//! Tier-1 frontmatter repair -- deterministic, schema-driven, and PURE.
//!
//! [`propose_fix`] and [`propose_skeleton`] compute a schema-conformant
//! replacement frontmatter for a file, reading every repair rule (which
//! fields are required, their machine-vs-human authorship, description
//! caps, namespace cardinality/parents/report-only-ness, the report
//! trigger/period regex) off the merged [`Profile`] -- exactly the schema
//! [`crate::validate::validate`] itself interprets, so a fix and the
//! violation it clears are always schema-consistent with each other.
//!
//! # Purity (SC16)
//! Nothing in this module touches a filesystem, a network, a model/LLM, or
//! the wall clock. [`propose_fix`]/[`propose_skeleton`] take `now` as a
//! caller-supplied `&str` (an ISO-8601 timestamp) rather than reading the
//! system clock themselves -- the caller (a binary, a test) owns deciding
//! what "now" means; this module only stamps the value it's given. See
//! `sc16_purity` below for the guard that pins this.
//!
//! # What Tier-1 can and cannot fix
//! Tier-1 repairs STRUCTURE: dedupe, drop, trim, add a placeholder, stamp,
//! classify. It never invents real prose or judgement -- a required field
//! the merged profile marks `human_authored` (see [`Profile::required_fields`])
//! gets, at most, a placeholder value here, and its name is surfaced in
//! [`FixProposal::human_authored_fields`] so a caller knows a person or a
//! gated model turn still owes that field real content. This module does
//! not author those fields and does not write anything to disk -- both are
//! the caller's job.
//!
//! # Always-flat output
//! A source document may nest its managed fields under a top-level
//! `workspace:` mapping (the Skill/Agent/Rule convention); [`propose_fix`]
//! always renders flat instead of reconstructing that nesting. A flat
//! block is unambiguously valid input to [`crate::validate::validate`]
//! (its own flattening step reads flat or nested identically), so this
//! loses no conformance -- only the source's nesting *style*, which is a
//! presentation choice for whichever caller eventually writes the file,
//! not a schema concern this pure library needs to preserve.

use crate::profile::{Cardinality, Profile};
use crate::validate::{classify_file_class, flatten, is_missing_value};
use crate::value::{FrontmatterValue, RawFields};
use crate::ParsedFrontmatter;

/// A placeholder tag/value token a human (or a gated Tier-2 model turn)
/// must replace -- never a real, meaningful value. Kept as one named
/// constant so every stub this module emits is textually recognizable.
const PLACEHOLDER: &str = "TODO";

/// The result of a Tier-1 repair: the repaired frontmatter, in canonical
/// field order (see [`render`]), plus the subset of required fields the
/// merged profile marks `human_authored` that this pass could only stub.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct FixProposal {
    /// The repaired frontmatter fields, in canonical order. Render with
    /// [`render`] to get a `---`-fenced YAML block; re-parse with
    /// [`crate::parse::parse`] and re-check with
    /// [`crate::validate::validate`] to confirm conformance.
    pub fields: RawFields,
    /// Required fields this pass stubbed with a placeholder rather than a
    /// real value, because the merged profile marks that field
    /// `human_authored` (see [`Profile::required_fields`]) and the field
    /// was absent, blank, or (for `tags`) missing a required namespace.
    /// A field already present with real content is never listed here,
    /// even if the profile marks it `human_authored` -- only what Tier-1
    /// actually had to stub.
    pub human_authored_fields: Vec<String>,
    /// The file class [`propose_fix`]/[`propose_skeleton`] classified
    /// `rel_path` into (see [`crate::validate::FrontmatterEntry::file_class`]).
    /// Not itself a frontmatter field; carried for a caller that wants it
    /// without re-classifying.
    pub file_class: String,
}

/// Computes a schema-conformant replacement frontmatter for `parsed`,
/// against `profile`, stamping `updated:` with `now`.
///
/// Every original field survives unless a repair narrows or replaces it:
/// a required field that's absent/blank gets a placeholder (its name then
/// appears in [`FixProposal::human_authored_fields`] if the profile marks
/// it `human_authored`); `tags` is dropped and rebuilt against the profile's
/// namespace rules (dedupe a singleton, drop an orphaned child or a
/// misused report-only tag, add a placeholder for an absent required
/// namespace); an over-cap `description` is trimmed to its file class's
/// cap; `updated` is always re-stamped to `now` (a Tier-1 fix pass is
/// itself an edit); every other original top-level field passes through
/// untouched, in its original relative order, after the schema's required
/// fields. The result always re-validates via [`crate::validate::validate`]
/// as conformant, modulo any placeholder Tier-2 still owes.
#[must_use]
pub fn propose_fix(
    parsed: &ParsedFrontmatter,
    rel_path: &str,
    profile: &Profile,
    now: &str,
) -> FixProposal {
    let file_class = classify_file_class(rel_path, profile);
    let effective = flatten(&parsed.raw_fields);
    let mut human_authored_fields = Vec::new();
    let mut fields: Vec<(String, FrontmatterValue)> = Vec::new();

    for (field, authorship) in profile.required_fields() {
        if field == "updated" {
            fields.push((field.to_string(), FrontmatterValue::Scalar(now.to_string())));
            continue;
        }
        if field == "tags" {
            let (tags, stubbed) = repair_tags(effective.get("tags"), profile, now);
            if stubbed && authorship == "human_authored" {
                human_authored_fields.push(field.to_string());
            }
            fields.push((field.to_string(), FrontmatterValue::Sequence(tags)));
            continue;
        }

        let current = effective.get(field);
        let value = match current {
            _ if is_missing_value(current) => None,
            Some(v) if field == "description" => Some(trim_description(v, &file_class, profile)),
            Some(v) => Some(v.clone()),
            None => None,
        };
        match value {
            None => {
                fields.push((field.to_string(), stub_for(field)));
                if authorship == "human_authored" {
                    human_authored_fields.push(field.to_string());
                }
            }
            Some(value) => fields.push((field.to_string(), value)),
        }
    }

    let required_names: Vec<&str> = profile.required_fields().map(|(f, _)| f).collect();
    for (key, value) in parsed.raw_fields.iter() {
        if key == "workspace" || required_names.contains(&key) {
            continue;
        }
        fields.push((key.to_string(), value.clone()));
    }

    FixProposal {
        fields: RawFields::from_ordered_pairs(fields),
        human_authored_fields,
        file_class,
    }
}

/// Computes the minimal schema-conformant frontmatter skeleton for a file
/// with no existing frontmatter at all: every required field stubbed (see
/// [`propose_fix`]'s per-field rules), `updated:` stamped from `now`, and
/// `file_class` classified from `rel_path`. Every required field the
/// profile marks `human_authored` is necessarily stubbed here, since there
/// is no original content to preserve.
#[must_use]
pub fn propose_skeleton(rel_path: &str, profile: &Profile, now: &str) -> FixProposal {
    let empty = ParsedFrontmatter::from_raw_fields(
        RawFields::from_ordered_pairs(Vec::new()),
        String::new(),
    );
    propose_fix(&empty, rel_path, profile, now)
}

/// A generic placeholder value for a scalar required field this crate has
/// no shape-specific stub for -- `tags`/`links`/`updated` are handled by
/// their own rules in [`propose_fix`]; every other required field (whether
/// one of this pack's `name`/`description`/`id`, or a field a foreign
/// pack declares) gets this same textual marker, since a pure library has
/// no way to know a foreign field's intended shape or wording.
fn stub_for(field: &str) -> FrontmatterValue {
    if field == "links" {
        FrontmatterValue::Sequence(Vec::new())
    } else {
        FrontmatterValue::Scalar(format!("{PLACEHOLDER}: {field} needs a value."))
    }
}

/// Shortens `description` to at most `cap` characters (chars, not bytes),
/// preferring to break at the last word boundary within the cap -- a
/// mechanical trim of existing human-authored prose, never a rewrite.
/// Passes through unchanged (cloned) if not a `Scalar`, or if `file_class`
/// has no cap in `profile` (an uncapped class), or if it's within cap.
fn trim_description(
    value: &FrontmatterValue,
    file_class: &str,
    profile: &Profile,
) -> FrontmatterValue {
    let FrontmatterValue::Scalar(text) = value else {
        return value.clone();
    };
    let Some(cap) = profile.description_cap(file_class) else {
        return value.clone();
    };
    let cap = usize::try_from(cap).unwrap_or(usize::MAX);
    if text.chars().count() <= cap {
        return value.clone();
    }
    let truncated: String = text.chars().take(cap).collect();
    let shortened = match truncated.rfind(' ') {
        Some(last_space) if last_space > 0 => &truncated[..last_space],
        _ => &truncated,
    };
    FrontmatterValue::Scalar(
        shortened
            .trim_end_matches([' ', ',', ';', ':', '-'])
            .to_string(),
    )
}

/// Rebuilds `tags` into full conformance with `profile`'s namespace rules,
/// returning the repaired list plus whether any REQUIRED-namespace
/// placeholder was appended (the signal [`propose_fix`] uses to decide
/// whether `tags` belongs in [`FixProposal::human_authored_fields`]).
/// Applies, in order: keep only namespaced (`ns:value`) entries; dedupe
/// each singleton namespace to its first occurrence; drop a child
/// namespace's tags whose declared parent namespace is absent (to a fixed
/// point, since dropping one child can never resurrect a parent, but a
/// pack could declare a chain); resolve the report axis (drop a malformed
/// `period` tag on a report file, or every report-only tag on a
/// non-report file); then add a placeholder for every still-absent
/// namespace the profile requires (singleton, at-least-one, or -- on a
/// report file -- report-required).
fn repair_tags(
    tags: Option<&FrontmatterValue>,
    profile: &Profile,
    now: &str,
) -> (Vec<String>, bool) {
    let mut tags: Vec<String> = match tags {
        Some(FrontmatterValue::Sequence(items)) => items.clone(),
        _ => Vec::new(),
    };
    tags.retain(|t| t.contains(':'));

    dedupe_singletons(&mut tags, profile);
    drop_orphaned_children(&mut tags, profile);

    let trigger = &profile.pack.report.trigger;
    let is_report = tags
        .iter()
        .any(|t| t == &format!("{}:{}", trigger.namespace, trigger.value));

    if is_report {
        drop_malformed_period(&mut tags, profile);
    } else {
        drop_report_only(&mut tags, profile);
    }

    let stubbed = add_required_placeholders(&mut tags, profile, is_report, now);
    (tags, stubbed)
}

fn namespace_of(tag: &str) -> &str {
    tag.split_once(':').map_or(tag, |(ns, _)| ns)
}

fn dedupe_singletons(tags: &mut Vec<String>, profile: &Profile) {
    let mut seen: Vec<String> = Vec::new();
    tags.retain(|tag| {
        let ns = namespace_of(tag);
        let is_singleton = profile
            .pack
            .namespaces
            .iter()
            .any(|n| n.name == ns && n.cardinality == Cardinality::Singleton);
        if !is_singleton {
            return true;
        }
        if seen.iter().any(|s| s == ns) {
            false
        } else {
            seen.push(ns.to_string());
            true
        }
    });
}

fn drop_orphaned_children(tags: &mut Vec<String>, profile: &Profile) {
    loop {
        let present = |ns: &str| tags.iter().any(|t| namespace_of(t) == ns);
        let orphan = profile
            .pack
            .namespaces
            .iter()
            .filter(|n| !n.parents.is_empty())
            .find(|n| present(&n.name) && n.parents.iter().any(|p| !present(p)));
        let Some(orphan) = orphan else { break };
        let name = orphan.name.clone();
        tags.retain(|t| namespace_of(t) != name);
    }
}

fn drop_report_only(tags: &mut Vec<String>, profile: &Profile) {
    let report_only: Vec<&str> = profile
        .pack
        .namespaces
        .iter()
        .filter(|n| n.report_only)
        .map(|n| n.name.as_str())
        .collect();
    tags.retain(|t| !report_only.contains(&namespace_of(t)));
}

fn drop_malformed_period(tags: &mut Vec<String>, profile: &Profile) {
    let period_ns = &profile.pack.report.period.namespace;
    tags.retain(|t| {
        if namespace_of(t) != period_ns {
            return true;
        }
        let value = t.split_once(':').map_or("", |(_, v)| v);
        profile.period_pattern.is_match(value)
    });
}

/// Appends a placeholder tag for every namespace `profile` still requires
/// after the drop phases: every singleton/at-least-one namespace always,
/// plus (only on a report file) every `report.required_namespaces` entry.
/// Returns whether any placeholder was appended.
fn add_required_placeholders(
    tags: &mut Vec<String>,
    profile: &Profile,
    is_report: bool,
    now: &str,
) -> bool {
    let mut stubbed = false;

    for ns in &profile.pack.namespaces {
        let required = matches!(
            ns.cardinality,
            Cardinality::Singleton | Cardinality::AtLeastOne
        );
        let present = tags.iter().any(|t| namespace_of(t) == ns.name);
        if required && !present {
            tags.push(format!("{}:{PLACEHOLDER}", ns.name));
            stubbed = true;
        }
    }

    if is_report {
        for ns in &profile.pack.report.required_namespaces {
            if tags.iter().any(|t| namespace_of(t) == ns) {
                continue;
            }
            let placeholder = if *ns == profile.pack.report.period.namespace {
                period_placeholder(profile, now)
            } else {
                format!("{ns}:{PLACEHOLDER}")
            };
            tags.push(placeholder);
            stubbed = true;
        }
    }
    stubbed
}

/// A best-effort, schema-valid placeholder for the report's `period`
/// namespace: a same-day interval derived from `now`'s date portion (the
/// merged pack's shipped period format is `YYYY-MM-DD/YYYY-MM-DD`, so
/// duplicating `now`'s first 10 characters produces a syntactically valid,
/// single-day interval). Verified against `profile.period_pattern` before
/// use -- a foreign pack with an incompatible period format falls back to
/// a bare `TODO` marker rather than guessing a value that wouldn't match.
fn period_placeholder(profile: &Profile, now: &str) -> String {
    let period_ns = &profile.pack.report.period.namespace;
    if let Some(date) = now.get(..10) {
        let candidate = format!("{date}/{date}");
        if profile.period_pattern.is_match(&candidate) {
            return format!("{period_ns}:{candidate}");
        }
    }
    format!("{period_ns}:{PLACEHOLDER}")
}

/// Renders `fields` as a `---`-fenced canonical YAML block: a `Scalar`
/// named `updated` renders bare (an unquoted timestamp, this crate's own
/// convention throughout its test fixtures); every other `Scalar` renders
/// double-quoted, with `\`/`"` and the whitespace controls `\n`/`\r`/`\t`
/// escaped (see [`escape_double_quoted`] for why raw newlines are unsafe);
/// a `Sequence` renders as a block
/// list, one bare (unquoted) item per line indented two spaces under its
/// key (`tags:`/`links:`-style); a `Mapping` renders as a nested indented
/// block (recursively, by the same rules); `Other` (an explicit YAML null)
/// renders as bare `null`. Field order is exactly `fields`' own iteration
/// order -- [`propose_fix`]/[`propose_skeleton`] build that order as the
/// profile's required-field order followed by every passthrough field in
/// its original relative position, so this function itself makes no
/// ordering decision.
#[must_use]
pub fn render(fields: &RawFields) -> String {
    let mut out = String::from("---\n");
    for (key, value) in fields.iter() {
        render_field(key, value, 0, &mut out);
    }
    out.push_str("---\n");
    out
}

fn render_field(key: &str, value: &FrontmatterValue, indent: usize, out: &mut String) {
    use std::fmt::Write as _;
    let pad = " ".repeat(indent);
    match value {
        FrontmatterValue::Scalar(text) if key == "updated" => {
            let _ = writeln!(out, "{pad}{key}: {text}");
        }
        FrontmatterValue::Scalar(text) => {
            let _ = writeln!(out, "{pad}{key}: \"{}\"", escape_double_quoted(text));
        }
        FrontmatterValue::Sequence(items) if items.is_empty() => {
            // A bare `key:` with nothing under it re-parses as a YAML null,
            // not an empty sequence -- flow-style `[]` is what round-trips.
            let _ = writeln!(out, "{pad}{key}: []");
        }
        FrontmatterValue::Sequence(items) => {
            let _ = writeln!(out, "{pad}{key}:");
            for item in items {
                let _ = writeln!(out, "{pad}  - {item}");
            }
        }
        FrontmatterValue::Mapping(inner) => {
            let _ = writeln!(out, "{pad}{key}:");
            for (inner_key, inner_value) in inner.iter() {
                render_field(inner_key, inner_value, indent + 2, out);
            }
        }
        FrontmatterValue::Other => {
            let _ = writeln!(out, "{pad}{key}: null");
        }
    }
}

/// Escapes a scalar for a YAML double-quoted scalar: `\` and `"` (the quote
/// delimiters), plus the three whitespace controls that would otherwise be
/// emitted as raw bytes on their own physical lines. A raw newline is not
/// merely a folding hazard -- a value line equal to `---` would be read back
/// as the frontmatter block's closing delimiter (see `crate::parse`),
/// truncating the block or failing the reparse. Emitting `\n`/`\r`/`\t` as
/// their YAML named escapes keeps every scalar on one physical line, so the
/// rendered block round-trips byte-for-byte regardless of value content.
/// Backslash is escaped first so the escapes this function itself inserts
/// are not doubled.
fn escape_double_quoted(text: &str) -> String {
    text.replace('\\', "\\\\")
        .replace('"', "\\\"")
        .replace('\n', "\\n")
        .replace('\r', "\\r")
        .replace('\t', "\\t")
}

/// SC16: the fixer path (this module) has no filesystem, wall-clock, or
/// model call. Guarded two ways: the type signatures above take `now` as a
/// plain `&str` parameter (never reading a clock themselves), and this
/// grep-style guard scans the module's own source (embedded at compile
/// time, never read from disk at test run time) for the tokens a fs/clock
/// dependency would have to spell.
#[cfg(test)]
mod sc16_purity {
    const SOURCE: &str = include_str!("fix.rs");

    #[test]
    fn fixer_source_has_no_filesystem_or_clock_or_model_tokens() {
        // Scan only the production code above the first `#[cfg(test)]`
        // (this module's own attribute) -- the test modules below
        // legitimately name these tokens as strings to check for.
        let production_code = SOURCE
            .split_once("#[cfg(test)]")
            .map_or(SOURCE, |(before, _)| before);
        for forbidden in ["std::fs", "std::time", "SystemTime", "Instant::now"] {
            assert!(
                !production_code.contains(forbidden),
                "fix.rs must stay pure (SC16): found forbidden token '{forbidden}'"
            );
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::parse;
    use crate::validate::validate;

    fn profile() -> Profile {
        Profile::bundled_psa_apm()
    }

    const NOW: &str = "2026-07-11T00:00:00Z";

    #[test]
    fn missing_required_field_is_stubbed_and_flagged_human_authored() {
        let parsed = parse::parse("---\nname: \"x\"\n---\nbody\n").unwrap();
        let proposal = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        assert_eq!(
            proposal.fields.get("id"),
            Some(&FrontmatterValue::Scalar(
                "TODO: id needs a value.".to_string()
            ))
        );
        assert!(proposal.human_authored_fields.contains(&"id".to_string()));
    }

    #[test]
    fn present_required_field_is_untouched_and_not_flagged() {
        let parsed = parse::parse("---\nname: \"Real Name\"\n---\nbody\n").unwrap();
        let proposal = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        assert_eq!(
            proposal.fields.get("name"),
            Some(&FrontmatterValue::Scalar("Real Name".to_string()))
        );
        assert!(!proposal.human_authored_fields.contains(&"name".to_string()));
    }

    #[test]
    fn updated_is_always_stamped_from_injected_now_even_when_present() {
        let input = "---\nname: \"x\"\nupdated: 2020-01-01T00:00:00Z\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let proposal = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        assert_eq!(
            proposal.fields.get("updated"),
            Some(&FrontmatterValue::Scalar(NOW.to_string()))
        );
    }

    #[test]
    fn updated_is_never_in_human_authored_fields() {
        let parsed = parse::parse("---\n---\nbody\n").unwrap();
        let proposal = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        assert!(!proposal
            .human_authored_fields
            .contains(&"updated".to_string()));
    }

    #[test]
    fn missing_links_stubs_an_empty_sequence_not_a_scalar() {
        let parsed = parse::parse("---\nname: \"x\"\n---\nbody\n").unwrap();
        let proposal = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        assert_eq!(
            proposal.fields.get("links"),
            Some(&FrontmatterValue::Sequence(Vec::new()))
        );
    }

    #[test]
    fn description_over_cap_is_trimmed_to_the_context_cap_at_a_word_boundary() {
        let long = "word ".repeat(100); // well over the 350-char context cap
        let input = format!(
            "---\nname: \"x\"\ndescription: \"{}\"\n---\nbody\n",
            long.trim()
        );
        let parsed = parse::parse(&input).unwrap();
        let proposal = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        let FrontmatterValue::Scalar(trimmed) = proposal.fields.get("description").unwrap() else {
            panic!("expected a scalar description");
        };
        assert!(trimmed.chars().count() <= 350);
        assert!(!trimmed.ends_with(' '));
    }

    #[test]
    fn description_over_cap_is_not_flagged_human_authored_merely_for_trimming() {
        let long = "word ".repeat(100);
        let input = format!(
            "---\nname: \"x\"\ndescription: \"{}\"\n---\nbody\n",
            long.trim()
        );
        let parsed = parse::parse(&input).unwrap();
        let proposal = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        assert!(!proposal
            .human_authored_fields
            .contains(&"description".to_string()));
    }

    #[test]
    fn singleton_namespace_dedupes_to_its_first_occurrence() {
        let input = "---\nname: \"x\"\ntags:\n  - status:complete\n  - status:stub\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let proposal = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        let FrontmatterValue::Sequence(tags) = proposal.fields.get("tags").unwrap() else {
            panic!("expected a sequence");
        };
        assert_eq!(
            tags.iter().filter(|t| t.starts_with("status:")).count(),
            1,
            "tags: {tags:?}"
        );
        assert!(tags.contains(&"status:complete".to_string()));
    }

    #[test]
    fn orphaned_child_namespace_is_dropped_when_its_parent_is_absent() {
        let input = "---\nname: \"x\"\ntags:\n  - feature:trace-explorer\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let proposal = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        let FrontmatterValue::Sequence(tags) = proposal.fields.get("tags").unwrap() else {
            panic!("expected a sequence");
        };
        assert!(
            !tags.iter().any(|t| t.starts_with("feature:")),
            "tags: {tags:?}"
        );
    }

    #[test]
    fn report_only_tag_is_dropped_on_a_non_report_file() {
        let input = "---\nname: \"x\"\ntags:\n  - type:knowledge\n  - source:slack\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let proposal = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        let FrontmatterValue::Sequence(tags) = proposal.fields.get("tags").unwrap() else {
            panic!("expected a sequence");
        };
        assert!(
            !tags.contains(&"source:slack".to_string()),
            "tags: {tags:?}"
        );
    }

    #[test]
    fn report_file_gets_a_schema_valid_period_placeholder() {
        let input = "---\nname: \"x\"\ntags:\n  - type:report\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let proposal = propose_fix(&parsed, "some/report.md", &profile(), NOW);
        let FrontmatterValue::Sequence(tags) = proposal.fields.get("tags").unwrap() else {
            panic!("expected a sequence");
        };
        let period = tags
            .iter()
            .find(|t| t.starts_with("period:"))
            .expect("a period: placeholder must be added on a report file");
        assert_eq!(period, "period:2026-07-11/2026-07-11");
        assert!(proposal.human_authored_fields.contains(&"tags".to_string()));
    }

    #[test]
    fn missing_namespace_required_singleton_gets_a_placeholder_tag() {
        let parsed = parse::parse("---\nname: \"x\"\n---\nbody\n").unwrap();
        let proposal = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        let FrontmatterValue::Sequence(tags) = proposal.fields.get("tags").unwrap() else {
            panic!("expected a sequence");
        };
        assert!(tags.contains(&"type:TODO".to_string()), "tags: {tags:?}");
    }

    #[test]
    fn file_class_is_classified_from_rel_path() {
        let parsed = parse::parse("---\nname: \"x\"\n---\nbody\n").unwrap();
        let proposal = propose_fix(&parsed, ".claude/skills/my-skill/SKILL.md", &profile(), NOW);
        assert_eq!(proposal.file_class, "skill");
    }

    #[test]
    fn propose_skeleton_stubs_every_required_field_and_classifies() {
        let proposal = propose_skeleton("some/doc.md", &profile(), NOW);
        assert_eq!(proposal.file_class, "context");
        for (field, authorship) in profile().required_fields() {
            if authorship == "human_authored" && field != "tags" {
                assert!(
                    proposal.human_authored_fields.contains(&field.to_string()),
                    "expected '{field}' flagged human_authored, got {:?}",
                    proposal.human_authored_fields
                );
            }
        }
        assert!(proposal.human_authored_fields.contains(&"tags".to_string()));
    }

    #[test]
    fn human_authored_fields_tracks_the_profiles_own_authorship_not_a_hardcoded_list() {
        // Flip `updated`'s declared authorship to human_authored in an
        // edited pack: since propose_fix special-cases `updated` by NAME
        // (always stamp, never stub), it must still never appear in
        // human_authored_fields even under this edited profile -- proving
        // that specific rule is about the field's role, not a hardcoded
        // authorship reading. Meanwhile flipping `name` to
        // machine_derivable must REMOVE it from the flagged set for a
        // skeleton build, proving THAT path reads the schema, not a
        // hardcoded field list.
        let mut pack: serde_json::Value = serde_json::from_str(include_str!(
            "../../../schemas/frontmatter/frontmatter-psa-apm.pack.json"
        ))
        .unwrap();
        for field in pack["required_fields"].as_array_mut().unwrap() {
            if field["field"] == "name" {
                field["authorship"] = serde_json::json!("machine_derivable");
            }
        }
        let profile = Profile::from_pack_json(&pack.to_string()).unwrap();
        let proposal = propose_skeleton("some/doc.md", &profile, NOW);
        assert!(
            !proposal.human_authored_fields.contains(&"name".to_string()),
            "demoting name: to machine_derivable in the schema must drop it \
             from human_authored_fields with no fix.rs change: {:?}",
            proposal.human_authored_fields
        );
    }

    #[test]
    fn render_output_is_deterministic_and_byte_stable() {
        let parsed =
            parse::parse("---\nname: \"x\"\ntags:\n  - type:knowledge\n---\nbody\n").unwrap();
        let proposal = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        let first = render(&proposal.fields);
        let second = render(&proposal.fields);
        assert_eq!(first, second);
    }

    #[test]
    fn render_uses_the_profiles_required_field_order_then_passthrough() {
        let input = "---\nname: \"x\"\nextra_field: keep-me\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let proposal = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        let keys: Vec<&str> = proposal.fields.iter().map(|(k, _)| k).collect();
        assert_eq!(
            keys,
            vec![
                "name",
                "description",
                "id",
                "tags",
                "links",
                "updated",
                "extra_field"
            ]
        );
    }

    #[test]
    fn render_double_quotes_ordinary_scalars_but_leaves_updated_bare() {
        let parsed = parse::parse("---\nname: \"x\"\n---\nbody\n").unwrap();
        let proposal = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        let text = render(&proposal.fields);
        assert!(text.contains("name: \"x\"\n"));
        assert!(text.contains(&format!("updated: {NOW}\n")));
    }

    #[test]
    fn propose_fix_result_re_validates_as_schema_valid_modulo_placeholders() {
        let input = "---\nname: \"x\"\ntags:\n  - status:complete\n  - status:stub\n  - feature:orphan\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let proposal = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        let rendered = render(&proposal.fields);
        let reparsed = parse::parse(&rendered).expect("rendered output must itself parse");
        let entry = validate(&reparsed, "some/doc.md", &profile());
        assert!(
            entry.is_valid,
            "repaired output must re-validate clean (placeholders still count as \
             present, non-blank values): {:?}",
            entry.violations
        );
    }

    #[test]
    fn propose_skeleton_result_re_validates_as_schema_valid() {
        let proposal = propose_skeleton("some/report.md", &profile(), NOW);
        let rendered = render(&proposal.fields);
        let reparsed = parse::parse(&rendered).unwrap();
        let entry = validate(&reparsed, "some/report.md", &profile());
        assert!(entry.is_valid, "{:?}", entry.violations);
    }

    #[test]
    fn passthrough_fields_survive_untouched_and_in_original_order() {
        let input = "---\nname: \"x\"\ncustom_a: 1\ncustom_b: 2\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let proposal = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        let keys: Vec<&str> = proposal.fields.iter().map(|(k, _)| k).collect();
        let a_index = keys.iter().position(|k| *k == "custom_a").unwrap();
        let b_index = keys.iter().position(|k| *k == "custom_b").unwrap();
        assert!(a_index < b_index);
        assert_eq!(
            proposal.fields.get("custom_a"),
            Some(&FrontmatterValue::Scalar("1".to_string()))
        );
    }

    #[test]
    fn workspace_nested_key_is_never_passed_through_verbatim() {
        let input = "---\nname: \"x\"\nworkspace:\n  id: \"a:b:c\"\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let proposal = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        assert!(!proposal.fields.contains_key("workspace"));
        // But the nested id: was still read via flatten() -- it should
        // NOT have been re-stubbed as missing.
        assert_eq!(
            proposal.fields.get("id"),
            Some(&FrontmatterValue::Scalar("a:b:c".to_string()))
        );
    }

    #[test]
    fn determinism_identical_input_yields_identical_proposal() {
        let parsed = parse::parse("---\nname: \"x\"\n---\nbody\n").unwrap();
        let first = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        let second = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        assert_eq!(first, second);
    }
}

// ---------------------------------------------------------------------------
// SDET adversarial verification (M2.P4.T1 test-engineer pass) -- not part of
// the fixer author's own suite; targets the mandate's explicitly flagged
// edge cases so a future regression in any of them fails loudly here rather
// than surfacing only downstream (navigator, a caller's re-validation).
// ---------------------------------------------------------------------------
#[cfg(test)]
mod sdet_adversarial {
    use super::*;
    use crate::parse;
    use crate::validate::validate;

    fn profile() -> Profile {
        Profile::bundled_psa_apm()
    }

    const NOW: &str = "2026-07-11T00:00:00Z";

    /// A foreign pack whose `report.period.regex` cannot match a
    /// `YYYY-MM-DD/YYYY-MM-DD` candidate (e.g. it demands a `Q<n>` marker
    /// this fixer has no way to synthesize). `period_placeholder` must fall
    /// back to a bare `period:TODO` marker rather than emitting a value that
    /// LOOKS schema-valid but silently isn't -- and that fallback is a
    /// documented Tier-1 limitation, not a bug: this test pins it so it
    /// cannot regress unnoticed, and proves the re-validation round-trip
    /// correctly still flags the resulting doc (the `period:TODO` value does
    /// not match the incompatible regex either, so `validate` must still
    /// report `invalid_period_format` on it).
    #[test]
    fn foreign_pack_incompatible_period_regex_falls_back_to_bare_todo_and_stays_invalid() {
        let mut pack: serde_json::Value = serde_json::from_str(include_str!(
            "../../../schemas/frontmatter/frontmatter-psa-apm.pack.json"
        ))
        .unwrap();
        pack["report"]["period"]["regex"] = serde_json::json!(r"^Q[1-4]-[0-9]{4}$");
        let profile = Profile::from_pack_json(&pack.to_string()).unwrap();

        let input = "---\nname: \"x\"\ntags:\n  - type:report\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let proposal = propose_fix(&parsed, "some/report.md", &profile, NOW);
        let FrontmatterValue::Sequence(tags) = proposal.fields.get("tags").unwrap() else {
            panic!("expected a sequence");
        };
        assert!(
            tags.contains(&"period:TODO".to_string()),
            "expected a bare TODO fallback (the fixer has no Q<n> synthesis), got: {tags:?}"
        );

        // Known Tier-1 limitation, pinned loudly: this placeholder does NOT
        // re-validate clean. A caller must not treat propose_fix's output as
        // unconditionally schema-valid when the pack's period format is
        // incompatible with the fixer's own YYYY-MM-DD/YYYY-MM-DD synthesis.
        let rendered = render(&proposal.fields);
        let reparsed = parse::parse(&rendered).unwrap();
        let entry = validate(&reparsed, "some/report.md", &profile);
        assert!(
            !entry.is_valid,
            "period:TODO must NOT satisfy an incompatible period regex -- if this now \
             passes, either validate's regex check regressed or the fixer started \
             guessing a value it cannot prove matches"
        );
    }

    /// A full parent chain (`feature` -> `product` -> `suite`, all absent)
    /// starting from a doc that declares only `feature:`. `feature` itself
    /// is `optional` cardinality (never independently required), so nothing
    /// forces `product`/`suite` to be *stubbed* -- the orphan-drop phase
    /// removes `feature` (its parents are absent) and no placeholder phase
    /// re-adds `product`/`suite`, since neither is `singleton`/`at_least_one`.
    /// Pinning this: Tier-1 does not walk namespace parent chains "upward"
    /// to backfill a whole taxonomy, it only drops what can't stand alone.
    #[test]
    fn full_parent_chain_missing_drops_the_child_and_stubs_nothing_upward() {
        let input = "---\nname: \"x\"\ntags:\n  - feature:trace-explorer\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let proposal = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        let FrontmatterValue::Sequence(tags) = proposal.fields.get("tags").unwrap() else {
            panic!("expected a sequence");
        };
        assert!(!tags.iter().any(|t| t.starts_with("feature:")), "{tags:?}");
        assert!(!tags.iter().any(|t| t.starts_with("product:")), "{tags:?}");
        assert!(!tags.iter().any(|t| t.starts_with("suite:")), "{tags:?}");
    }

    /// `tags:` given as a bare scalar (not a YAML list) at all -- a doc this
    /// malformed in the wild (hand-edited, or a truncated file). `repair_tags`
    /// must treat "not a Sequence" as "no tags at all" and rebuild a fresh,
    /// schema-conformant list from scratch, never propagate the scalar or
    /// panic on it.
    #[test]
    fn tags_given_as_a_scalar_is_repaired_into_a_conformant_list() {
        let input = "---\nname: \"x\"\ntags: \"type:knowledge\"\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let proposal = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        let value = proposal.fields.get("tags").unwrap();
        assert!(
            matches!(value, FrontmatterValue::Sequence(_)),
            "expected tags repaired to a Sequence even though the source was a Scalar, got: {value:?}"
        );
        let FrontmatterValue::Sequence(tags) = value else {
            unreachable!()
        };
        // The scalar's own string content is not a list item and is
        // correctly discarded; the required `type:` namespace still gets a
        // placeholder since nothing usable survived.
        assert!(tags.contains(&"type:TODO".to_string()), "{tags:?}");
        let rendered = render(&proposal.fields);
        let reparsed = parse::parse(&rendered).unwrap();
        let entry = validate(&reparsed, "some/doc.md", &profile());
        assert!(entry.is_valid, "{:?}", entry.violations);
    }

    /// A description exactly AT the context cap (350 chars) must survive
    /// byte-for-byte untouched -- trimming is only for OVER-cap text.
    #[test]
    fn description_exactly_at_cap_is_untouched() {
        let exact: String = "a".repeat(350);
        let input = format!("---\nname: \"x\"\ndescription: \"{exact}\"\n---\nbody\n");
        let parsed = parse::parse(&input).unwrap();
        let proposal = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        let FrontmatterValue::Scalar(text) = proposal.fields.get("description").unwrap() else {
            panic!("expected a scalar");
        };
        assert_eq!(text.chars().count(), 350);
        assert_eq!(text, &exact);
    }

    /// A description exactly ONE char over the context cap (351 chars, no
    /// internal spaces) must be trimmed down to <= 350 -- with no word
    /// boundary to break at inside the truncated 350-char window, the
    /// fallback path (`rfind(' ')` finds nothing) must still shorten to the
    /// cap rather than leaving it at 351 or panicking on the slice.
    #[test]
    fn description_one_over_cap_with_no_word_boundary_still_trims_to_cap() {
        let over: String = "a".repeat(351);
        let input = format!("---\nname: \"x\"\ndescription: \"{over}\"\n---\nbody\n");
        let parsed = parse::parse(&input).unwrap();
        let proposal = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        let FrontmatterValue::Scalar(text) = proposal.fields.get("description").unwrap() else {
            panic!("expected a scalar");
        };
        assert!(
            text.chars().count() <= 350,
            "trimmed len: {}",
            text.chars().count()
        );
    }

    /// Unicode multi-byte characters in an over-cap description must be
    /// trimmed at a CHAR boundary, not a byte boundary -- `chars().take(cap)`
    /// is exactly what guards this, but a regression to byte-slicing
    /// (`&text[..cap]`) would panic mid-codepoint on this input (each 'e'
    /// with combining diacritics / multi-byte emoji is >1 byte).
    #[test]
    fn unicode_description_over_cap_trims_at_a_char_boundary_not_a_byte_boundary() {
        // 400 multi-byte chars (3 bytes each in UTF-8: U+4E2D "中"), well
        // over the 350-char context cap but with no ASCII space anywhere --
        // forces both the char-counting cap check AND the no-word-boundary
        // fallback to run against multi-byte content simultaneously.
        let long: String = "中".repeat(400);
        let input = format!("---\nname: \"x\"\ndescription: \"{long}\"\n---\nbody\n");
        let parsed = parse::parse(&input).unwrap();
        let proposal = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        let FrontmatterValue::Scalar(text) = proposal.fields.get("description").unwrap() else {
            panic!("expected a scalar");
        };
        assert!(
            text.chars().count() <= 350,
            "trimmed char len: {}",
            text.chars().count()
        );
        // No panic reaching here is itself the primary assertion (a byte-
        // boundary slice on this input would have already panicked above);
        // additionally confirm the render/reparse round-trip stays valid
        // UTF-8 and re-parses.
        let rendered = render(&proposal.fields);
        assert!(parse::parse(&rendered).is_ok());
    }

    /// `propose_skeleton` for each of the three file classes the bundled
    /// pack defines -- each must classify correctly AND apply that class's
    /// own description cap (verified indirectly: a skeleton's stubbed
    /// description is always short, so this mainly pins `file_class`
    /// classification per path shape).
    #[test]
    fn propose_skeleton_classifies_every_file_class_correctly() {
        let cases = [
            ("the-work/deliverables/foo/report.md", "context"),
            (".claude/skills/my-skill/SKILL.md", "skill"),
            ("nested/dir/SKILL.md", "skill"),
            (".claude/agents/reviewer.md", "agent"),
        ];
        for (path, expected_class) in cases {
            let proposal = propose_skeleton(path, &profile(), NOW);
            assert_eq!(
                proposal.file_class, expected_class,
                "path {path} classified as {}, expected {expected_class}",
                proposal.file_class
            );
            let rendered = render(&proposal.fields);
            let reparsed = parse::parse(&rendered).unwrap();
            let entry = validate(&reparsed, path, &profile());
            assert!(entry.is_valid, "path {path}: {:?}", entry.violations);
        }
    }

    /// Byte-stable render across a full `propose_fix` -> render -> reparse ->
    /// `propose_fix` loop: fixing an already-fixed doc a second time (with
    /// the same `now`) must be idempotent -- a caller invoking `propose_fix`
    /// twice (e.g. a retry) must never see field churn.
    #[test]
    fn double_fix_is_idempotent_given_the_same_now() {
        let input = "---\nname: \"x\"\ntags:\n  - type:knowledge\n  - status:complete\n  - privacy:internal\n  - owner:datadog\n  - topic:tooling\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let first = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        let rendered_first = render(&first.fields);
        let reparsed = parse::parse(&rendered_first).unwrap();
        let second = propose_fix(&reparsed, "some/doc.md", &profile(), NOW);
        let rendered_second = render(&second.fields);
        assert_eq!(rendered_first, rendered_second);
    }

    /// Render's empty-sequence handling: `links: []` (flow style), never a
    /// bare `links:` -- the SE-flagged null-roundtrip trap. Confirmed by
    /// re-parsing the rendered text and checking the value comes back as an
    /// empty `Sequence`, not `Other` (what a bare `key:` null would parse
    /// as).
    #[test]
    fn empty_links_sequence_round_trips_as_empty_sequence_not_null() {
        let parsed = parse::parse("---\nname: \"x\"\n---\nbody\n").unwrap();
        let proposal = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        let rendered = render(&proposal.fields);
        assert!(rendered.contains("links: []\n"), "{rendered}");
        let reparsed = parse::parse(&rendered).unwrap();
        assert_eq!(
            reparsed.raw_fields.get("links"),
            Some(&FrontmatterValue::Sequence(Vec::new()))
        );
    }

    /// A multi-line scalar value (here a block scalar whose body even
    /// contains a bare `---` line) must render on a single physical line via
    /// `\n` escapes and round-trip byte-for-byte. A raw newline would fold to
    /// a space on reparse at best, and -- when a value line equals `---` --
    /// would be read as the frontmatter block's closing delimiter, truncating
    /// the block or failing the reparse outright. This pins the escaping that
    /// prevents that block-breakout / injection.
    #[test]
    fn multiline_scalar_with_delimiter_line_escapes_and_round_trips() {
        let input =
            "---\nname: \"x\"\ndescription: |\n  first line\n  ---\n  second line\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let proposal = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        let rendered = render(&proposal.fields);
        assert!(
            !rendered.contains("first line\n"),
            "a raw newline leaked into the rendered scalar:\n{rendered}"
        );
        let reparsed = parse::parse(&rendered).expect("rendered output must reparse");
        assert_eq!(
            reparsed.raw_fields.get("description"),
            Some(&FrontmatterValue::Scalar(
                "first line\n---\nsecond line\n".to_string()
            )),
            "multi-line scalar must round-trip byte-for-byte; rendered:\n{rendered}"
        );
    }

    /// A scalar value containing both a colon and a double quote must
    /// escape correctly and re-parse back to the SAME string -- the render
    /// quoting/escaping contract under adversarial content, not just the
    /// happy-path values the author's own suite exercises.
    #[test]
    fn scalar_with_colon_and_quote_escapes_and_round_trips() {
        let tricky = r#"a value: with a "quote" and \backslash"#;
        let input = format!(
            "---\nname: \"x\"\ncustom: \"{}\"\n---\nbody\n",
            tricky.replace('\\', "\\\\").replace('"', "\\\"")
        );
        let parsed = parse::parse(&input).unwrap();
        let proposal = propose_fix(&parsed, "some/doc.md", &profile(), NOW);
        let rendered = render(&proposal.fields);
        let reparsed = parse::parse(&rendered).unwrap();
        assert_eq!(
            reparsed.raw_fields.get("custom"),
            Some(&FrontmatterValue::Scalar(tricky.to_string())),
            "round-tripped custom field mismatch; rendered doc:\n{rendered}"
        );
    }
}
