//! `M2.P2.T1b` -- the sole frontmatter validator: interprets a [`Profile`]
//! (the declarative schema, `crate::profile`) over one file's already-parsed
//! [`ParsedFrontmatter`] (`crate::parse`) and emits a [`FrontmatterEntry`]
//! verdict. This module re-ports `audit_helper/schema.py` +
//! `audit_helper/frontmatter.py`'s `validate()` (the workspace's current
//! Python emitter) as a generic schema interpreter: every fact this module
//! checks (which fields are required, how tag namespaces cascade, what a
//! description cap is, which paths are exempt) is read off `Profile`, not
//! hardcoded here -- see `crate::profile`'s module doc and this module's
//! `data_driven` test for the "schema edit, no code edit" property the
//! build plan requires.
//!
//! # Scope (this task)
//! Faithful port of the LIVE Python emitter's violation codes, fields,
//! message text, and emission ORDER for the checks the declarative schema
//! and task spec cover: `MISSING_REQUIRED_FIELD`, `DESCRIPTION_OVER_CAP`,
//! `DESCRIPTION_NOT_TOP_LEVEL`, the five tag-namespace codes
//! (`MISSING_REQUIRED_TAG`, `MULTIPLE_SINGLE_VALUE_TAGS`,
//! `ORPHAN_NAMESPACE_TAG`, `REPORT_ONLY_TAG_MISUSED`,
//! `INVALID_PERIOD_FORMAT`), and the tags-not-a-list code. Deliberately
//! out of scope (see the task's violation-code enumeration and
//! `DIVERGENCES.md`): `DESCRIPTION_INVALID_SCALAR` (YAML scalar-quoting
//! style -- this crate's parser already normalizes scalar shape, so the
//! raw-text quoting style Python's check inspects doesn't exist here in
//! the same form), `INVALID_UPDATED_FORMAT` (timestamp format), and
//! `MISSING_FRONTMATTER`/`MALFORMED_FRONTMATTER` (raw-text-level concerns
//! `crate::parse::parse`'s `Result` already reports -- see
//! [`ScanOutcome`]/[`fold`] below for how a caller folds that into the
//! coverage rollup instead).
//!
//! `proposed_frontmatter` on [`FrontmatterEntry`] stays `None` in this
//! task -- the Tier-1 fixer that fills it is `M4.P3.T2`'s `fix`; it is
//! modeled as an `Option` now so the shape is stable across that task.

use crate::profile::{Cardinality, Profile};
use crate::value::{FrontmatterValue, RawFields};
use crate::ParsedFrontmatter;

/// One frontmatter schema violation: a stable `code`, the `field` it's
/// about, and a human-readable `message`. Field order and shape mirror the
/// Python emitter's `Violation` `NamedTuple` (`audit_helper/frontmatter.py`)
/// so a consumer mapping this into another representation (e.g. the CLI's
/// human-readable output) needs no extra lookup.
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
/// frontmatter block (`M4.P3.T2`; always `None` from this task's
/// `validate`).
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
    /// Reserved for `M4.P3.T2`'s Tier-1 fixer. Always `None` from this
    /// task's `validate`.
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
/// directory walk that produces the per-file [`ScanOutcome`]s is
/// `M4.P3.T1`'s `lint` -- this crate only provides the shape and the
/// per-outcome [`fold`] step.
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
// New (non-Python) violation code: non-string top-level frontmatter keys.
// ---------------------------------------------------------------------------

/// Not a Python-parity code -- `audit_helper` never encounters this case
/// because `PyYAML` keeps a non-string mapping key as its native Python
/// type (a real `int`/`bool`/etc key), while this crate's parser
/// (`crate::parse::yaml_key_to_string`) stringifies EVERY key, falling back
/// to a `{key:?}` Rust-Debug rendering for a key that was itself a YAML
/// sequence or mapping (never a legitimate frontmatter key). That
/// fallback rendering is recognizable -- Rust's derived `Debug` for a
/// compound value always looks like `Variant(...)`, containing `(`, which
/// no legitimate plain frontmatter key does -- so this flags it rather
/// than silently treating it as an ordinary field name (the QR carry-over
/// concern this task calls out). A key that stringified from a YAML
/// scalar int/bool (e.g. `123:`, `true:`) is NOT flagged: it renders as
/// plain digits/letters, indistinguishable from a hand-typed string key,
/// and Python has the same blind spot for those (a `123` int key would
/// simply never match any known field name and be ignored).
const CODE_NON_STRING_KEY: &str = "NON_STRING_FRONTMATTER_KEY";

/// Not a Python-parity code -- see [`CODE_NON_STRING_KEY`]. Python's
/// `tags` not-a-list check is `TAGS_NOT_A_LIST`
/// (`audit_helper/frontmatter.py`'s `validate()`, tags branch); pinned
/// here as a named constant (not inline in the cascade) purely for
/// discoverability, not because it's a new invention.
const CODE_TAGS_NOT_A_LIST: &str = "TAGS_NOT_A_LIST";

/// Not part of the declarative schema's `codes` list (see
/// `DIVERGENCES.md`) because it isn't in `schema.py` -- it's LIVE in
/// `audit_helper/frontmatter.py`'s `validate()` (the `raw_value is None`
/// branch). Pinned as a named constant for the same reason as
/// [`CODE_TAGS_NOT_A_LIST`].
const CODE_DESCRIPTION_NOT_TOP_LEVEL: &str = "DESCRIPTION_NOT_TOP_LEVEL";

// ---------------------------------------------------------------------------
// The effective (workspace:-flattened) field view
// ---------------------------------------------------------------------------

/// A flattened view of a document's fields: every top-level field, with any
/// same-named field nested under a top-level `workspace:` mapping
/// OVERRIDING the top-level one -- re-porting `frontmatter.py`'s
/// `_flatten()` verbatim (Skills/Agents/Rules nest their
/// id/tags/links/updated/description under `workspace:`; validation always
/// runs against this merged view so one rule set covers both styles).
struct Effective<'a> {
    pairs: Vec<(String, &'a FrontmatterValue)>,
}

impl<'a> Effective<'a> {
    fn get(&self, key: &str) -> Option<&'a FrontmatterValue> {
        self.pairs.iter().find(|(k, _)| k == key).map(|(_, v)| *v)
    }
}

fn flatten(raw: &RawFields) -> Effective<'_> {
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

/// True if `value` counts as absent for a required field -- re-porting
/// `frontmatter.py`'s `_is_missing()`: an outright-absent key (`None`
/// here) or a blank/whitespace-only scalar string is missing; a present
/// (even empty) sequence or mapping is not. [`FrontmatterValue::Other`]
/// (a YAML explicit null, `key:` with nothing after it) is treated as
/// missing -- `PyYAML` resolves that same YAML shape to Python `None`, which
/// `_is_missing(None)` already treats as missing, so this is the same
/// fact expressed through this crate's typed value instead of a dynamic
/// `is None` check.
fn is_missing_value(value: Option<&FrontmatterValue>) -> bool {
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
/// glob wins (array order), else `default` -- re-porting the pack's
/// documented classification logic (`frontmatter-psa-apm.pack.json`'s
/// `file_class.note`).
fn classify_file_class(rel_path: &str, profile: &Profile) -> String {
    for rule in &profile.pack.file_class.rules {
        if glob_match(&rule.matcher.glob, rel_path) {
            return rule.class.clone();
        }
    }
    profile.pack.file_class.default.clone()
}

/// `fnmatch`-equivalent glob match: `*` matches any sequence of characters
/// (including none, and including `/` -- this crate's globs are NOT
/// path-segment-aware, matching Python `fnmatch.fnmatch`'s semantics
/// exactly, per the pack's own `note` fields), `?` matches exactly one
/// character, every other character matches itself literally. Character
/// classes (`[...]`) are NOT supported -- no glob in the current core or
/// psa-apm pack uses one; a pack that needs one would need this function
/// extended, not worked around.
fn glob_match(pattern: &str, text: &str) -> bool {
    let p: Vec<char> = pattern.chars().collect();
    let t: Vec<char> = text.chars().collect();
    let (mut pi, mut ti) = (0usize, 0usize);
    let mut star: Option<(usize, usize)> = None; // (pattern index after '*', text index tried)
    while ti < t.len() {
        if pi < p.len() && (p[pi] == '?' || p[pi] == t[ti]) {
            pi += 1;
            ti += 1;
        } else if pi < p.len() && p[pi] == '*' {
            star = Some((pi + 1, ti));
            pi += 1;
        } else if let Some((star_pi, star_ti)) = star {
            pi = star_pi;
            ti = star_ti + 1;
            star = Some((star_pi, ti));
        } else {
            return false;
        }
    }
    while pi < p.len() && p[pi] == '*' {
        pi += 1;
    }
    pi == p.len()
}

// ---------------------------------------------------------------------------
// Exempt taxonomy
// ---------------------------------------------------------------------------

/// True if `rel_path` is exempt from the completeness/tag-cascade checks --
/// re-porting `schema.py`'s `is_doc_exempt()` verbatim against the pack's
/// `exempt` vocabulary: basename in `filenames`, any path component in
/// `dir_components`, or the POSIX path matches a `path_globs` entry.
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
    exempt.path_globs.iter().any(|g| glob_match(g, rel_path))
}

// ---------------------------------------------------------------------------
// Message-template rendering
// ---------------------------------------------------------------------------

/// Substitutes `{key}` placeholders in `template` with their string value,
/// verbatim -- the byte-identical-rendering contract `schemas/frontmatter/README.md`
/// specifies for both interpreters.
fn render(template: &str, subs: &[(&str, &str)]) -> String {
    let mut out = template.to_string();
    for (key, value) in subs {
        out = out.replace(&format!("{{{key}}}"), value);
    }
    out
}

/// Renders a list of tag strings the way Python's f-string `{namespaces[ns]}`
/// does -- `repr()` of a `list[str]`: `['type:a', 'type:b']`. Single-quoted
/// unless a value contains a `'` and no `"`, in which case double-quoted;
/// backslashes and the delimiter quote are escaped. Real tag values are
/// plain `namespace:value` ASCII with neither quote character, so this
/// covers every case this crate's own callers can produce; it does not
/// reproduce Python's control-character (`\n`/`\t`/...) escaping, which no
/// legitimate tag value contains either.
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
// Tag-namespace cascade (re-ports schema.py's tag_rule_violations verbatim,
// driven by the pack's ordered `namespaces` array instead of schema.py's
// constant tuples/dict).
// ---------------------------------------------------------------------------

/// Groups tag strings by namespace (the part before the first `:`),
/// preserving each namespace's tags in source order -- re-porting
/// `schema.py`'s `tag_namespaces()`. A tag with no `:` is dropped (cannot
/// participate in any namespace rule); every value this crate's parser
/// puts in a `tags:` sequence is already a `String` (see `crate::value`),
/// so the Python `isinstance(tag, str)` half of that filter is vacuous
/// here -- only the `":" in tag` half does any work.
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

/// Re-ports `schema.py`'s `tag_rule_violations()`: the full ordered
/// cascade -- singleton, at-least-one, parent-dependency, report-only
/// misuse, report-required, period format -- driven entirely by
/// `profile.pack.namespaces`' array order and `profile.pack.report`, with
/// message wording read from `profile.core`'s cascade step templates.
/// Delegates each cascade step to its own phase function (below), purely to
/// keep this entry point short -- the ORDER these phases run in, not their
/// internal size, is what the parity contract pins.
fn tag_rule_violations(tags: &[String], profile: &Profile) -> Vec<Violation> {
    let mut violations = Vec::new();
    let groups = group_by_namespace(tags);

    singleton_cardinality_phase(&groups, profile, &mut violations);
    at_least_one_cardinality_phase(&groups, profile, &mut violations);
    parent_dependency_phase(&groups, profile, &mut violations);

    let trigger = &profile.pack.report.trigger;
    let is_report = group_get(&groups, &trigger.namespace)
        .iter()
        .any(|t| t.split_once(':').map(|(_, v)| v) == Some(trigger.value.as_str()));

    report_only_misuse_phase(&groups, profile, is_report, &mut violations);
    if is_report {
        report_required_phase(&groups, profile, &mut violations);
        report_period_format_phase(&groups, profile, &mut violations);
    }

    violations
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

fn report_only_misuse_phase(
    groups: &[(String, Vec<String>)],
    profile: &Profile,
    is_report: bool,
    violations: &mut Vec<Violation>,
) {
    let Some(step) = profile.core.cascade_step("report_only_misuse") else {
        return;
    };
    for ns in profile.pack.namespaces.iter().filter(|n| n.report_only) {
        if !group_get(groups, &ns.name).is_empty() && !is_report {
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

fn report_required_phase(
    groups: &[(String, Vec<String>)],
    profile: &Profile,
    violations: &mut Vec<Violation>,
) {
    let Some(step) = profile.core.cascade_step("report_required") else {
        return;
    };
    for ns in &profile.pack.report.required_namespaces {
        if group_get(groups, ns).is_empty() {
            if let Some(emit) = step.emit(0) {
                violations.push(Violation {
                    code: emit.code.clone(),
                    field: ns.clone(),
                    message: render(&emit.message, &[("namespace", ns)]),
                });
            }
        }
    }
}

fn report_period_format_phase(
    groups: &[(String, Vec<String>)],
    profile: &Profile,
    violations: &mut Vec<Violation>,
) {
    let Some(step) = profile.core.cascade_step("report_period_format") else {
        return;
    };
    let period_ns = &profile.pack.report.period.namespace;
    for period_tag in group_get(groups, period_ns) {
        let value = period_tag.split_once(':').map_or("", |(_, v)| v);
        if !profile.period_pattern.is_match(value) {
            if let Some(emit) = step.emit(0) {
                violations.push(Violation {
                    code: emit.code.clone(),
                    field: period_ns.clone(),
                    message: render(
                        &emit.message,
                        &[("period_namespace", period_ns), ("period_tag", period_tag)],
                    ),
                });
            }
        }
    }
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

    // New (non-Python) check: reject a top-level key this crate's parser
    // could only have produced via its debug-stringify fallback -- see
    // `CODE_NON_STRING_KEY`'s doc comment for why this, and only this,
    // subset of "not a string key" is detectable at this layer.
    for (key, _) in parsed.raw_fields.iter() {
        if key.contains('(') {
            violations.push(Violation {
                code: CODE_NON_STRING_KEY.to_string(),
                field: key.to_string(),
                message: format!(
                    "frontmatter key {key:?} did not come from a plain YAML string/scalar"
                ),
            });
        }
    }

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
    // description being present at all (re-porting frontmatter.py's
    // `if not _is_missing(description):` guard).
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
        Profile::bundled_psa_apm()
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
  - owner:datadog\n\
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
            "    - owner:datadog\n",
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
            "    - owner:datadog\n",
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
            "---\nname: \"x\"\ndescription: \"{long_description}\"\nid: \"a:b:c\"\ntags:\n  - type:knowledge\n  - topic:t\n  - status:complete\n  - privacy:internal\n  - owner:datadog\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n"
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
            "---\nname: \"x\"\ndescription: \"{long_description}\"\nid: \"a:b:c\"\ntags:\n  - type:skill\n  - topic:t\n  - status:complete\n  - privacy:internal\n  - owner:datadog\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n"
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
            "---\nname: \"x\"\ndescription: \"{long_description}\"\nid: \"a:b:c\"\ntags:\n  - type:agent\n  - topic:t\n  - status:complete\n  - privacy:internal\n  - owner:datadog\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n"
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
        let input = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:a\n  - type:b\n  - status:complete\n  - privacy:internal\n  - owner:datadog\n  - topic:t\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
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
        let input = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:knowledge\n  - status:complete\n  - privacy:internal\n  - owner:datadog\n  - topic:t\n  - feature:trace-explorer\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
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
        let input = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:knowledge\n  - status:complete\n  - privacy:internal\n  - owner:datadog\n  - topic:t\n  - source:slack\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, "some/doc.md", &profile());
        assert!(entry
            .violations
            .iter()
            .any(|v| v.code == "REPORT_ONLY_TAG_MISUSED" && v.field == "source"));
    }

    #[test]
    fn report_file_requires_source_and_period() {
        let input = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:report\n  - status:complete\n  - privacy:internal\n  - owner:datadog\n  - topic:t\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
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
        let input = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:report\n  - status:complete\n  - privacy:internal\n  - owner:datadog\n  - topic:t\n  - source:slack\n  - period:not-a-range\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
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
            "'period:not-a-range' is not YYYY-MM-DD..YYYY-MM-DD"
        );
    }

    #[test]
    fn report_file_with_valid_period_format_has_no_period_violation() {
        let input = "---\nname: \"x\"\ndescription: \"d\"\nid: \"a:b:c\"\ntags:\n  - type:report\n  - status:complete\n  - privacy:internal\n  - owner:datadog\n  - topic:t\n  - source:slack\n  - period:2026-04-01..2026-06-30\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, "some/report.md", &profile());
        assert!(entry.is_valid, "violations: {:?}", entry.violations);
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
        let entry = validate(&parsed, "the-work/workspace/scratchpad.md", &profile());
        assert!(entry.is_valid);
    }

    #[test]
    fn non_string_top_level_key_is_flagged() {
        let input = "---\nname: \"x\"\n[a]: z\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, "some/doc.md", &profile());
        assert!(entry
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
    fn bundled_core_path_validates_with_no_disk_read() {
        // `Profile::bundled_psa_apm` embeds both JSON files at compile
        // time -- this test asserts validate() works from that profile
        // alone, i.e. with no external schema file present at runtime.
        let parsed = parse::parse(conformant_context_doc()).unwrap();
        let entry = validate(
            &parsed,
            "knowledge-base/test/doc.md",
            &Profile::bundled_psa_apm(),
        );
        assert!(entry.is_valid);
    }

    // -- Data-driven proof: mutating the loaded Profile changes the
    // -- verdict with NO change to this module's code. --------------------

    /// The bundled pack's JSON, deserialized to a mutable [`serde_json::Value`]
    /// so a test can edit the schema DATA and rebuild a [`Profile`] from the
    /// edited text via [`Profile::from_pack_json`] -- the strongest form of
    /// the "schema edit changes verdicts, no `validate.rs` edit" proof: these
    /// two tests below touch only JSON text, never this module's code.
    fn bundled_pack_as_value() -> serde_json::Value {
        serde_json::from_str(include_str!(
            "../../../schemas/frontmatter/frontmatter-psa-apm.pack.json"
        ))
        .expect("bundled pack JSON must parse")
    }

    #[test]
    fn mutating_the_loaded_description_cap_changes_the_verdict() {
        let mut pack = bundled_pack_as_value();
        pack["description_caps"]["context"] = serde_json::json!(5);
        let profile =
            Profile::from_pack_json(&pack.to_string()).expect("edited pack must still deserialize");
        let input = "---\nname: \"x\"\ndescription: \"this is definitely over five characters\"\nid: \"a:b:c\"\ntags:\n  - type:knowledge\n  - topic:t\n  - status:complete\n  - privacy:internal\n  - owner:datadog\nlinks: []\nupdated: 2026-07-11T00:00:00Z\n---\nbody\n";
        let parsed = parse::parse(input).unwrap();
        let entry = validate(&parsed, "some/doc.md", &profile);
        assert!(entry
            .violations
            .iter()
            .any(|v| v.code == "DESCRIPTION_OVER_CAP"));
    }

    #[test]
    fn adding_a_required_field_to_the_loaded_profile_changes_the_verdict() {
        let mut pack = bundled_pack_as_value();
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
}
