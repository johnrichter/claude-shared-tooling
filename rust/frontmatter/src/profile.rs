//! The declarative frontmatter schema, deserialized -- core profile
//! (mechanisms) + extension pack (concrete vocabulary), see
//! `schemas/frontmatter/README.md` for the split rationale. [`validate::validate`]
//! is a generic interpreter over [`Profile`]: every rule it enforces (which
//! fields are required, how namespaces cascade, what a description cap is,
//! which paths are exempt) is a value read off this struct, not a Rust
//! constant -- so a schema edit changes verdicts with no change to
//! `validate.rs` (see the `data_driven` test module at the bottom of this
//! file).
//!
//! # Construction paths
//! - [`Profile::bundled_psa_apm`] -- embedded core (`core@1`) + embedded
//!   psa-apm pack (`psa-apm@1`), both via `include_str!`. Zero filesystem
//!   reads; this is what the crate's own tests use, and what a psa-apm
//!   consumer uses by default.
//! - [`Profile::from_pack_json`] -- embedded core + a caller-supplied pack
//!   JSON string. This is the path a foreign repo takes: it ships its own
//!   pack (its own required fields, namespaces, caps, ...) but adopts
//!   `core@1` unchanged. Resolving *which* pack a foreign repo should load
//!   (a `navigator.toml` sentinel) is a later task (M3.P2.T1); this
//!   function only needs the pack's JSON text, already read by the caller.
//! - [`Profile::from_json`] -- both core and pack supplied as JSON text.
//!   The fully general constructor the two above wrap.

use globset::{Glob, GlobSet, GlobSetBuilder};
use regex::Regex;
use serde::Deserialize;
use std::collections::HashMap;
use std::fmt;

const EMBEDDED_CORE_JSON: &str =
    include_str!("../../../schemas/frontmatter/frontmatter-core.schema.json");
const EMBEDDED_PSA_APM_PACK_JSON: &str =
    include_str!("../../../schemas/frontmatter/frontmatter-psa-apm.pack.json");

/// Why a [`Profile`] could not be built from supplied JSON text.
///
/// Malformed schema JSON is a caller-facing error, never a panic -- a
/// broken profile (bad JSON, a pack whose `period.regex` does not compile
/// as a `regex::Regex`, a `file_class`/`exempt` glob `globset` rejects) is
/// something a caller can report and act on.
#[derive(Debug)]
pub enum ProfileError {
    /// The core profile JSON did not deserialize into `Core` (this crate's
    /// private core-profile struct).
    InvalidCore(serde_json::Error),
    /// The extension pack JSON did not deserialize into `Pack` (this
    /// crate's private extension-pack struct).
    InvalidPack(serde_json::Error),
    /// The pack's `report.period.regex` is not a `regex`-crate-compilable
    /// pattern.
    InvalidPeriodRegex(String, regex::Error),
    /// One of the pack's `file_class` rule globs, or one of its
    /// `exempt.path_globs`, is not a `globset`-compilable glob.
    InvalidGlob(String, globset::Error),
}

impl fmt::Display for ProfileError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::InvalidCore(err) => write!(f, "core profile JSON is invalid: {err}"),
            Self::InvalidPack(err) => write!(f, "extension pack JSON is invalid: {err}"),
            Self::InvalidPeriodRegex(pattern, err) => {
                write!(f, "pack's period.regex '{pattern}' is invalid: {err}")
            }
            Self::InvalidGlob(glob, err) => {
                write!(f, "pack's glob '{glob}' is invalid: {err}")
            }
        }
    }
}

impl std::error::Error for ProfileError {}

/// The resolved schema a call to [`crate::validate::validate`] (below)
/// interprets:
/// the core profile's message templates plus the extension pack's concrete
/// vocabulary. See the module doc for construction paths.
#[derive(Debug, Clone)]
pub struct Profile {
    pub(crate) core: Core,
    pub(crate) pack: Pack,
    /// The pack's `report.period.regex`, compiled once at construction
    /// time (never per-file/per-match), so a malformed pattern is a
    /// construction-time [`ProfileError`], not a per-match failure deep in
    /// `validate`. See the pack file's `report.period.regex`/`notes` for
    /// why the shipped pattern uses `[0-9]`, not `\d`.
    pub(crate) period_pattern: Regex,
    /// The pack's `file_class.rules` globs and `exempt.path_globs`,
    /// compiled once at construction time -- see [`CompiledGlobs`].
    pub(crate) globs: CompiledGlobs,
}

impl Profile {
    /// Builds a `Profile` from the embedded core (`core@1`) and the
    /// embedded psa-apm pack (`psa-apm@1`) -- no filesystem access.
    ///
    /// # Panics
    /// Never: both embedded JSON strings are this crate's own committed
    /// schema files, validated by this crate's own tests -- an error here
    /// would mean the checked-in schema itself is broken, which the test
    /// suite (`profile::tests::bundled_psa_apm_profile_builds`) catches at
    /// build time, not at a caller's runtime.
    #[must_use]
    pub fn bundled_psa_apm() -> Self {
        Self::from_json(EMBEDDED_CORE_JSON, EMBEDDED_PSA_APM_PACK_JSON)
            .expect("bundled core + psa-apm pack JSON must deserialize; see profile::tests")
    }

    /// Builds a `Profile` from the embedded core (`core@1`) plus a
    /// caller-supplied extension pack's JSON text -- the path a foreign
    /// repo takes when it ships its own vocabulary but adopts the core
    /// mechanisms unchanged.
    ///
    /// # Errors
    /// [`ProfileError::InvalidPack`] if `pack_json` does not deserialize
    /// into `Pack`; [`ProfileError::InvalidPeriodRegex`] if the pack's
    /// `report.period.regex` does not compile as a `regex::Regex`;
    /// [`ProfileError::InvalidGlob`] if any `file_class` rule glob or
    /// `exempt.path_globs` entry does not compile as a `globset` glob.
    pub fn from_pack_json(pack_json: &str) -> Result<Self, ProfileError> {
        Self::from_json(EMBEDDED_CORE_JSON, pack_json)
    }

    /// Builds a `Profile` from caller-supplied core and pack JSON text.
    ///
    /// # Errors
    /// [`ProfileError::InvalidCore`] / [`ProfileError::InvalidPack`] on
    /// malformed JSON or a shape that doesn't match `Core`/`Pack`;
    /// [`ProfileError::InvalidPeriodRegex`] if the pack's `report.period.regex`
    /// does not compile as a `regex::Regex`; [`ProfileError::InvalidGlob`]
    /// if any `file_class` rule glob or `exempt.path_globs` entry does not
    /// compile as a `globset` glob.
    pub fn from_json(core_json: &str, pack_json: &str) -> Result<Self, ProfileError> {
        let core: Core = serde_json::from_str(core_json).map_err(ProfileError::InvalidCore)?;
        let pack: Pack = serde_json::from_str(pack_json).map_err(ProfileError::InvalidPack)?;
        let period_pattern = Regex::new(&pack.report.period.regex).map_err(|err| {
            ProfileError::InvalidPeriodRegex(pack.report.period.regex.clone(), err)
        })?;
        let globs = CompiledGlobs::compile(&pack)?;
        Ok(Self {
            core,
            pack,
            period_pattern,
            globs,
        })
    }
}

// ---------------------------------------------------------------------------
// Core profile (mechanisms + message templates)
// ---------------------------------------------------------------------------

/// The core profile's message templates for the two `independent_checks`
/// this crate emits (`required_fields`, `description_caps`) -- see
/// `frontmatter-core.schema.json`'s `mechanisms` object. The tag-cascade
/// message templates are not separately modeled: `violation_cascade`'s
/// `emit[].message` strings use the same placeholder grammar and are
/// rendered by [`mod@crate::validate`]'s cascade phases directly off this
/// struct's `violation_cascade`, so both sets of wording live in one place
/// -- the deserialized JSON -- and neither is duplicated as a Rust literal.
#[derive(Debug, Clone, Deserialize)]
pub(crate) struct Core {
    pub(crate) mechanisms: CoreMechanisms,
    pub(crate) violation_cascade: Vec<CascadeStep>,
}

#[derive(Debug, Clone, Deserialize)]
pub(crate) struct CoreMechanisms {
    pub(crate) required_fields: MessageTemplate,
    pub(crate) description_caps: MessageTemplate,
}

#[derive(Debug, Clone, Deserialize)]
pub(crate) struct MessageTemplate {
    pub(crate) message: String,
}

/// One step of the core's `violation_cascade`. `iterate`/`guard` are
/// human-readable descriptions in the schema (not machine grammar); this
/// crate's `validate` module encodes the per-step iteration logic itself
/// (over the pack's ordered vocabulary), and reads `step` (to identify
/// which step it's rendering for) and `emit` (the message templates) off
/// this struct -- so changing a template's wording changes emitted text
/// with no `validate.rs` edit, even though the step *kind* is fixed.
#[derive(Debug, Clone, Deserialize)]
pub(crate) struct CascadeStep {
    pub(crate) step: String,
    pub(crate) emit: Vec<CascadeEmit>,
}

#[derive(Debug, Clone, Deserialize)]
pub(crate) struct CascadeEmit {
    pub(crate) code: String,
    pub(crate) message: String,
}

impl Core {
    /// Looks up a cascade step by its `step` name (e.g.
    /// `"singleton_cardinality"`). Returns `None` only if the loaded core
    /// profile omits a step `validate` needs -- a malformed/partial core,
    /// handled by the caller as a construction-time concern (bundled core
    /// is tested to always have every step; a foreign core is the
    /// caller's responsibility, per the crate's schema-loading contract).
    pub(crate) fn cascade_step(&self, step: &str) -> Option<&CascadeStep> {
        self.violation_cascade.iter().find(|s| s.step == step)
    }
}

impl CascadeStep {
    /// The `emit` entry at `index` (cascade steps declare their `emit`
    /// templates in a fixed, small, position-significant order -- e.g.
    /// singleton's index 0 is the "absent" template, index 1 is "excess";
    /// see `frontmatter-core.schema.json`). Returns `None` if the loaded
    /// core lacks that emit entry.
    pub(crate) fn emit(&self, index: usize) -> Option<&CascadeEmit> {
        self.emit.get(index)
    }
}

// ---------------------------------------------------------------------------
// Extension pack (concrete vocabulary)
// ---------------------------------------------------------------------------

/// The extension pack: the concrete required fields, description caps,
/// namespace vocabulary, report rules, file-class globs, and exempt
/// taxonomy a workspace instantiates `core@1` with. See
/// `frontmatter-psa-apm.pack.json` for the psa-apm values and their
/// `source` provenance.
#[derive(Debug, Clone, Deserialize)]
pub(crate) struct Pack {
    pub(crate) required_fields: Vec<RequiredField>,
    pub(crate) description_caps: DescriptionCaps,
    pub(crate) file_class: FileClassRules,
    pub(crate) namespaces: Vec<NamespaceSpec>,
    pub(crate) report: ReportSpec,
    pub(crate) exempt: ExemptSpec,
}

#[derive(Debug, Clone, Deserialize)]
pub(crate) struct RequiredField {
    pub(crate) field: String,
    // `authorship`/`source` are read by the pack for provenance and by the
    // future Tier-1 fixer (M4.P3.T2); `validate` itself only needs `field`.
    #[allow(dead_code)]
    pub(crate) authorship: String,
}

/// `file_class -> max description length`. Deserialized from a JSON object
/// that also carries a non-numeric `source` provenance string alongside
/// the numeric class entries (see the pack file) -- [`DescriptionCaps::get`]
/// is the only accessor, so that shape detail stays contained here.
#[derive(Debug, Clone, Deserialize)]
pub(crate) struct DescriptionCaps(HashMap<String, serde_json::Value>);

impl DescriptionCaps {
    /// The cap for `file_class`, or `None` if the pack has no cap for that
    /// class (see `frontmatter-core.schema.json`'s `description_caps`
    /// mechanism: an absent class is uncapped).
    pub(crate) fn get(&self, file_class: &str) -> Option<u64> {
        self.0.get(file_class).and_then(serde_json::Value::as_u64)
    }
}

#[derive(Debug, Clone, Deserialize)]
pub(crate) struct FileClassRules {
    pub(crate) default: String,
    pub(crate) rules: Vec<FileClassRule>,
}

#[derive(Debug, Clone, Deserialize)]
pub(crate) struct FileClassRule {
    pub(crate) class: String,
    #[serde(rename = "match")]
    pub(crate) matcher: FileClassMatch,
}

#[derive(Debug, Clone, Deserialize)]
pub(crate) struct FileClassMatch {
    pub(crate) glob: String,
}

/// One namespace's cascade participation: its cardinality plus the two
/// optional axes (`parents`, `report_only`) that add extra cascade phases
/// for that namespace. Array order in the pack (this struct's source
/// position in `Pack::namespaces`) IS iteration order -- see
/// `namespaces_order_note` in the pack file.
#[derive(Debug, Clone, Deserialize)]
pub(crate) struct NamespaceSpec {
    pub(crate) name: String,
    pub(crate) cardinality: Cardinality,
    #[serde(default)]
    pub(crate) parents: Vec<String>,
    #[serde(default)]
    pub(crate) report_only: bool,
    /// This facet's value type -- will drive range-eligibility for a
    /// downstream query consumer (facetquery): `String` supports
    /// equality/wildcard only, `Date`/`Numeric` are also
    /// range/comparison-eligible. Parsed here for that future consumer and
    /// for the future merge task; no public accessor exists yet (`field`
    /// and `NamespaceSpec` are `pub(crate)`) and this crate's own
    /// `validate` does not read it (see module doc) -- hence
    /// `#[allow(dead_code)]`. A pack that omits `type` gets
    /// `FacetType::String` via `#[serde(default)]`.
    #[allow(dead_code)]
    #[serde(rename = "type", default)]
    pub(crate) facet_type: FacetType,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum Cardinality {
    Singleton,
    AtLeastOne,
    Optional,
}

/// A namespace's value type, per the pack's optional per-namespace `type`
/// (see `frontmatter-psa-apm.pack.json`'s `namespaces[].type` and the
/// meta-schema's `namespaces.items.properties.type`). `String` is the
/// default for a pack that omits `type` -- every namespace predates this
/// field and must keep deserializing unchanged.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum FacetType {
    /// Equality/wildcard-eligible only. The default for a pack that omits
    /// `type`.
    #[default]
    String,
    /// Range/comparison-eligible (e.g. `period`).
    Date,
    /// Range/comparison-eligible.
    Numeric,
}

#[derive(Debug, Clone, Deserialize)]
pub(crate) struct ReportSpec {
    pub(crate) trigger: ReportTrigger,
    pub(crate) required_namespaces: Vec<String>,
    pub(crate) period: ReportPeriod,
}

#[derive(Debug, Clone, Deserialize)]
pub(crate) struct ReportTrigger {
    pub(crate) namespace: String,
    pub(crate) value: String,
}

#[derive(Debug, Clone, Deserialize)]
pub(crate) struct ReportPeriod {
    pub(crate) namespace: String,
    pub(crate) regex: String,
}

#[derive(Debug, Clone, Deserialize)]
pub(crate) struct ExemptSpec {
    pub(crate) filenames: Vec<String>,
    pub(crate) dir_components: Vec<String>,
    pub(crate) path_globs: Vec<String>,
}

// ---------------------------------------------------------------------------
// Compiled globs (file_class rules + exempt path_globs)
// ---------------------------------------------------------------------------

/// The pack's `file_class.rules` globs and `exempt.path_globs`, each
/// compiled once into a `globset::GlobSet` at [`Profile`] construction
/// (never per-file) -- determinism + perf, and a malformed glob in the
/// schema is a construction-time [`ProfileError`], not a per-file mismatch
/// deep in `validate`. `literal_separator` is left at `globset`'s default
/// (`false`), so `*` crosses `/` -- matching shell `fnmatch` glob
/// semantics, the behavior the pack's globs are written against.
#[derive(Debug, Clone)]
pub(crate) struct CompiledGlobs {
    file_class: GlobSet,
    exempt_path: GlobSet,
}

impl CompiledGlobs {
    fn compile(pack: &Pack) -> Result<Self, ProfileError> {
        let mut file_class_builder = GlobSetBuilder::new();
        for rule in &pack.file_class.rules {
            let glob = Glob::new(&rule.matcher.glob)
                .map_err(|err| ProfileError::InvalidGlob(rule.matcher.glob.clone(), err))?;
            file_class_builder.add(glob);
        }
        let file_class = file_class_builder
            .build()
            .map_err(|err| ProfileError::InvalidGlob("file_class.rules".to_string(), err))?;

        let mut exempt_builder = GlobSetBuilder::new();
        for pattern in &pack.exempt.path_globs {
            let glob = Glob::new(pattern)
                .map_err(|err| ProfileError::InvalidGlob(pattern.clone(), err))?;
            exempt_builder.add(glob);
        }
        let exempt_path = exempt_builder
            .build()
            .map_err(|err| ProfileError::InvalidGlob("exempt.path_globs".to_string(), err))?;

        Ok(Self {
            file_class,
            exempt_path,
        })
    }

    /// The lowest `file_class.rules` index matching `rel_path`, or `None`
    /// if no rule matches -- `GlobSet::matches` returns every matching
    /// index, so this picks the minimum itself to preserve first-match-wins
    /// (pack array order), the same semantics the prior hand-rolled matcher
    /// gave.
    pub(crate) fn file_class_rule_index(&self, rel_path: &str) -> Option<usize> {
        self.file_class.matches(rel_path).into_iter().min()
    }

    /// True if `rel_path` matches any `exempt.path_globs` entry.
    pub(crate) fn exempt_path_matches(&self, rel_path: &str) -> bool {
        self.exempt_path.is_match(rel_path)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn bundled_psa_apm_profile_builds() {
        let _profile = Profile::bundled_psa_apm();
    }

    #[test]
    fn from_pack_json_accepts_the_bundled_pack_text_too() {
        let profile = Profile::from_pack_json(EMBEDDED_PSA_APM_PACK_JSON)
            .expect("bundled pack JSON must be a valid pack");
        assert_eq!(profile.pack.required_fields.len(), 6);
    }

    #[test]
    fn invalid_pack_json_is_a_typed_error_not_a_panic() {
        let result = Profile::from_pack_json("{ not json");
        assert!(matches!(result, Err(ProfileError::InvalidPack(_))));
    }

    // -- SDET: never-panic on malformed/empty/wrong-shape profile JSON -----

    #[test]
    fn empty_pack_json_is_a_typed_error_not_a_panic() {
        let result = Profile::from_pack_json("");
        assert!(matches!(result, Err(ProfileError::InvalidPack(_))));
    }

    #[test]
    fn truncated_pack_json_is_a_typed_error_not_a_panic() {
        // Bundled pack text cut off mid-object -- a realistic "partial
        // write"/"truncated download" shape, not just a syntax typo.
        let truncated = &EMBEDDED_PSA_APM_PACK_JSON[..EMBEDDED_PSA_APM_PACK_JSON.len() / 2];
        let result = Profile::from_pack_json(truncated);
        assert!(matches!(result, Err(ProfileError::InvalidPack(_))));
    }

    #[test]
    fn pack_json_missing_a_required_key_is_a_typed_error_not_a_panic() {
        // Valid JSON, valid object shape, but missing `report` entirely --
        // `Pack`'s fields are not `#[serde(default)]`, so this must fail
        // deserialization rather than construct a half-populated `Pack`.
        let result = Profile::from_pack_json(
            r#"{"required_fields":[],"description_caps":{},"file_class":{"default":"context","rules":[]},"namespaces":[],"exempt":{"filenames":[],"dir_components":[],"path_globs":[]}}"#,
        );
        assert!(matches!(result, Err(ProfileError::InvalidPack(_))));
    }

    #[test]
    fn pack_json_with_wrong_field_type_is_a_typed_error_not_a_panic() {
        // `required_fields` must be an array of objects; a bare string is
        // the wrong shape entirely.
        let mut pack = bundled_pack_as_value_standalone();
        pack["required_fields"] = serde_json::json!("not-an-array");
        let result = Profile::from_pack_json(&pack.to_string());
        assert!(matches!(result, Err(ProfileError::InvalidPack(_))));
    }

    #[test]
    fn wrong_shape_top_level_pack_json_is_a_typed_error_not_a_panic() {
        // A JSON array, not an object -- Pack expects a map.
        let result = Profile::from_pack_json("[1, 2, 3]");
        assert!(matches!(result, Err(ProfileError::InvalidPack(_))));
    }

    #[test]
    fn malformed_core_json_is_a_typed_error_not_a_panic() {
        let result = Profile::from_json("{ not json", EMBEDDED_PSA_APM_PACK_JSON);
        assert!(matches!(result, Err(ProfileError::InvalidCore(_))));
    }

    /// M2.P2.T1b finalization: swapping the hand-rolled restricted subset
    /// for a real `regex::Regex` means "unsupported pattern feature" is no
    /// longer a concept -- any valid regex compiles, including a character
    /// class. So this now proves the genuinely-new failure mode: a pattern
    /// `regex` itself rejects (unbalanced group), not a feature the prior
    /// hand-rolled subset happened not to support. EXPECTED-VALUE CHANGE
    /// from the pre-swap test of the same name: the prior assertion (a
    /// character-class pattern is rejected) is now FALSE -- `[0-9]{4}` is
    /// ordinary, fully-supported regex syntax -- so it is retired rather
    /// than pinned as a regression.
    #[test]
    fn invalid_period_regex_is_a_typed_error_not_a_panic() {
        let mut pack = bundled_pack_as_value_standalone();
        pack["report"]["period"]["regex"] = serde_json::json!(r"^(unbalanced"); // real regex syntax error
        let result = Profile::from_pack_json(&pack.to_string());
        assert!(matches!(
            result,
            Err(ProfileError::InvalidPeriodRegex(_, _))
        ));
    }

    #[test]
    fn a_character_class_period_regex_now_compiles_cleanly() {
        // The prior hand-rolled subset rejected any character class as
        // "unsupported"; the real `regex` crate has no such restriction.
        let mut pack = bundled_pack_as_value_standalone();
        pack["report"]["period"]["regex"] = serde_json::json!(r"^[0-9]{4}$");
        let result = Profile::from_pack_json(&pack.to_string());
        assert!(result.is_ok());
    }

    #[test]
    fn invalid_file_class_glob_is_a_typed_error_not_a_panic() {
        let mut pack = bundled_pack_as_value_standalone();
        pack["file_class"]["rules"][0]["match"]["glob"] = serde_json::json!("[unterminated");
        let result = Profile::from_pack_json(&pack.to_string());
        assert!(matches!(result, Err(ProfileError::InvalidGlob(_, _))));
    }

    #[test]
    fn invalid_exempt_path_glob_is_a_typed_error_not_a_panic() {
        let mut pack = bundled_pack_as_value_standalone();
        pack["exempt"]["path_globs"] = serde_json::json!(["[unterminated"]);
        let result = Profile::from_pack_json(&pack.to_string());
        assert!(matches!(result, Err(ProfileError::InvalidGlob(_, _))));
    }

    fn bundled_pack_as_value_standalone() -> serde_json::Value {
        serde_json::from_str(EMBEDDED_PSA_APM_PACK_JSON).expect("bundled pack JSON must parse")
    }

    // -- SDET: shipped period regex vs Python PERIOD_RE semantics ----------

    fn psa_apm_pattern() -> Regex {
        Profile::bundled_psa_apm().period_pattern
    }

    #[test]
    fn period_pattern_matches_the_canonical_shape() {
        assert!(psa_apm_pattern().is_match("2026-04-01..2026-06-30"));
    }

    #[test]
    fn period_pattern_rejects_tricky_non_matches() {
        let p = psa_apm_pattern();
        // Extra/missing digits.
        assert!(!p.is_match("2026-4-01..2026-06-30"), "short month");
        assert!(
            !p.is_match("2026-04-01..2026-06-300"),
            "extra trailing digit"
        );
        assert!(
            !p.is_match("02026-04-01..2026-06-30"),
            "extra leading digit"
        );
        // Missing/wrong separator.
        assert!(!p.is_match("2026-04-01.2026-06-30"), "single dot");
        assert!(!p.is_match("2026-04-01...2026-06-30"), "triple dot");
        // Leading/trailing junk (implicit `^`/`$` anchoring).
        assert!(!p.is_match(" 2026-04-01..2026-06-30"), "leading space");
        assert!(!p.is_match("2026-04-01..2026-06-30 "), "trailing space");
        assert!(!p.is_match("x2026-04-01..2026-06-30"), "leading junk char");
        assert!(!p.is_match(""), "empty string");
    }

    /// RESOLVED (was a TE Unicode-digit escalation, M2.P2.T1b finalization):
    /// Python's `re` module matches `\d` against any Unicode decimal-digit
    /// codepoint by default (no `re.ASCII` flag on `schema.py`'s
    /// `PERIOD_RE`), so a period value built from Arabic-Indic digits
    /// (`٢٠٢٦-٠٤-٠١..٢٠٢٦-٠٦-٣٠`) matches Python's regex. The shipped pack
    /// regex deliberately uses `[0-9]`, not `\d` -- a character class is
    /// ASCII-only regardless of the `regex` crate's Unicode mode, so the
    /// same string does NOT match here. This is a DELIBERATE, documented
    /// choice (see the pack file's `report.period.regex`/`notes`), not an
    /// open gap: every real `period:` value in the corpus is ASCII, so this
    /// is strictly more correct than Python's un-flagged `\d`, not a
    /// functional regression.
    #[test]
    fn shipped_period_regex_rejects_unicode_digits_that_pythons_d_would_accept() {
        let p = psa_apm_pattern();
        assert!(
            !p.is_match("\u{0662}\u{0660}\u{0662}\u{0666}-04-01..2026-06-30"),
            "the shipped ASCII-only `[0-9]` pattern must reject Arabic-Indic \
             digits, even though Python's default (non-ASCII-flagged) \\d \
             would accept them -- deliberate, documented divergence"
        );
    }

    // -- facet type (`namespaces[].type`) -----------------------------------

    fn namespace<'a>(profile: &'a Profile, name: &str) -> &'a NamespaceSpec {
        profile
            .pack
            .namespaces
            .iter()
            .find(|n| n.name == name)
            .expect("namespace must exist in the bundled pack")
    }

    #[test]
    fn bundled_period_namespace_is_facet_type_date() {
        let profile = Profile::bundled_psa_apm();
        assert_eq!(namespace(&profile, "period").facet_type, FacetType::Date);
    }

    #[test]
    fn a_namespace_omitting_type_defaults_to_facet_type_string() {
        let profile = Profile::bundled_psa_apm();
        // `topic` carries no `type` in the pack file.
        assert_eq!(namespace(&profile, "topic").facet_type, FacetType::String);
    }

    #[test]
    fn an_unknown_facet_type_value_is_a_typed_error_not_a_panic() {
        let mut pack = bundled_pack_as_value_standalone();
        pack["namespaces"][0]["type"] = serde_json::json!("not-a-real-type");
        let result = Profile::from_pack_json(&pack.to_string());
        assert!(matches!(result, Err(ProfileError::InvalidPack(_))));
    }

    #[test]
    fn bundled_psa_apm_has_the_expected_per_namespace_facet_types() {
        // Additive check: `period` is the only namespace this task types as
        // non-`String` -- every other namespace keeps the implicit default.
        let profile = Profile::bundled_psa_apm();
        for spec in &profile.pack.namespaces {
            let expected = if spec.name == "period" {
                FacetType::Date
            } else {
                FacetType::String
            };
            assert_eq!(
                spec.facet_type, expected,
                "namespace '{}' has unexpected facet type",
                spec.name
            );
        }
    }

    // -- SDET: meta-schema conformance (SE-flagged automation gap) --------
    //
    // `frontmatter-profile.meta.schema.json` is not wired into this crate's
    // load/validate path (that's the later merge task), and pulling in a
    // full JSON-Schema-2020-12 validator crate just to assert this in
    // isolation would be a heavy dep for one test. So this section pins
    // what IS mechanically checkable today without one:
    //   1. the meta-schema's own `removes` pattern and `namespaces[].type`
    //      enum, read live off the checked-in meta-schema JSON (not
    //      hand-copied), behave as documented;
    //   2. the bundled core/pack JSON satisfy the meta-schema's top-level
    //      `required` shape for their `kind`;
    //   3. `type`'s serde enum enforcement is the one piece of the pack
    //      shape actually enforced by this crate today (see the
    //      `an_unknown_facet_type_value_is_a_typed_error_not_a_panic` test
    //      above) -- `removes` is NOT yet enforced by this crate (Pack
    //      doesn't deserialize it at all, so a malformed entry is silently
    //      ignored); that gap closes with the merge task that wires
    //      `removes` semantics in, not here.
    const META_SCHEMA_JSON: &str =
        include_str!("../../../schemas/frontmatter/frontmatter-profile.meta.schema.json");

    fn meta_schema() -> serde_json::Value {
        serde_json::from_str(META_SCHEMA_JSON).expect("meta-schema JSON must parse")
    }

    fn removes_pattern() -> Regex {
        let schema = meta_schema();
        let pattern = schema["$defs"]["extensionPack"]["properties"]["removes"]["items"]["pattern"]
            .as_str()
            .expect("meta-schema must declare removes.items.pattern");
        Regex::new(pattern).expect("removes.items.pattern must itself be a valid regex")
    }

    #[test]
    fn meta_schema_removes_pattern_accepts_every_documented_typed_reference_kind() {
        let pattern = removes_pattern();
        for kind in [
            "namespace",
            "required_field",
            "description_cap",
            "file_class_rule",
            "report_required",
            "exempt_filename",
            "exempt_dir",
            "exempt_glob",
        ] {
            let reference = format!("{kind}:team");
            assert!(
                pattern.is_match(&reference),
                "removes pattern must accept '{reference}'"
            );
        }
    }

    #[test]
    fn meta_schema_removes_pattern_rejects_an_unknown_kind_or_a_missing_target() {
        let pattern = removes_pattern();
        assert!(
            !pattern.is_match("bogus_kind:team"),
            "an unrecognized typed-reference kind must not match"
        );
        assert!(
            !pattern.is_match("namespace"),
            "a bare kind with no ':target' must not match"
        );
        assert!(
            !pattern.is_match("namespace:"),
            "an empty target after the colon must not match (pattern requires '.+' after ':')"
        );
    }

    #[test]
    fn meta_schema_namespaces_type_enum_matches_the_facet_type_variants() {
        // Cross-check the meta-schema's `namespaces[].type` enum against
        // this crate's `FacetType` variants (via their serde
        // `rename_all = "snake_case"` spellings) -- if either side gains a
        // variant/enum value without the other, this test catches the drift
        // instead of it surfacing as a silent-ignore in production.
        let schema = meta_schema();
        let enum_values: Vec<&str> = schema["$defs"]["extensionPack"]["properties"]["namespaces"]
            ["items"]["properties"]["type"]["enum"]
            .as_array()
            .expect("meta-schema must declare namespaces[].type.enum")
            .iter()
            .map(|v| v.as_str().expect("enum entries must be strings"))
            .collect();
        assert_eq!(
            enum_values,
            vec!["string", "date", "numeric"],
            "meta-schema's type enum must match FacetType's String/Date/Numeric variants"
        );
    }

    #[test]
    fn bundled_core_and_pack_json_satisfy_the_meta_schemas_top_level_kind_discriminant() {
        // Lightweight structural stand-in for full meta-schema validation:
        // the meta-schema's top-level `oneOf` discriminates core-profile vs
        // extension-pack purely on `kind`, and both are in the meta-schema's
        // required top-level key list -- pin that the two bundled files each
        // declare the `kind` their own struct expects to embed successfully.
        let core: serde_json::Value =
            serde_json::from_str(EMBEDDED_CORE_JSON).expect("bundled core JSON must parse");
        assert_eq!(core["kind"], "core-profile");

        let pack: serde_json::Value =
            serde_json::from_str(EMBEDDED_PSA_APM_PACK_JSON).expect("bundled pack JSON must parse");
        assert_eq!(pack["kind"], "extension-pack");
    }

    #[test]
    fn a_malformed_removes_entry_is_not_yet_rejected_by_this_crate_pins_the_open_gap() {
        // Documents, rather than closes, the SE-flagged gap: `Pack` does not
        // model `removes` at all (no field on the struct), so serde simply
        // ignores it -- a value that violates the meta-schema's
        // `removes.items.pattern` deserializes without error today. Full
        // enforcement (via meta-schema validation or a typed `removes`
        // field) is the later merge task's job; this test exists so that
        // task's own regression suite has a documented "was broken, now
        // fixed" pin instead of discovering the gap by surprise.
        let mut pack = bundled_pack_as_value_standalone();
        pack["removes"] = serde_json::json!(["not-a-valid-typed-reference"]);
        let result = Profile::from_pack_json(&pack.to_string());
        assert!(
            result.is_ok(),
            "current behavior: a malformed `removes` entry is silently ignored, not rejected \
             (see this test's doc comment) -- if this now fails, the merge task has wired \
             `removes` enforcement in and this test should be deleted, not fixed to pass"
        );
    }
}
