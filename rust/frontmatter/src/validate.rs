//! The sole frontmatter validator: interprets a [`Profile`]
//! (the declarative schema, `crate::profile`) over one file's already-parsed
//! [`ParsedFrontmatter`] (`crate::parse`) and emits a [`FrontmatterEntry`]
//! verdict. Every fact this module checks (which fields are required, how
//! tag namespaces cascade, what a description cap is, which paths are
//! exempt) is read off `Profile`, never hardcoded here -- see
//! `crate::profile`'s module doc and this module's `data_driven` test for
//! the "schema edit, no code edit" property the build plan requires.
//!
//! # Scope (this task)
//! Emits these violation codes, with the field, message text, and emission
//! ORDER the declarative schema and task spec pin: `MISSING_REQUIRED_FIELD`,
//! `DESCRIPTION_OVER_CAP`, `DESCRIPTION_NOT_TOP_LEVEL`, the five
//! tag-namespace codes (`MISSING_REQUIRED_TAG`, `MULTIPLE_SINGLE_VALUE_TAGS`,
//! `ORPHAN_NAMESPACE_TAG`, `REPORT_ONLY_TAG_MISUSED`,
//! `INVALID_PERIOD_FORMAT`), and the tags-not-a-list code. Deliberately
//! out of scope: `DESCRIPTION_INVALID_SCALAR` (raw YAML scalar-quoting
//! style -- this crate's parser already normalizes scalar shape, so that
//! raw-text quoting distinction doesn't exist here in the same form),
//! `INVALID_UPDATED_FORMAT` (timestamp format), and
//! `MISSING_FRONTMATTER`/`MALFORMED_FRONTMATTER` (raw-text-level concerns
//! `crate::parse::parse`'s `Result` already reports -- see
//! [`ScanOutcome`]/[`fold`] below for how a caller folds that into the
//! coverage rollup instead).
//!
//! `proposed_frontmatter` on [`FrontmatterEntry`] always stays `None` from
//! [`validate`] -- it's an `Option` so a future Tier-1 fixer can populate it
//! without changing the shape.

use crate::profile::{Cardinality, FacetType, Profile, RuleSet};
use crate::value::{FrontmatterValue, RawFields};
use crate::ParsedFrontmatter;
use time::Date;

/// One frontmatter schema violation: a stable `code`, the `field` it's
/// about, and a human-readable `message`. This three-field shape is the
/// stable contract downstream consumers (e.g. the CLI's human-readable
/// output) map from, so none needs an extra lookup to render it.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Violation {
    /// A stable, uppercase-snake-case identifier (e.g.
    /// `"MISSING_REQUIRED_FIELD"`) -- never localized, safe to match on.
    pub code: String,
    /// The frontmatter field or tag namespace the violation is about.
    pub field: String,
    /// Human-readable detail, safe to show a user as-is.
    pub message: String,
}

/// The verdict for one file: its derived file class, whether it's
/// conformant, the violations found (empty when conformant), and a
/// reserved slot for a future Tier-1-fixer-produced replacement
/// frontmatter block (always `None` from [`validate`]).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct FrontmatterEntry {
    /// The file class [`validate`] derived from the file's path via the
    /// pack's `file_class` rules (e.g. `"context"`, `"skill"`, `"agent"`,
    /// or any other class a foreign pack declares). Drives the
    /// description-length cap; carries no other meaning on its own.
    pub file_class: String,
    /// `true` iff `violations` is empty.
    pub is_valid: bool,
    /// Every violation found, in the cascade's fixed emission order (see
    /// the module doc's scope list) -- deterministic for identical input.
    pub violations: Vec<Violation>,
    /// Reserved for a future Tier-1 fixer. Always `None` from [`validate`].
    pub proposed_frontmatter: Option<String>,
}

impl FrontmatterEntry {
    fn valid(file_class: String) -> Self {
        Self {
            file_class,
            is_valid: true,
            violations: Vec::new(),
            proposed_frontmatter: None,
        }
    }

    fn from_violations(file_class: String, violations: Vec<Violation>) -> Self {
        let is_valid = violations.is_empty();
        Self {
            file_class,
            is_valid,
            violations,
            proposed_frontmatter: None,
        }
    }
}

/// A rollup of validation outcomes across a set of scanned files. The
/// directory walk that produces the per-file [`ScanOutcome`]s is a
/// caller's concern (e.g. a `lint` command) -- this crate only provides
/// the shape and the per-outcome [`fold`] step.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub struct CoverageRollup {
    /// Total files folded in, regardless of outcome.
    pub scanned: u64,
    /// Files with a [`FrontmatterEntry`] where `is_valid` is `true`.
    pub valid: u64,
    /// Files with a [`FrontmatterEntry`] where `is_valid` is `false`.
    pub invalid: u64,
    /// Files with no usable frontmatter to validate at all -- either
    /// [`crate::parse::parse`] returned an `Err` (unclosed delimiter,
    /// malformed YAML, too-deeply-nested, non-mapping top level), or it
    /// returned `Ok` with an empty `raw_fields` (no `---`-fenced block
    /// found, or an empty block `---\n---`; `crate::parse` cannot itself
    /// distinguish those two -- both mean "nothing to validate").
    pub missing_frontmatter: u64,
}

/// One file's outcome, as folded into a [`CoverageRollup`] by [`fold`].
///
/// Deliberately separate from [`FrontmatterEntry`]: a file with no
/// parsable/present frontmatter never reaches [`validate`] at all (there
/// is no [`ParsedFrontmatter`] to hand it, or handing it an empty one
/// would double-count against `missing_frontmatter` AND `invalid`), so the
/// caller folding a directory scan's results passes [`ScanOutcome::Missing`]
/// directly instead of contriving an entry.
#[derive(Debug, Clone, Copy)]
pub enum ScanOutcome<'a> {
    /// No frontmatter to validate -- see [`CoverageRollup::missing_frontmatter`].
    Missing,
    /// A validated file's verdict.
    Entry(&'a FrontmatterEntry),
}

/// Folds one file's [`ScanOutcome`] into `rollup`, in place.
pub fn fold(rollup: &mut CoverageRollup, outcome: ScanOutcome<'_>) {
    rollup.scanned += 1;
    match outcome {
        ScanOutcome::Missing => rollup.missing_frontmatter += 1,
        ScanOutcome::Entry(entry) if entry.is_valid => rollup.valid += 1,
        ScanOutcome::Entry(_) => rollup.invalid += 1,
    }
}

// ---------------------------------------------------------------------------
// DROPPED: NON_STRING_FRONTMATTER_KEY.
// ---------------------------------------------------------------------------

// This check had no reliable trigger. `crate::parse` stringifies EVERY
// mapping key, falling back to a `{key:?}` Debug rendering only for a key
// that was itself a YAML sequence or mapping. The check sniffed that
// fallback via `key.contains('(')` -- but a legitimate plain-string key
// containing a literal `(` (e.g. an author's ad hoc `"notes (draft)"`
// field) produces the identical substring, so it false-positived on
// ordinary human-authored keys, textually indistinguishable from the rare
// shape it meant to catch. Dropped rather than fixed: catching the real
// case correctly needs `crate::parse` to carry a distinct non-string-key
// signal instead of Debug-format sniffing (a value-model change outside
// this task's scope). A non-string-keyed field is now simply inert -- it
// matches no required field name and joins no namespace rule, so it emits
// no violation, which is also what the canonical validator does.

/// The tags-not-a-list code. Not part of the declarative schema's cascade
/// `codes` list -- it guards a structural precondition (`tags` must be a
/// sequence before the namespace cascade can run at all) -- so it is
/// pinned here as a named constant rather than read from the profile like
/// the cascade codes.
const CODE_TAGS_NOT_A_LIST: &str = "TAGS_NOT_A_LIST";

/// The description-not-top-level code. Like [`CODE_TAGS_NOT_A_LIST`], it is
/// not in the declarative schema's cascade `codes` list -- it guards a
/// placement rule (`description` must be a top-level field, not nested
/// under `workspace:`) rather than a cascade step -- so it is pinned here
/// as a named constant.
const CODE_DESCRIPTION_NOT_TOP_LEVEL: &str = "DESCRIPTION_NOT_TOP_LEVEL";

// ---------------------------------------------------------------------------
// The effective (workspace:-flattened) field view
// ---------------------------------------------------------------------------

/// A flattened view of a document's fields: every top-level field, with any
/// same-named field nested under a top-level `workspace:` mapping
/// OVERRIDING the top-level one. Skills/Agents/Rules nest their
/// id/tags/links/updated/description under `workspace:`; validation always
/// runs against this merged view so one rule set covers both styles.
pub(crate) struct Effective<'a> {
    pairs: Vec<(String, &'a FrontmatterValue)>,
}

impl<'a> Effective<'a> {
    pub(crate) fn get(&self, key: &str) -> Option<&'a FrontmatterValue> {
        self.pairs.iter().find(|(k, _)| k == key).map(|(_, v)| *v)
    }
}

pub(crate) fn flatten(raw: &RawFields) -> Effective<'_> {
    let mut pairs: Vec<(String, &FrontmatterValue)> = raw
        .iter()
        .filter(|(k, _)| *k != "workspace")
        .map(|(k, v)| (k.to_string(), v))
        .collect();
    if let Some(FrontmatterValue::Mapping(inner)) = raw.get("workspace") {
        for (k, v) in inner.iter() {
            if let Some(existing) = pairs.iter_mut().find(|(ek, _)| ek == k) {
                existing.1 = v;
            } else {
                pairs.push((k.to_string(), v));
            }
        }
    }
    Effective { pairs }
}

/// True if `value` counts as absent for a required field: an outright-absent
/// key (`None`) or a blank/whitespace-only scalar string is missing; a
/// present (even empty) sequence or mapping is not. [`FrontmatterValue::Other`]
/// -- a YAML explicit null (`key:` with nothing after it) -- also counts as
/// missing, since "key present but carrying no value" is the same as
/// absence for a required-field check.
pub(crate) fn is_missing_value(value: Option<&FrontmatterValue>) -> bool {
    match value {
        Some(FrontmatterValue::Scalar(s)) => s.trim().is_empty(),
        Some(FrontmatterValue::Sequence(_) | FrontmatterValue::Mapping(_)) => false,
        None | Some(FrontmatterValue::Other) => true,
    }
}

// ---------------------------------------------------------------------------
// file_class classification (glob matching)
// ---------------------------------------------------------------------------

/// Classifies `rel_path` via the pack's `file_class` rules: first matching
/// glob wins (array order), else `default` (see the pack file's
/// `file_class.note`). Matching itself is `profile.globs`' pre-compiled
/// `GlobSet` (see `crate::profile::CompiledGlobs`); this function only
/// resolves the winning rule's `class`.
pub(crate) fn classify_file_class(rel_path: &str, profile: &Profile) -> String {
    match profile.globs.file_class_rule_index(rel_path) {
        Some(index) => profile.pack.file_class.rules[index].class.clone(),
        None => profile.pack.file_class.default.clone(),
    }
}

// ---------------------------------------------------------------------------
// Exempt taxonomy
// ---------------------------------------------------------------------------

/// True if `rel_path` is exempt from the completeness/tag-cascade checks,
/// per the pack's `exempt` vocabulary: basename in `filenames`, any path
/// component in `dir_components`, or the POSIX path matches a `path_globs`
/// entry (via `profile.globs`' pre-compiled `GlobSet`).
fn is_exempt(rel_path: &str, profile: &Profile) -> bool {
    let exempt = &profile.pack.exempt;
    let basename = rel_path.rsplit('/').next().unwrap_or(rel_path);
    if exempt.filenames.iter().any(|f| f == basename) {
        return true;
    }
    if rel_path
        .split('/')
        .any(|part| exempt.dir_components.iter().any(|d| d == part))
    {
        return true;
    }
    profile.globs.exempt_path_matches(rel_path)
}

// ---------------------------------------------------------------------------
// Message-template rendering
// ---------------------------------------------------------------------------

/// Substitutes `{key}` placeholders in `template` with their string value --
/// the byte-identical-rendering contract `schemas/frontmatter/README.md`
/// specifies for a message template.
fn render(template: &str, subs: &[(&str, &str)]) -> String {
    let mut out = template.to_string();
    for (key, value) in subs {
        out = out.replace(&format!("{{{key}}}"), value);
    }
    out
}

/// Renders a list of tag strings in the reference emitter's list format --
/// `['type:a', 'type:b']` -- so the emitted message is byte-identical to it
/// (the message-parity contract). Each value is single-quoted, unless it
/// contains a `'` and no `"` in which case it is double-quoted; backslashes
/// and the delimiter quote are escaped. Real tag values are plain
/// `namespace:value` ASCII with neither quote character, so this covers
/// every case this crate's callers can produce; it does not reproduce the
/// control-character (`\n`/`\t`/...) escaping that format applies to values,
/// which no legitimate tag value contains.
fn python_repr_list(values: &[String]) -> String {
    let rendered: Vec<String> = values.iter().map(|s| python_repr_str(s)).collect();
    format!("[{}]", rendered.join(", "))
}

fn python_repr_str(s: &str) -> String {
    let quote = if s.contains('\'') && !s.contains('"') {
        '"'
    } else {
        '\''
    };
    let mut out = String::with_capacity(s.len() + 2);
    out.push(quote);
    for c in s.chars() {
        if c == '\\' || c == quote {
            out.push('\\');
        }
        out.push(c);
    }
    out.push(quote);
    out
}

// ---------------------------------------------------------------------------
// Tag-namespace cascade: the ordered violation phases, driven entirely by
// the pack's `namespaces` array order and its general conditional
// `rule_sets` rather than any namespace vocabulary or report-shaped
// assumption hardcoded here.
// ---------------------------------------------------------------------------

/// Groups tag strings by namespace (the part before the first `:`),
/// preserving each namespace's tags in source order. A tag with no `:` is
/// dropped -- it cannot participate in any namespace rule. Every value this
/// crate's parser puts in a `tags:` sequence is already a `String` (see
/// `crate::value`), so the only filter that does any work here is "the tag
/// contains a `:`".
fn group_by_namespace(tags: &[String]) -> Vec<(String, Vec<String>)> {
    let mut groups: Vec<(String, Vec<String>)> = Vec::new();
    for tag in tags {
        let Some((ns, _)) = tag.split_once(':') else {
            continue;
        };
        if let Some(entry) = groups.iter_mut().find(|(name, _)| name == ns) {
            entry.1.push(tag.clone());
        } else {
            groups.push((ns.to_string(), vec![tag.clone()]));
        }
    }
    groups
}

fn group_get<'a>(groups: &'a [(String, Vec<String>)], ns: &str) -> &'a [String] {
    groups
        .iter()
        .find(|(name, _)| name == ns)
        .map_or(&[], |(_, tags)| tags.as_slice())
}

/// The full ordered tag cascade -- singleton, at-least-one,
/// parent-dependency, then the three conditional rule-set phases
/// (forbidden-unless-matched, required, value-format) -- driven entirely by
/// `profile.pack.namespaces`' array order and `profile.pack.rule_sets`, with
/// message wording read from `profile.core`'s cascade step templates (and,
/// for value-format failures, the pack's own value-format `message`).
/// Delegates each cascade step to its own phase function (below), purely to
/// keep this entry point short -- the ORDER these phases run in, not their
/// internal size, is what the emission-order contract pins.
fn tag_rule_violations(tags: &[String], profile: &Profile) -> Vec<Violation> {
    let mut violations = Vec::new();
    let groups = group_by_namespace(tags);

    singleton_cardinality_phase(&groups, profile, &mut violations);
    at_least_one_cardinality_phase(&groups, profile, &mut violations);
    parent_dependency_phase(&groups, profile, &mut violations);
    rule_set_forbidden_phase(&groups, profile, &mut violations);
    rule_set_required_phase(&groups, profile, &mut violations);
    rule_set_value_format_phase(&groups, profile, &mut violations);

    violations
}

/// True if `groups` (the file's tags, grouped by namespace) match `rule_set`
/// -- i.e. carry a tag `match.namespace:match.value`. The single generic
/// predicate every rule-set phase branches on; there is no report-named
/// special case.
fn rule_set_matches(groups: &[(String, Vec<String>)], rule_set: &RuleSet) -> bool {
    group_get(groups, &rule_set.match_criteria.namespace)
        .iter()
        .any(|t| t.split_once(':').map(|(_, v)| v) == Some(rule_set.match_criteria.value.as_str()))
}

/// The rendered `{match}` substitution for a rule-set's messages -- its
/// `match` as `namespace:value` (e.g. `type:report`).
fn rule_set_match_label(rule_set: &RuleSet) -> String {
    format!(
        "{}:{}",
        rule_set.match_criteria.namespace, rule_set.match_criteria.value
    )
}

fn singleton_cardinality_phase(
    groups: &[(String, Vec<String>)],
    profile: &Profile,
    violations: &mut Vec<Violation>,
) {
    let Some(step) = profile.core.cascade_step("singleton_cardinality") else {
        return;
    };
    for ns in profile
        .pack
        .namespaces
        .iter()
        .filter(|n| n.cardinality == Cardinality::Singleton)
    {
        let count = group_get(groups, &ns.name).len();
        if count == 0 {
            if let Some(emit) = step.emit(0) {
                violations.push(Violation {
                    code: emit.code.clone(),
                    field: ns.name.clone(),
                    message: render(&emit.message, &[("namespace", &ns.name)]),
                });
            }
        } else if count > 1 {
            if let Some(emit) = step.emit(1) {
                let tags_repr = python_repr_list(group_get(groups, &ns.name));
                violations.push(Violation {
                    code: emit.code.clone(),
                    field: ns.name.clone(),
                    message: render(
                        &emit.message,
                        &[
                            ("namespace", &ns.name),
                            ("count", &count.to_string()),
                            ("tags", &tags_repr),
                        ],
                    ),
                });
            }
        }
    }
}

fn at_least_one_cardinality_phase(
    groups: &[(String, Vec<String>)],
    profile: &Profile,
    violations: &mut Vec<Violation>,
) {
    let Some(step) = profile.core.cascade_step("at_least_one_cardinality") else {
        return;
    };
    for ns in profile
        .pack
        .namespaces
        .iter()
        .filter(|n| n.cardinality == Cardinality::AtLeastOne)
    {
        if group_get(groups, &ns.name).is_empty() {
            if let Some(emit) = step.emit(0) {
                violations.push(Violation {
                    code: emit.code.clone(),
                    field: ns.name.clone(),
                    message: render(&emit.message, &[("namespace", &ns.name)]),
                });
            }
        }
    }
}

fn parent_dependency_phase(
    groups: &[(String, Vec<String>)],
    profile: &Profile,
    violations: &mut Vec<Violation>,
) {
    let Some(step) = profile.core.cascade_step("parent_dependency") else {
        return;
    };
    for child in profile
        .pack
        .namespaces
        .iter()
        .filter(|n| !n.parents.is_empty())
    {
        if group_get(groups, &child.name).is_empty() {
            continue;
        }
        for parent in &child.parents {
            if group_get(groups, parent).is_empty() {
                if let Some(emit) = step.emit(0) {
                    violations.push(Violation {
                        code: emit.code.clone(),
                        field: child.name.clone(),
                        message: render(
                            &emit.message,
                            &[("child", &child.name), ("parent", parent)],
                        ),
                    });
                }
            }
        }
    }
}

/// For each rule-set, on a NON-matching file, every
/// `apply.forbidden_unless_matched` namespace present is a misuse -- the
/// inverse of the require direction (a namespace valid only on matching
/// files). Iterates rule-sets in pack order, then each rule-set's forbidden
/// list in its declared order.
fn rule_set_forbidden_phase(
    groups: &[(String, Vec<String>)],
    profile: &Profile,
    violations: &mut Vec<Violation>,
) {
    let Some(step) = profile.core.cascade_step("rule_set_forbidden") else {
        return;
    };
    for rule_set in &profile.pack.rule_sets {
        if rule_set_matches(groups, rule_set) {
            continue;
        }
        let label = rule_set_match_label(rule_set);
        for ns in &rule_set.apply.forbidden_unless_matched {
            if !group_get(groups, ns).is_empty() {
                if let Some(emit) = step.emit(0) {
                    violations.push(Violation {
                        code: emit.code.clone(),
                        field: ns.clone(),
                        message: render(&emit.message, &[("namespace", ns), ("match", &label)]),
                    });
                }
            }
        }
    }
}

/// For each rule-set, on a MATCHING file, every `apply.require_namespaces`
/// entry that is absent is a `MISSING_REQUIRED_TAG`.
fn rule_set_required_phase(
    groups: &[(String, Vec<String>)],
    profile: &Profile,
    violations: &mut Vec<Violation>,
) {
    let Some(step) = profile.core.cascade_step("rule_set_required") else {
        return;
    };
    for rule_set in &profile.pack.rule_sets {
        if !rule_set_matches(groups, rule_set) {
            continue;
        }
        let label = rule_set_match_label(rule_set);
        for ns in &rule_set.apply.require_namespaces {
            if group_get(groups, ns).is_empty() {
                if let Some(emit) = step.emit(0) {
                    violations.push(Violation {
                        code: emit.code.clone(),
                        field: ns.clone(),
                        message: render(&emit.message, &[("namespace", ns), ("match", &label)]),
                    });
                }
            }
        }
    }
}

/// For each rule-set, on a MATCHING file, every `apply.value_formats` entry
/// checks its namespace's tags against the compiled regex; a
/// `date_interval`-typed namespace is additionally calendar-checked. The
/// code comes from the core cascade step; the shape-failure message comes
/// from the pack's own value-format `message` (`{value}` = the whole tag),
/// while the calendar-failure message is the interval-type semantic pinned
/// here.
fn rule_set_value_format_phase(
    groups: &[(String, Vec<String>)],
    profile: &Profile,
    violations: &mut Vec<Violation>,
) {
    let Some(step) = profile.core.cascade_step("rule_set_value_format") else {
        return;
    };
    let Some(emit) = step.emit(0) else {
        return;
    };
    for (rule_index, rule_set) in profile.pack.rule_sets.iter().enumerate() {
        if !rule_set_matches(groups, rule_set) {
            continue;
        }
        for (format_index, value_format) in rule_set.apply.value_formats.iter().enumerate() {
            // Parallel-index into the profile's compiled patterns (built in
            // lockstep with `pack.rule_sets` -- see `Profile`'s field doc).
            let pattern = &profile.rule_set_value_patterns[rule_index][format_index];
            let is_interval = profile.namespace_facet_type(&value_format.namespace)
                == Some(FacetType::DateInterval);
            for tag in group_get(groups, &value_format.namespace) {
                let value = tag.split_once(':').map_or("", |(_, v)| v);
                let shape_valid = pattern.is_match(value);
                let calendar_ok = !is_interval || is_real_calendar_interval(value);
                if !shape_valid || !calendar_ok {
                    let message = if shape_valid && is_interval {
                        format!("'{tag}' is not a real YYYY-MM-DD/YYYY-MM-DD calendar interval")
                    } else {
                        render(&value_format.message, &[("value", tag)])
                    };
                    violations.push(Violation {
                        code: emit.code.clone(),
                        field: value_format.namespace.clone(),
                        message,
                    });
                }
            }
        }
    }
}

/// Whether `value` (already shape-valid: `YYYY-MM-DD/YYYY-MM-DD`) names a
/// real, non-inverted calendar interval -- both endpoints parse as real
/// `time::Date`s (rejecting e.g. month 13, February 30th, a non-leap-year
/// February 29th, or a signed year) and `start <= end`.
fn is_real_calendar_interval(value: &str) -> bool {
    let Some((start, end)) = value.split_once('/') else {
        return false;
    };
    let (Some(start), Some(end)) = (parse_calendar_date(start), parse_calendar_date(end)) else {
        return false;
    };
    start <= end
}

/// Parses `s` as a real `YYYY-MM-DD` civil date, `None` if it names a date
/// that doesn't exist on the calendar. `time`'s `[year]` component permits
/// an optional leading sign regardless of representation; the workspace's
/// `period:` values are always unsigned, so a leading non-digit is rejected
/// before the calendar-validating parse runs.
fn parse_calendar_date(s: &str) -> Option<Date> {
    use time::macros::format_description;
    if !s.starts_with(|c: char| c.is_ascii_digit()) {
        return None;
    }
    Date::parse(s, format_description!("[year]-[month]-[day]")).ok()
}

// ---------------------------------------------------------------------------
// The entry point
// ---------------------------------------------------------------------------

/// Validates `parsed`'s frontmatter against `profile`, deriving `file_class`
/// from `rel_path`. Never panics on any `ParsedFrontmatter` input (the
/// parser already guarantees no-panic parsing; nothing here re-introduces
/// one -- every lookup is by-name against `Vec`/`HashMap` and every
/// arithmetic operation is on bounded, checked lengths).
///
/// A file's frontmatter having no `---`-fenced block at all, or a block
/// that fails to parse, is NOT this function's concern -- `crate::parse::parse`
/// already reports that as an `Err` (or, for an empty-but-present block,
/// an `Ok` with empty `raw_fields`, indistinguishable from "no block" at
/// this layer); a caller who already knows a file has no usable
/// frontmatter folds [`ScanOutcome::Missing`] directly rather than
/// constructing a placeholder `ParsedFrontmatter` to hand this function.
#[must_use]
pub fn validate(parsed: &ParsedFrontmatter, rel_path: &str, profile: &Profile) -> FrontmatterEntry {
    let file_class = classify_file_class(rel_path, profile);

    // independent_checks order: exempt_gate first -- an exempt file skips
    // every other check outright (schemas/frontmatter/frontmatter-core.schema.json
    // `independent_checks.exempt_gate`).
    if is_exempt(rel_path, profile) {
        return FrontmatterEntry::valid(file_class);
    }

    let mut violations = Vec::new();

    let effective = flatten(&parsed.raw_fields);

    // required_fields, in pack-declared order.
    for rf in &profile.pack.required_fields {
        if is_missing_value(effective.get(&rf.field)) {
            violations.push(Violation {
                code: "MISSING_REQUIRED_FIELD".to_string(),
                field: rf.field.clone(),
                message: render(
                    &profile.core.mechanisms.required_fields.message,
                    &[("field", &rf.field)],
                ),
            });
        }
    }

    // description_cap, then description_not_top_level -- both gated on
    // description being present at all (the `!is_missing_value` guard
    // below).
    let description = effective.get("description");
    if !is_missing_value(description) {
        if let (Some(FrontmatterValue::Scalar(text)), Some(cap)) =
            (description, profile.pack.description_caps.get(&file_class))
        {
            let length = text.chars().count();
            if length as u64 > cap {
                violations.push(Violation {
                    code: "DESCRIPTION_OVER_CAP".to_string(),
                    field: "description".to_string(),
                    message: render(
                        &profile.core.mechanisms.description_caps.message,
                        &[
                            ("length", &length.to_string()),
                            ("file_class", &file_class),
                            ("cap", &cap.to_string()),
                        ],
                    ),
                });
            }
        }

        let top_level_has_description = parsed.raw_fields.contains_key("description");
        if !top_level_has_description {
            violations.push(Violation {
                code: CODE_DESCRIPTION_NOT_TOP_LEVEL.to_string(),
                field: "description".to_string(),
                message: "description must be a top-level frontmatter field".to_string(),
            });
        }
    }

    // tags: not-a-list, or the full namespace cascade.
    match effective.get("tags") {
        Some(FrontmatterValue::Sequence(tags)) => {
            violations.extend(tag_rule_violations(tags, profile));
        }
        Some(_) => {
            violations.push(Violation {
                code: CODE_TAGS_NOT_A_LIST.to_string(),
                field: "tags".to_string(),
                message: "tags must be a YAML list of 'namespace:value' strings".to_string(),
            });
        }
        None => {}
    }

    FrontmatterEntry::from_violations(file_class, violations)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::parse;

    fn profile() -> Profile {
        crate::test_support::synthetic_profile()
    }

    fn conformant_context_doc() -> &'static str {
        "---\n\
name: \"Test Doc\"\n\
description: \"A conformant test document.\"\n\
id: \"knowledge-base:test:doc\"\n\
tags:\n\
  - type:knowledge\n\
  - topic:testing\n\
  - status:complete\n\
  - privacy:internal\n\
  - owner:example\n\
links: []\n\
updated: 2026-07-11T00:00:00Z\n\
---\n\
body\n"
    }

    #[test]
    fn conformant_doc_is_valid_with_no_violations() {
        let parsed = parse::parse(conformant_context_doc()).unwrap();
        let entry = validate(&parsed, "knowledge-base/test/doc.md", &profile());
        assert!(entry.is_valid, "violations: {:?}", entry.violations);
        assert!(entry.violations.is_empty());
        assert_eq!(entry.file_class, "context");
        assert_eq!(entry.proposed_frontmatter, None);
    }

    #[test]
    fn missing_required_field_fires_in_pack_order() {
        let input = "---\nname: \"x\"\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, "some/doc.md", &profile());
        assert!(!entry.is_valid);
        let missing_fields: Vec<&str> = entry
            .violations
            .iter()
            .filter(|v| v.code == "MISSING_REQUIRED_FIELD")
            .map(|v| v.field.as_str())
            .collect();
        // Pack order: name, description, id, tags, links, updated -- name
        // is present, so the rest fire in that order.
        assert_eq!(
            missing_fields,
            vec!["description", "id", "tags", "links", "updated"]
        );
    }

    #[test]
    fn workspace_nested_required_fields_satisfy_completeness() {
        let input = concat!(
            "---\n",
            "name: \"My Skill\"\n",
            "description: \"top-level description within the skill cap.\"\n",
            "workspace:\n",
            "  id: \"skill:workspace:my-skill\"\n",
            "  links: []\n",
            "  updated: 2026-07-11T00:00:00Z\n",
            "  tags:\n",
            "    - type:skill\n",
            "    - topic:testing\n",
            "    - status:complete\n",
            "    - privacy:internal\n",
            "    - owner:example\n",
            "---\n",
            "body\n"
        );
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, ".claude/skills/my-skill/SKILL.md", &profile());
        assert!(entry.is_valid, "violations: {:?}", entry.violations);
        assert_eq!(entry.file_class, "skill");
    }

    #[test]
    fn description_nested_only_is_not_top_level() {
        let input = concat!(
            "---\n",
            "name: \"My Skill\"\n",
            "workspace:\n",
            "  description: \"nested-only description.\"\n",
            "  id: \"skill:workspace:my-skill\"\n",
            "  links: []\n",
            "  updated: 2026-07-11T00:00:00Z\n",
            "  tags:\n",
            "    - type:skill\n",
            "    - topic:testing\n",
            "    - status:complete\n",
            "    - privacy:internal\n",
            "    - owner:example\n",
            "---\n",
            "body\n"
        );
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, ".claude/skills/my-skill/SKILL.md", &profile());
        assert!(entry
            .violations
            .iter()
            .any(|v| v.code == "DESCRIPTION_NOT_TOP_LEVEL" && v.field == "description"));
    }

    #[test]
    fn description_over_cap_by_file_class_context() {
        let long_description = "x".repeat(351);
        let input = format!(
            "---\nname: \"x\"\ndescription: \"{long_description}\"\nid: \"a:b:c\"\ntags:\n  - type:knowledge\n  - topic:t\n  - status:complete\n  - privacy:internal\n  - owner:example\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n"
        );
        let parsed = parse::parse(&input).unwrap();
        let entry = validate(&parsed, "some/doc.md", &profile());
        let violation = entry
            .violations
            .iter()
            .find(|v| v.code == "DESCRIPTION_OVER_CAP")
            .expect("expected cap violation");
        assert_eq!(violation.field, "description");
        assert!(violation.message.contains("351"));
        assert!(violation.message.contains("context"));
        assert!(violation.message.contains("350"));
    }

    #[test]
    fn description_over_cap_by_file_class_skill() {
        let long_description = "x".repeat(501);
        let input = format!(
            "---\nname: \"x\"\ndescription: \"{long_description}\"\nid: \"a:b:c\"\ntags:\n  - type:skill\n  - topic:t\n  - status:complete\n  - privacy:internal\n  - owner:example\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n"
        );
        let parsed = parse::parse(&input).unwrap();
        let entry = validate(&parsed, ".claude/skills/x/SKILL.md", &profile());
        assert!(entry
            .violations
            .iter()
            .any(|v| v.code == "DESCRIPTION_OVER_CAP"));
    }

    #[test]
    fn description_over_cap_by_file_class_agent() {
        let long_description = "x".repeat(751);
        let input = format!(
            "---\nname: \"x\"\ndescription: \"{long_description}\"\nid: \"a:b:c\"\ntags:\n  - type:agent\n  - topic:t\n  - status:complete\n  - privacy:internal\n  - owner:example\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n"
        );
        let parsed = parse::parse(&input).unwrap();
        let entry = validate(&parsed, ".claude/agents/x.md", &profile());
        assert!(entry
            .violations
            .iter()
            .any(|v| v.code == "DESCRIPTION_OVER_CAP"));
    }

    #[test]
    fn tags_not_a_list_fires_when_tags_is_a_scalar() {
        let input = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags: not-a-list\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, "some/doc.md", &profile());
        assert!(entry
            .violations
            .iter()
            .any(|v| v.code == "TAGS_NOT_A_LIST" && v.field == "tags"));
    }

    #[test]
    fn singleton_missing_and_multiple_fire_in_cascade_order() {
        let input = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - status:complete\n  - status:stub\n  - topic:t\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, "some/doc.md", &profile());
        let codes: Vec<(&str, &str)> = entry
            .violations
            .iter()
            .map(|v| (v.code.as_str(), v.field.as_str()))
            .collect();
        // Singletons in pack order: type (missing), status (multiple),
        // privacy (missing), owner (missing) -- before any other cascade
        // phase.
        assert_eq!(
            codes,
            vec![
                ("MISSING_REQUIRED_TAG", "type"),
                ("MULTIPLE_SINGLE_VALUE_TAGS", "status"),
                ("MISSING_REQUIRED_TAG", "privacy"),
                ("MISSING_REQUIRED_TAG", "owner"),
            ]
        );
    }

    #[test]
    fn multiple_single_value_tags_message_uses_python_repr_list() {
        let input = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:a\n  - type:b\n  - status:complete\n  - privacy:internal\n  - owner:example\n  - topic:t\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, "some/doc.md", &profile());
        let violation = entry
            .violations
            .iter()
            .find(|v| v.code == "MULTIPLE_SINGLE_VALUE_TAGS")
            .unwrap();
        assert_eq!(
            violation.message,
            "'type:' must appear exactly once, found 2: ['type:a', 'type:b']"
        );
    }

    #[test]
    fn orphan_namespace_tag_when_feature_present_without_product_or_suite() {
        let input = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:knowledge\n  - status:complete\n  - privacy:internal\n  - owner:example\n  - topic:t\n  - feature:trace-explorer\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, "some/doc.md", &profile());
        let orphans: Vec<&str> = entry
            .violations
            .iter()
            .filter(|v| v.code == "ORPHAN_NAMESPACE_TAG")
            .map(|v| v.field.as_str())
            .collect();
        assert_eq!(
            orphans,
            vec!["feature", "feature"],
            "feature requires both product and suite, in that order"
        );
    }

    #[test]
    fn report_only_tag_misused_on_non_report_file() {
        let input = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:knowledge\n  - status:complete\n  - privacy:internal\n  - owner:example\n  - topic:t\n  - source:slack\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, "some/doc.md", &profile());
        assert!(entry
            .violations
            .iter()
            .any(|v| v.code == "REPORT_ONLY_TAG_MISUSED" && v.field == "source"));
    }

    #[test]
    fn report_file_requires_source_and_period() {
        let input = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:report\n  - status:complete\n  - privacy:internal\n  - owner:example\n  - topic:t\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, "some/report.md", &profile());
        let missing: Vec<&str> = entry
            .violations
            .iter()
            .filter(|v| {
                v.code == "MISSING_REQUIRED_TAG" && (v.field == "source" || v.field == "period")
            })
            .map(|v| v.field.as_str())
            .collect();
        assert_eq!(missing, vec!["source", "period"]);
    }

    #[test]
    fn report_file_with_bad_period_format_fires_invalid_period_format() {
        let input = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:report\n  - status:complete\n  - privacy:internal\n  - owner:example\n  - topic:t\n  - source:slack\n  - period:not-a-range\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, "some/report.md", &profile());
        let violation = entry
            .violations
            .iter()
            .find(|v| v.code == "INVALID_PERIOD_FORMAT")
            .expect("expected period violation");
        assert_eq!(violation.field, "period");
        assert_eq!(
            violation.message,
            "'period:not-a-range' is not YYYY-MM-DD/YYYY-MM-DD"
        );
    }

    #[test]
    fn report_file_with_valid_period_format_has_no_period_violation() {
        let input = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:report\n  - status:complete\n  - privacy:internal\n  - owner:example\n  - topic:t\n  - source:slack\n  - period:2026-04-01/2026-06-30\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, "some/report.md", &profile());
        assert!(entry.is_valid, "violations: {:?}", entry.violations);
    }

    #[test]
    fn report_file_with_leap_day_period_endpoint_has_no_period_violation() {
        let input = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:report\n  - status:complete\n  - privacy:internal\n  - owner:example\n  - topic:t\n  - source:slack\n  - period:2024-02-29/2024-03-01\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, "some/report.md", &profile());
        assert!(entry.is_valid, "violations: {:?}", entry.violations);
    }

    /// Shape-valid but calendar-impossible endpoints (month 13, day 45,
    /// non-leap Feb 29th) each fire `INVALID_PERIOD_FORMAT` with a message
    /// distinguishing them from a plain shape failure.
    #[test]
    fn report_file_with_calendar_invalid_period_endpoint_fires_invalid_period_format() {
        for period in [
            "period:2026-13-45/2026-14-99",
            "period:2026-02-30/2026-03-01",
            "period:2025-02-29/2025-03-01",
        ] {
            let input = format!(
                "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:report\n  - status:complete\n  - privacy:internal\n  - owner:example\n  - topic:t\n  - source:slack\n  - {period}\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n"
            );
            let parsed = parse::parse(&input).unwrap();
            let entry = validate(&parsed, "some/report.md", &profile());
            let violation = entry
                .violations
                .iter()
                .find(|v| v.code == "INVALID_PERIOD_FORMAT")
                .unwrap_or_else(|| panic!("expected a period violation for {period}"));
            assert_eq!(violation.field, "period");
            assert_eq!(
                violation.message,
                format!("'{period}' is not a real YYYY-MM-DD/YYYY-MM-DD calendar interval")
            );
        }
    }

    /// A shape-valid, calendar-valid, but inverted interval (start > end)
    /// is also `INVALID_PERIOD_FORMAT` -- real `Date` comparison catches
    /// what the shape regex can't.
    #[test]
    fn report_file_with_inverted_period_fires_invalid_period_format() {
        let input = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:report\n  - status:complete\n  - privacy:internal\n  - owner:example\n  - topic:t\n  - source:slack\n  - period:2026-12-31/2026-01-01\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, "some/report.md", &profile());
        let violation = entry
            .violations
            .iter()
            .find(|v| v.code == "INVALID_PERIOD_FORMAT")
            .expect("expected a period violation for an inverted interval");
        assert_eq!(violation.field, "period");
        assert_eq!(
            violation.message,
            "'period:2026-12-31/2026-01-01' is not a real YYYY-MM-DD/YYYY-MM-DD calendar interval"
        );
    }

    /// A signed-year endpoint fails the pack's digits-only shape regex
    /// before the calendar gate ever runs -- `time::Date::parse` would
    /// otherwise accept a signed year (`sign:automatic` on `[year]`), so
    /// `is_real_calendar_interval` never sees an unsigned-only violation
    /// like this one; it's caught at the existing shape gate instead.
    #[test]
    fn report_file_with_signed_year_period_endpoint_fires_invalid_period_format() {
        let input = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:report\n  - status:complete\n  - privacy:internal\n  - owner:example\n  - topic:t\n  - source:slack\n  - period:+2026-04-01/2026-06-30\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, "some/report.md", &profile());
        let violation = entry
            .violations
            .iter()
            .find(|v| v.code == "INVALID_PERIOD_FORMAT")
            .expect("expected a period violation for a signed-year endpoint");
        assert_eq!(violation.field, "period");
        assert_eq!(
            violation.message,
            "'period:+2026-04-01/2026-06-30' is not YYYY-MM-DD/YYYY-MM-DD"
        );
    }

    /// SDET (Time-B harden #2): a same-day interval (`start == end`) is
    /// VALID -- the gate is `start <= end`, inclusive, not a strict `<`.
    #[test]
    fn report_file_with_same_day_period_interval_has_no_period_violation() {
        let input = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:report\n  - status:complete\n  - privacy:internal\n  - owner:example\n  - topic:t\n  - source:slack\n  - period:2026-05-15/2026-05-15\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, "some/report.md", &profile());
        assert!(entry.is_valid, "violations: {:?}", entry.violations);
    }

    /// SDET (Time-B harden #1): century-rule leap years. Feb 29 exists in
    /// a year divisible by 400 (2000) but NOT in a century year that is
    /// divisible by 100 but not 400 (2100) -- a naive "divisible by 4"
    /// check would get both wrong; `time::Date` must get both right.
    #[test]
    fn report_file_with_century_leap_rule_period_endpoints() {
        let valid_400_rule =
            "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:report\n  - status:complete\n  - privacy:internal\n  - owner:example\n  - topic:t\n  - source:slack\n  - period:2000-02-29/2000-03-01\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
        let parsed = parse::parse(valid_400_rule).unwrap();
        let entry = validate(&parsed, "some/report.md", &profile());
        assert!(
            entry.is_valid,
            "2000-02-29 is a real leap day (400-rule); violations: {:?}",
            entry.violations
        );

        let invalid_100_rule = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:report\n  - status:complete\n  - privacy:internal\n  - owner:example\n  - topic:t\n  - source:slack\n  - period:2100-02-29/2100-03-01\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
        let parsed = parse::parse(invalid_100_rule).unwrap();
        let entry = validate(&parsed, "some/report.md", &profile());
        let violation = entry
            .violations
            .iter()
            .find(|v| v.code == "INVALID_PERIOD_FORMAT")
            .expect("2100-02-29 is not a real date (century non-leap); expected a violation");
        assert_eq!(violation.field, "period");
        assert_eq!(
            violation.message,
            "'period:2100-02-29/2100-03-01' is not a real YYYY-MM-DD/YYYY-MM-DD calendar interval"
        );
    }

    /// SDET (Time-B harden #1): month 0 and day 0 -- both below-range, the
    /// polar opposite of the above-range cases already covered
    /// (`report_file_with_calendar_invalid_period_endpoint_fires_invalid_period_format`).
    #[test]
    fn report_file_with_zero_month_or_zero_day_period_endpoint_fires_invalid_period_format() {
        for period in [
            "period:2026-00-05/2026-01-06",
            "period:2026-01-00/2026-01-06",
        ] {
            let input = format!(
                "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:report\n  - status:complete\n  - privacy:internal\n  - owner:example\n  - topic:t\n  - source:slack\n  - {period}\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n"
            );
            let parsed = parse::parse(&input).unwrap();
            let entry = validate(&parsed, "some/report.md", &profile());
            assert!(
                entry
                    .violations
                    .iter()
                    .any(|v| v.code == "INVALID_PERIOD_FORMAT" && v.field == "period"),
                "expected a period violation for {period}"
            );
        }
    }

    /// SDET (Time-B harden #3): the shape-gate message and the
    /// calendar-gate message are textually distinct, both under
    /// `INVALID_PERIOD_FORMAT` -- a caller can tell WHICH gate rejected a
    /// value from the message text alone.
    #[test]
    fn shape_gate_and_calendar_gate_period_messages_are_distinguishable() {
        let shape_input = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:report\n  - status:complete\n  - privacy:internal\n  - owner:example\n  - topic:t\n  - source:slack\n  - period:not-a-range\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
        let parsed = parse::parse(shape_input).unwrap();
        let entry = validate(&parsed, "some/report.md", &profile());
        let shape_message = entry
            .violations
            .iter()
            .find(|v| v.code == "INVALID_PERIOD_FORMAT")
            .expect("shape failure")
            .message
            .clone();

        let calendar_input = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:report\n  - status:complete\n  - privacy:internal\n  - owner:example\n  - topic:t\n  - source:slack\n  - period:2026-02-30/2026-03-01\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
        let parsed = parse::parse(calendar_input).unwrap();
        let entry = validate(&parsed, "some/report.md", &profile());
        let calendar_message = entry
            .violations
            .iter()
            .find(|v| v.code == "INVALID_PERIOD_FORMAT")
            .expect("calendar failure")
            .message
            .clone();

        assert_ne!(
            shape_message, calendar_message,
            "shape-gate and calendar-gate rejections must produce distinct messages"
        );
        assert!(shape_message.contains("is not YYYY-MM-DD/YYYY-MM-DD"));
        assert!(calendar_message.contains("is not a real YYYY-MM-DD/YYYY-MM-DD calendar interval"));
    }

    #[test]
    fn exempt_file_is_valid_with_no_violations_regardless_of_content() {
        let parsed = parse::parse("---\nname: \"x\"\n---\nbody\n").unwrap();
        let entry = validate(&parsed, "plan.md", &profile());
        assert!(entry.is_valid);
        assert!(entry.violations.is_empty());
    }

    #[test]
    fn exempt_path_glob_is_honored() {
        let parsed = parse::parse("---\nname: \"x\"\n---\nbody\n").unwrap();
        let entry = validate(&parsed, "work/workspace/scratchpad.md", &profile());
        assert!(entry.is_valid);
    }

    /// `NON_STRING_FRONTMATTER_KEY` is dropped (see the module-level
    /// "DROPPED" note) -- a sequence-shaped key (the check's real, narrow
    /// trigger) now produces NO violation of that code.
    #[test]
    fn non_string_top_level_key_produces_no_non_string_key_violation() {
        let input = "---\nname: \"x\"\n[a]: z\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, "some/doc.md", &profile());
        assert!(!entry
            .violations
            .iter()
            .any(|v| v.code == "NON_STRING_FRONTMATTER_KEY"));
    }

    #[test]
    fn determinism_identical_input_yields_identical_entry() {
        let parsed = parse::parse(conformant_context_doc()).unwrap();
        let profile = profile();
        let first = validate(&parsed, "knowledge-base/test/doc.md", &profile);
        let second = validate(&parsed, "knowledge-base/test/doc.md", &profile);
        assert_eq!(first, second);
    }

    #[test]
    fn embedded_core_path_validates_with_no_disk_read() {
        // The synthetic profile is built entirely from a compile-time
        // string literal -- this test asserts validate() works from that
        // profile alone, i.e. with no external schema file present at
        // runtime.
        let parsed = parse::parse(conformant_context_doc()).unwrap();
        let entry = validate(&parsed, "knowledge-base/test/doc.md", &profile());
        assert!(entry.is_valid);
    }

    // -- Data-driven proof: mutating the loaded Profile changes the
    // -- verdict with NO change to this module's code. --------------------

    /// The synthetic pack's JSON, deserialized to a mutable [`serde_json::Value`]
    /// so a test can edit the schema DATA and rebuild a [`Profile`] from the
    /// edited text via [`Profile::from_pack_json`] -- the strongest form of
    /// the "schema edit changes verdicts, no `validate.rs` edit" proof: these
    /// two tests below touch only JSON text, never this module's code.
    fn synthetic_pack_as_value() -> serde_json::Value {
        serde_json::from_str(crate::test_support::SYNTHETIC_PACK_JSON)
            .expect("synthetic pack JSON must parse")
    }

    #[test]
    fn mutating_the_loaded_description_cap_changes_the_verdict() {
        let mut pack = synthetic_pack_as_value();
        pack["description_caps"]["context"] = serde_json::json!(5);
        let profile =
            Profile::from_pack_json(&pack.to_string()).expect("edited pack must still deserialize");
        let input = "---\nname: \"x\"\ndescription: \"this is definitely over five characters\"\nid: \"a:b:c\"\ntags:\n  - type:knowledge\n  - topic:t\n  - status:complete\n  - privacy:internal\n  - owner:example\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, "some/doc.md", &profile);
        assert!(entry
            .violations
            .iter()
            .any(|v| v.code == "DESCRIPTION_OVER_CAP"));
    }

    #[test]
    fn adding_a_required_field_to_the_loaded_profile_changes_the_verdict() {
        let mut pack = synthetic_pack_as_value();
        pack["required_fields"]
            .as_array_mut()
            .expect("required_fields must be a JSON array")
            .push(serde_json::json!({ "field": "owner_email", "authorship": "human_authored" }));
        let profile =
            Profile::from_pack_json(&pack.to_string()).expect("edited pack must still deserialize");
        let parsed = parse::parse(conformant_context_doc()).unwrap();
        let entry = validate(&parsed, "knowledge-base/test/doc.md", &profile);
        assert!(!entry.is_valid);
        assert!(entry
            .violations
            .iter()
            .any(|v| v.code == "MISSING_REQUIRED_FIELD" && v.field == "owner_email"));
    }

    // -----------------------------------------------------------------------
    // Cascade order under MULTIPLE simultaneous violations.
    // -----------------------------------------------------------------------

    /// One doc that trips EVERY cascade phase at once: a singleton excess
    /// (`status`), a singleton absence (`privacy`), a parent-dependency
    /// chain with two orphans (`feature`->`suite` via `product`), and
    /// (since `type:report` is present) both a `report_required` absence
    /// (`source`) and a `report_period_format` failure (`period`).
    /// `type`/`topic` are present and clean so this isolates the phases
    /// that DO fire.
    ///
    /// Expected order: singleton(status excess) -> singleton(privacy
    /// absent) -> singleton(owner absent, sorts last among singletons
    /// because `owner` is appended last in the namespaces array) ->
    /// parent(feature->suite) -> parent(product->suite) ->
    /// `report_required(source)` -> `report_period_format(period)`.
    fn multi_violation_report_doc() -> &'static str {
        "---\n\
name: \"x\"\n\
description: \"d\"\n\
id: \"a:b:c\"\n\
tags:\n\
  - type:report\n\
  - status:complete\n\
  - status:stub\n\
  - topic:t\n\
  - feature:x\n\
  - product:y\n\
  - period:bad-format\n\
links: []\n\
updated: 2026-07-11T00:00:00Z\n\
---\n\
body\n"
    }

    #[test]
    fn cascade_order_under_multiple_simultaneous_violations_matches_live_python_plus_owner() {
        let parsed = parse::parse(multi_violation_report_doc()).unwrap();
        let entry = validate(&parsed, "some/report.md", &profile());
        let codes: Vec<(&str, &str)> = entry
            .violations
            .iter()
            .map(|v| (v.code.as_str(), v.field.as_str()))
            .collect();
        assert_eq!(
            codes,
            vec![
                ("MULTIPLE_SINGLE_VALUE_TAGS", "status"),
                ("MISSING_REQUIRED_TAG", "privacy"),
                ("MISSING_REQUIRED_TAG", "owner"), // `owner:` is a required singleton tag, enforced only in Rust
                ("ORPHAN_NAMESPACE_TAG", "feature"),
                ("ORPHAN_NAMESPACE_TAG", "product"),
                ("MISSING_REQUIRED_TAG", "source"),
                ("INVALID_PERIOD_FORMAT", "period"),
            ],
            "cascade order (minus the owner line) must match a reference Python \
             implementation's violation output on the identical tag list"
        );
    }

    /// Same fixture with the Rust-only `owner` line filtered out --
    /// asserts the REMAINING sequence is byte-for-byte a reference Python
    /// implementation's actual output, captured against this exact tags
    /// list during cross-implementation verification. This is the
    /// parity-critical assertion the frozen goldens formalize at scale.
    #[test]
    fn cascade_order_minus_owner_line_is_byte_for_byte_the_live_python_sequence() {
        let parsed = parse::parse(multi_violation_report_doc()).unwrap();
        let entry = validate(&parsed, "some/report.md", &profile());
        let without_owner: Vec<(&str, &str, &str)> = entry
            .violations
            .iter()
            .filter(|v| v.field != "owner")
            .map(|v| (v.code.as_str(), v.field.as_str(), v.message.as_str()))
            .collect();
        assert_eq!(
            without_owner,
            vec![
                (
                    "MULTIPLE_SINGLE_VALUE_TAGS",
                    "status",
                    "'status:' must appear exactly once, found 2: ['status:complete', 'status:stub']"
                ),
                (
                    "MISSING_REQUIRED_TAG",
                    "privacy",
                    "missing required 'privacy:' tag"
                ),
                (
                    "ORPHAN_NAMESPACE_TAG",
                    "feature",
                    "'feature:' tag requires a 'suite:' tag, which is missing"
                ),
                (
                    "ORPHAN_NAMESPACE_TAG",
                    "product",
                    "'product:' tag requires a 'suite:' tag, which is missing"
                ),
                (
                    "MISSING_REQUIRED_TAG",
                    "source",
                    "type:report requires a 'source:' tag, which is missing"
                ),
                (
                    "INVALID_PERIOD_FORMAT",
                    "period",
                    "'period:bad-format' is not YYYY-MM-DD/YYYY-MM-DD"
                ),
            ]
        );
    }

    // -----------------------------------------------------------------------
    // SDET: spot-parity fixtures (ran LIVE against a reference Python
    // implementation of the same validation rules). Every fixture below
    // produces the EXACT SAME violation set as that reference implementation,
    // except for the Rust-only `owner` line -- confirmed present/absent as
    // expected in each case.
    // -----------------------------------------------------------------------

    #[test]
    fn spot_parity_valid_context_doc_has_only_the_owner_divergence() {
        let input = "---\nname: Valid Context File\ndescription: \"A valid context file with all required fields properly formatted and exactly at the description cap.\"\nid: knowledge-base:tooling:valid-context\ntags:\n  - type:knowledge\n  - topic:tooling\n  - status:complete\n  - privacy:internal\nlinks:\n  - knowledge-base:claude:rules-frontmatter\nupdated: 2026-07-01T12:34:56Z\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(
            &parsed,
            "knowledge-base/tooling/valid-context.md",
            &profile(),
        );
        assert_eq!(entry.file_class, "context");
        assert_eq!(entry.violations.len(), 1, "{:?}", entry.violations);
        assert_eq!(entry.violations[0].code, "MISSING_REQUIRED_TAG");
        assert_eq!(entry.violations[0].field, "owner");
    }

    #[test]
    fn spot_parity_valid_skill_with_workspace_nesting_has_only_the_owner_divergence() {
        let input = "---\nname: Valid Skill\ndescription: \"A valid skill file with nested workspace: structure and all required fields properly formatted and exactly at the skill description cap.\"\nworkspace:\n  id: skill:workspace:valid-skill\n  tags:\n    - type:skill\n    - topic:tooling\n    - status:complete\n    - privacy:internal\n  links:\n    - knowledge-base:claude:rules-frontmatter\n  updated: 2026-07-02T08:30:00Z\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, ".claude/skills/valid-skill/SKILL.md", &profile());
        assert_eq!(entry.file_class, "skill");
        assert_eq!(entry.violations.len(), 1, "{:?}", entry.violations);
        assert_eq!(entry.violations[0].field, "owner");
    }

    #[test]
    fn spot_parity_report_with_good_period_has_only_the_owner_divergence() {
        let input = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:report\n  - status:complete\n  - privacy:internal\n  - topic:t\n  - source:slack\n  - period:2026-04-01/2026-06-30\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, "some/report.md", &profile());
        assert_eq!(entry.violations.len(), 1, "{:?}", entry.violations);
        assert_eq!(entry.violations[0].field, "owner");
    }

    // -----------------------------------------------------------------------
    // SDET: never-panic / robustness on adversarial ParsedFrontmatter input
    // (step 2). Every case below is constructed via the crate's own public
    // API (RawFields::from_ordered_pairs + FrontmatterValue variants), not
    // through parse(), so it can express shapes parse()'s YAML backend
    // would reject or never produce -- exactly the seam a future caller
    // bypassing parse() could hit.
    // -----------------------------------------------------------------------

    fn entry_from_raw(pairs: Vec<(&str, FrontmatterValue)>, rel_path: &str) -> FrontmatterEntry {
        let raw = RawFields::from_ordered_pairs(
            pairs.into_iter().map(|(k, v)| (k.to_string(), v)).collect(),
        );
        // `ParsedFrontmatter::from_raw_fields` is `pub(crate)`, reachable
        // from this in-crate test module directly -- constructing via
        // `RawFields` (never through `parse::parse`) exercises the same
        // public-API seam a future caller building `ParsedFrontmatter`
        // directly (bypassing this crate's own parser and its nesting cap)
        // could hit.
        let parsed = ParsedFrontmatter::from_raw_fields(raw, String::new());
        validate(&parsed, rel_path, &profile())
    }

    #[test]
    fn validate_on_completely_empty_raw_fields_never_panics() {
        let entry = entry_from_raw(vec![], "some/doc.md");
        assert!(!entry.is_valid);
        assert!(entry
            .violations
            .iter()
            .any(|v| v.code == "MISSING_REQUIRED_FIELD" && v.field == "name"));
    }

    #[test]
    fn validate_on_tags_as_a_mapping_never_panics_and_reports_not_a_list() {
        let tags_mapping = FrontmatterValue::Mapping(RawFields::from_ordered_pairs(vec![(
            "type".to_string(),
            FrontmatterValue::Scalar("knowledge".to_string()),
        )]));
        let entry = entry_from_raw(vec![("tags", tags_mapping)], "some/doc.md");
        assert!(entry
            .violations
            .iter()
            .any(|v| v.code == "TAGS_NOT_A_LIST" && v.field == "tags"));
    }

    #[test]
    fn validate_on_tags_as_a_scalar_never_panics_and_reports_not_a_list() {
        let entry = entry_from_raw(
            vec![("tags", FrontmatterValue::Scalar("oops".to_string()))],
            "some/doc.md",
        );
        assert!(entry.violations.iter().any(|v| v.code == "TAGS_NOT_A_LIST"));
    }

    #[test]
    fn validate_on_description_as_a_sequence_never_panics() {
        // Not a Scalar -- the cap check's `if let Some(Scalar(...))` guard
        // simply doesn't match, so no DESCRIPTION_OVER_CAP fires, but the
        // top-level-presence check still runs (description IS present at
        // top level here) and no panic occurs either way.
        let entry = entry_from_raw(
            vec![(
                "description",
                FrontmatterValue::Sequence(vec!["a".to_string(), "b".to_string()]),
            )],
            "some/doc.md",
        );
        assert!(!entry
            .violations
            .iter()
            .any(|v| v.code == "DESCRIPTION_OVER_CAP"));
        assert!(!entry
            .violations
            .iter()
            .any(|v| v.code == "DESCRIPTION_NOT_TOP_LEVEL"));
    }

    #[test]
    fn validate_on_description_as_a_mapping_never_panics() {
        let entry = entry_from_raw(
            vec![(
                "description",
                FrontmatterValue::Mapping(RawFields::from_ordered_pairs(vec![(
                    "x".to_string(),
                    FrontmatterValue::Scalar("y".to_string()),
                )])),
            )],
            "some/doc.md",
        );
        assert!(!entry.violations.iter().any(|v| v.code.is_empty())); // no panic occurred, entry produced
    }

    #[test]
    fn validate_on_workspace_as_a_scalar_never_panics_and_ignores_it() {
        // `flatten()` only descends into `workspace` when it IS a Mapping;
        // a scalar `workspace:` value is left as an ordinary (unused) top-
        // level field -- required fields must still come from top level.
        let entry = entry_from_raw(
            vec![
                ("name", FrontmatterValue::Scalar("x".to_string())),
                (
                    "workspace",
                    FrontmatterValue::Scalar("not-a-mapping".to_string()),
                ),
            ],
            "some/doc.md",
        );
        assert!(entry
            .violations
            .iter()
            .any(|v| v.code == "MISSING_REQUIRED_FIELD" && v.field == "id"));
    }

    #[test]
    fn validate_on_a_huge_tag_list_never_panics_and_completes() {
        let mut tags: Vec<String> = (0..20_000).map(|i| format!("topic:t{i}")).collect();
        tags.push("type:knowledge".to_string());
        tags.push("status:complete".to_string());
        tags.push("privacy:internal".to_string());
        tags.push("owner:example".to_string());
        let entry = entry_from_raw(
            vec![
                ("name", FrontmatterValue::Scalar("x".to_string())),
                ("description", FrontmatterValue::Scalar("d".to_string())),
                ("id", FrontmatterValue::Scalar("a:b:c".to_string())),
                ("tags", FrontmatterValue::Sequence(tags)),
                ("links", FrontmatterValue::Sequence(vec![])),
                (
                    "updated",
                    FrontmatterValue::Scalar("2026-07-11T00:00:00Z".to_string()),
                ),
            ],
            "some/doc.md",
        );
        assert!(entry.is_valid, "{:?}", entry.violations);
    }

    #[test]
    fn validate_on_a_deeply_recursively_nested_mapping_value_never_panics() {
        // Builds a 2000-level-deep single-child Mapping chain and assigns
        // it as an ordinary field's value. `validate()`/`flatten()` never
        // recurse into a field's OWN nested shape (only the single
        // `workspace:` level is descended into), so this exercises
        // construction/traversal/eventual Drop of a value this deep
        // without validate() itself ever walking it -- proving the no-
        // panic contract holds even for a shape parse()'s own 64-level
        // nesting cap (crate::parse::MAX_NESTING_DEPTH) would have rejected
        // long before validate() ever saw it.
        let mut deep = FrontmatterValue::Scalar("leaf".to_string());
        for _ in 0..2000 {
            deep = FrontmatterValue::Mapping(RawFields::from_ordered_pairs(vec![(
                "next".to_string(),
                deep,
            )]));
        }
        let entry = entry_from_raw(vec![("junk_field", deep)], "some/doc.md");
        // No panic reaching here is the assertion; content is incidental.
        assert!(!entry.is_valid);
    }

    #[test]
    fn validate_on_duplicated_top_level_keys_never_panics_and_uses_first_occurrence() {
        // RawFields::get() (crate::value) documents first-match lookup;
        // this pins that a validator built on it sees the FIRST of two
        // same-named top-level entries, not a panic or a merge.
        let entry = entry_from_raw(
            vec![
                ("name", FrontmatterValue::Scalar("first".to_string())),
                ("name", FrontmatterValue::Scalar("second".to_string())),
            ],
            "some/doc.md",
        );
        assert!(!entry
            .violations
            .iter()
            .any(|v| v.code == "MISSING_REQUIRED_FIELD" && v.field == "name"));
    }

    // -----------------------------------------------------------------------
    // NON_STRING_FRONTMATTER_KEY DROPPED (see the module-level "DROPPED"
    // note above). The check's false-positive on a legitimate
    // paren-containing key is resolved by removing the check outright, not
    // by fixing the heuristic: the tests below now assert NO such
    // violation, for both the false-positive shapes AND the heuristic's
    // former real trigger.
    // -----------------------------------------------------------------------

    #[test]
    fn legitimate_string_key_containing_a_paren_produces_no_violation() {
        let input = "---\nname: \"x\"\n\"notes (draft)\": some value\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, "some/doc.md", &profile());
        assert!(!entry
            .violations
            .iter()
            .any(|v| v.code == "NON_STRING_FRONTMATTER_KEY"));
    }

    #[test]
    fn unquoted_plain_scalar_key_containing_a_paren_produces_no_violation() {
        let input = "---\nname: \"x\"\nnotes (draft): some value\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, "some/doc.md", &profile());
        assert!(!entry
            .violations
            .iter()
            .any(|v| v.code == "NON_STRING_FRONTMATTER_KEY"));
    }

    /// A non-string-shaped key (here, a YAML sequence key, `[a, b]:`) --
    /// the dropped heuristic's former real trigger -- now produces no
    /// `NON_STRING_FRONTMATTER_KEY` violation either: it simply doesn't
    /// match any required field name or participate in namespace rules,
    /// exactly Python's behavior for the same shape.
    #[test]
    fn a_sequence_shaped_key_produces_no_non_string_key_violation() {
        let input = "---\nname: \"x\"\n[a, b]: z\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, "some/doc.md", &profile());
        assert!(!entry
            .violations
            .iter()
            .any(|v| v.code == "NON_STRING_FRONTMATTER_KEY"));
    }

    // -----------------------------------------------------------------------
    // SDET: data-driven property extended beyond the SE's two tests (step 7).
    // -----------------------------------------------------------------------

    #[test]
    fn mutating_a_singleton_namespace_to_optional_removes_its_violation() {
        let mut pack = synthetic_pack_as_value();
        for ns in pack["namespaces"].as_array_mut().unwrap() {
            if ns["name"] == "privacy" {
                ns["cardinality"] = serde_json::json!("optional");
            }
        }
        let profile =
            Profile::from_pack_json(&pack.to_string()).expect("edited pack must still deserialize");
        // conformant_context_doc has no privacy: substitute -- reuse a doc
        // missing privacy on purpose.
        let input = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:knowledge\n  - topic:t\n  - status:complete\n  - owner:example\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, "some/doc.md", &profile);
        assert!(
            !entry.violations.iter().any(|v| v.field == "privacy"),
            "privacy demoted to optional in the schema must drop its violation \
             with no validate.rs change: {:?}",
            entry.violations
        );
    }

    #[test]
    fn mutating_rule_set_require_namespaces_changes_the_verdict() {
        let mut pack = synthetic_pack_as_value();
        pack["rule_sets"][0]["apply"]["require_namespaces"] =
            serde_json::json!(["source", "period", "audience"]);
        let profile =
            Profile::from_pack_json(&pack.to_string()).expect("edited pack must still deserialize");
        let input = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:report\n  - status:complete\n  - privacy:internal\n  - owner:example\n  - topic:t\n  - source:slack\n  - period:2026-04-01/2026-06-30\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, "some/report.md", &profile);
        assert!(
            entry
                .violations
                .iter()
                .any(|v| v.code == "MISSING_REQUIRED_TAG" && v.field == "audience"),
            "adding 'audience' to the rule-set's require_namespaces in the schema \
             must surface a new MISSING_REQUIRED_TAG with no validate.rs change: {:?}",
            entry.violations
        );
    }

    // -----------------------------------------------------------------------
    // SDET: file_class first-match-wins + exempt dir_components (step 8).
    // -----------------------------------------------------------------------

    #[test]
    fn file_class_first_matching_rule_wins_over_a_later_matching_rule() {
        let mut pack = synthetic_pack_as_value();
        // Insert a broad `*.md` -> "agent" rule BEFORE the existing skill
        // rules -- if classify_file_class is truly first-match-wins, this
        // new rule (now first) decides, even though a later rule would
        // also match.
        pack["file_class"]["rules"].as_array_mut().unwrap().insert(
            0,
            serde_json::json!({ "class": "agent", "match": { "glob": "*.md" } }),
        );
        let profile =
            Profile::from_pack_json(&pack.to_string()).expect("edited pack must still deserialize");
        let parsed = parse::parse("---\nname: \"x\"\n---\nbody\n").unwrap();
        let entry = validate(&parsed, ".claude/skills/x/SKILL.md", &profile);
        assert_eq!(
            entry.file_class, "agent",
            "the inserted first-position rule must win over the later, also-\
             matching skill rule"
        );
    }

    /// The broad `**/SKILL.md` fallback rule (index 1) must still
    /// classify a `SKILL.md` that does NOT sit under `.claude/skills/` --
    /// i.e. the anchored rule (index 0) does not match, but the
    /// array-order fallback does (fnmatch parity: `**` spans `/`).
    #[test]
    fn skill_md_outside_dot_claude_skills_still_classifies_via_the_broad_fallback_rule() {
        let parsed = parse::parse("---\nname: \"x\"\n---\nbody\n").unwrap();
        let entry = validate(&parsed, "tools/SKILL.md", &profile());
        assert_eq!(entry.file_class, "skill");
    }

    /// The anchored skill rule's leading `*` must cross `/` (fnmatch
    /// parity, `literal_separator=false`) so a `SKILL.md` several
    /// directories deep still matches the anchored rule, not just the
    /// top-level `.claude/skills/*/SKILL.md` shape.
    #[test]
    fn anchored_skill_glob_leading_star_crosses_multiple_path_separators() {
        let parsed = parse::parse("---\nname: \"x\"\n---\nbody\n").unwrap();
        let entry = validate(
            &parsed,
            "some/deep/prefix/.claude/skills/foo/SKILL.md",
            &profile(),
        );
        assert_eq!(entry.file_class, "skill");
    }

    /// The `exempt.path_globs` entry `work/projects/*/findings/*.md`
    /// -- fnmatch parity means each `*` crosses `/`, so a `findings/` doc
    /// nested MORE than one level below `projects/` (extra path segments
    /// on either side of the two `*`s) still matches, not just the
    /// single-segment shape the glob's literal text suggests.
    #[test]
    fn exempt_findings_glob_matches_extra_nested_path_segments_too() {
        let parsed = parse::parse("---\nname: \"x\"\n---\nbody\n").unwrap();
        let entry = validate(
            &parsed,
            "work/projects/a/b/findings/sub/x.md",
            &profile(),
        );
        assert!(entry.is_valid, "{:?}", entry.violations);
    }

    #[test]
    fn exempt_dir_component_anywhere_in_the_path_is_honored() {
        let parsed = parse::parse("---\nname: \"x\"\n---\nbody\n").unwrap();
        let entry = validate(&parsed, "some/nested/__pycache__/x.md", &profile());
        assert!(entry.is_valid, "{:?}", entry.violations);
    }

    #[test]
    fn exempt_filename_match_is_honored_regardless_of_directory() {
        let parsed = parse::parse("---\nname: \"x\"\n---\nbody\n").unwrap();
        let entry = validate(&parsed, "deeply/nested/path/CLAUDE.md", &profile());
        assert!(entry.is_valid, "{:?}", entry.violations);
    }

    // -----------------------------------------------------------------------
    // SDET: determinism on a NON-conformant (multi-violation) doc, not just
    // the happy path (step 9).
    // -----------------------------------------------------------------------

    #[test]
    fn determinism_holds_for_a_multi_violation_entry_too() {
        let parsed = parse::parse(multi_violation_report_doc()).unwrap();
        let profile = profile();
        let first = validate(&parsed, "some/report.md", &profile);
        let second = validate(&parsed, "some/report.md", &profile);
        let third = validate(&parsed, "some/report.md", &profile);
        assert_eq!(first, second);
        assert_eq!(second, third);
    }

    // -----------------------------------------------------------------------
    // Under core@2.0.0, the report axis is one match-then-apply rule-set;
    // the mechanism itself is not report-shaped. These tests use the
    // SHIPPED default@1.0.0 + reports@1.0.0 (report-semantics parity) and a
    // synthetic NON-report rule-set (proving the mechanism is generic).
    // -----------------------------------------------------------------------

    /// Merged profile from the two shipped public packs under core@2.0.0.
    fn default_plus_reports() -> Profile {
        let core = crate::profile::embedded_core_json();
        let default_json = crate::profile::embedded_pack_json("default@1.0.0").unwrap();
        let reports_json = crate::profile::embedded_pack_json("reports@1.0.0").unwrap();
        Profile::from_packs(core, &[default_json, reports_json])
            .expect("default@1.0.0 + reports@1.0.0 must merge under core@2.0.0")
            .0
    }

    #[test]
    fn shipped_default_and_reports_reproduce_pre_split_report_semantics_both_directions() {
        let profile = default_plus_reports();

        // Non-match direction: a report namespace on a NON-report file is a
        // REPORT_ONLY_TAG_MISUSED, via forbidden_unless_matched.
        let non_report = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:knowledge\n  - status:complete\n  - topic:t\n  - source:slack\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
        let parsed = parse::parse(non_report).unwrap();
        let entry = validate(&parsed, "some/doc.md", &profile);
        assert!(
            entry
                .violations
                .iter()
                .any(|v| v.code == "REPORT_ONLY_TAG_MISUSED" && v.field == "source"),
            "a report-only namespace off a report file must still be misuse: {:?}",
            entry.violations
        );

        // Match direction: a report file missing source/period fires
        // MISSING_REQUIRED_TAG for each (require_namespaces on match).
        let report = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:report\n  - status:complete\n  - topic:t\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
        let parsed = parse::parse(report).unwrap();
        let entry = validate(&parsed, "some/report.md", &profile);
        let missing: Vec<&str> = entry
            .violations
            .iter()
            .filter(|v| {
                v.code == "MISSING_REQUIRED_TAG" && (v.field == "source" || v.field == "period")
            })
            .map(|v| v.field.as_str())
            .collect();
        assert_eq!(
            missing,
            vec!["source", "period"],
            "a report file must still require source+period: {:?}",
            entry.violations
        );
    }

    /// A fixture pack adding a NON-report `{match, apply}` rule-set
    /// changes lint verdicts with NO change to this module's code -- proof
    /// the mechanism is generic, not report-shaped. The rule-set matches
    /// `type:policy` and forbids `owner` off a policy file / requires
    /// `status` on one.
    #[test]
    fn a_non_report_rule_set_changes_lint_verdicts_with_no_code_change() {
        const WITH_POLICY_RULE_SET: &str = r#"{
            "kind": "extension-pack",
            "version": "policy@1",
            "extends": "core@2.0.0",
            "required_fields": [],
            "description_caps": {"context": 350},
            "file_class": {"default": "context", "rules": []},
            "namespaces": [
                {"name": "type", "cardinality": "singleton"},
                {"name": "owner", "cardinality": "optional"},
                {"name": "status", "cardinality": "optional"}
            ],
            "rule_sets": [
                {
                    "match": {"namespace": "type", "value": "policy"},
                    "apply": {
                        "require_namespaces": ["status"],
                        "forbidden_unless_matched": ["owner"]
                    }
                }
            ],
            "exempt": {"filenames": [], "dir_components": [], "path_globs": []}
        }"#;
        let profile = Profile::from_pack_json(WITH_POLICY_RULE_SET)
            .expect("a synthetic non-report rule-set pack must build");

        // Non-policy file carrying `owner` -> forbidden_unless_matched fires.
        let non_policy =
            "---\nname: \"x\"\ntags:\n  - type:knowledge\n  - owner:example\n---\nbody\n";
        let parsed = parse::parse(non_policy).unwrap();
        let entry = validate(&parsed, "some/doc.md", &profile);
        assert!(
            entry
                .violations
                .iter()
                .any(|v| v.code == "REPORT_ONLY_TAG_MISUSED" && v.field == "owner"),
            "a non-report rule-set's forbidden_unless_matched must fire generically: {:?}",
            entry.violations
        );

        // Policy file missing `status` -> require_namespaces fires.
        let policy = "---\nname: \"x\"\ntags:\n  - type:policy\n---\nbody\n";
        let parsed = parse::parse(policy).unwrap();
        let entry = validate(&parsed, "some/doc.md", &profile);
        assert!(
            entry
                .violations
                .iter()
                .any(|v| v.code == "MISSING_REQUIRED_TAG" && v.field == "status"),
            "a non-report rule-set's require_namespaces must fire generically: {:?}",
            entry.violations
        );
    }
}
