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
/// broken profile (bad JSON, a pack whose `period.regex` this crate's
/// restricted regex subset cannot compile) is something a caller can
/// report and act on.
#[derive(Debug)]
pub enum ProfileError {
    /// The core profile JSON did not deserialize into `Core` (this crate's
    /// private core-profile struct).
    InvalidCore(serde_json::Error),
    /// The extension pack JSON did not deserialize into `Pack` (this
    /// crate's private extension-pack struct).
    InvalidPack(serde_json::Error),
    /// The pack's `report.period.regex` uses a pattern feature outside the
    /// restricted subset `PeriodPattern` (this module) supports (anchors
    /// `^`/`$`, `\d{N}` digit-repeats, `\.` literal dots, other literal
    /// characters).
    UnsupportedPeriodRegex(String),
}

impl fmt::Display for ProfileError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::InvalidCore(err) => write!(f, "core profile JSON is invalid: {err}"),
            Self::InvalidPack(err) => write!(f, "extension pack JSON is invalid: {err}"),
            Self::UnsupportedPeriodRegex(pattern) => {
                write!(
                    f,
                    "pack's period.regex '{pattern}' uses an unsupported pattern feature"
                )
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
    /// The pack's `report.period.regex`, pre-compiled into this crate's
    /// restricted pattern subset once at construction time (never
    /// per-file), so a malformed pattern is a construction-time
    /// [`ProfileError`], not a per-match failure deep in `validate`.
    pub(crate) period_pattern: PeriodPattern,
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
    /// into `Pack`; [`ProfileError::UnsupportedPeriodRegex`] if the pack's
    /// period regex is outside this crate's supported subset.
    pub fn from_pack_json(pack_json: &str) -> Result<Self, ProfileError> {
        Self::from_json(EMBEDDED_CORE_JSON, pack_json)
    }

    /// Builds a `Profile` from caller-supplied core and pack JSON text.
    ///
    /// # Errors
    /// [`ProfileError::InvalidCore`] / [`ProfileError::InvalidPack`] on
    /// malformed JSON or a shape that doesn't match `Core`/`Pack`;
    /// [`ProfileError::UnsupportedPeriodRegex`] if the pack's period regex
    /// is outside this crate's supported subset (see `PeriodPattern::compile`,
    /// below).
    pub fn from_json(core_json: &str, pack_json: &str) -> Result<Self, ProfileError> {
        let core: Core = serde_json::from_str(core_json).map_err(ProfileError::InvalidCore)?;
        let pack: Pack = serde_json::from_str(pack_json).map_err(ProfileError::InvalidPack)?;
        let period_pattern =
            PeriodPattern::compile(&pack.report.period.regex).ok_or_else(|| {
                ProfileError::UnsupportedPeriodRegex(pack.report.period.regex.clone())
            })?;
        Ok(Self {
            core,
            pack,
            period_pattern,
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
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum Cardinality {
    Singleton,
    AtLeastOne,
    Optional,
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
// Restricted period-regex subset
// ---------------------------------------------------------------------------

/// One compiled token of this crate's restricted regex subset -- just
/// enough grammar to interpret a pack's `report.period.regex` as DATA
/// (per the build-plan's "no validator-code edit" requirement) without
/// pulling in a full regex engine as a new dependency. Supports exactly
/// the shapes a calendar-range-style pattern needs: start/end anchors,
/// `\d{N}` digit-repeats, `\.` literal dots, and other literal characters.
/// Anything else (character classes, alternation, unanchored patterns, ...)
/// is rejected at `PeriodPattern::compile` time as [`ProfileError::UnsupportedPeriodRegex`]
/// rather than silently mismatched.
#[derive(Debug, Clone, PartialEq, Eq)]
enum PeriodTok {
    DigitRepeat(usize),
    Literal(char),
}

/// A compiled `report.period.regex`, produced once at [`Profile`] construction.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct PeriodPattern(Vec<PeriodTok>);

impl PeriodPattern {
    /// Compiles `pattern` if it is exactly `^`, then a sequence of
    /// `\d{N}` / `\.` / literal-character tokens, then `$` -- the shape
    /// every psa-apm-style calendar-range period regex takes. Returns
    /// `None` for anything outside that subset (unanchored, character
    /// classes, alternation, unescaped metacharacters, ...).
    fn compile(pattern: &str) -> Option<Self> {
        let inner = pattern.strip_prefix('^')?.strip_suffix('$')?;
        let mut toks = Vec::new();
        let chars: Vec<char> = inner.chars().collect();
        let mut i = 0;
        while i < chars.len() {
            match chars[i] {
                '\\' => {
                    let esc = *chars.get(i + 1)?;
                    match esc {
                        'd' => {
                            // Expect `{N}` immediately following `\d`.
                            if chars.get(i + 2) != Some(&'{') {
                                return None;
                            }
                            let close = chars[i + 3..].iter().position(|c| *c == '}')? + i + 3;
                            let digits: String = chars[i + 3..close].iter().collect();
                            let n: usize = digits.parse().ok()?;
                            toks.push(PeriodTok::DigitRepeat(n));
                            i = close + 1;
                        }
                        '.' => {
                            toks.push(PeriodTok::Literal('.'));
                            i += 2;
                        }
                        _ => return None, // unsupported escape
                    }
                }
                // Any other regex metacharacter in this position is outside
                // the supported subset -- reject rather than mismatch.
                '*' | '+' | '?' | '[' | ']' | '(' | ')' | '|' | '^' | '$' | '.' => return None,
                c => {
                    toks.push(PeriodTok::Literal(c));
                    i += 1;
                }
            }
        }
        Some(Self(toks))
    }

    /// True if `value` matches this compiled pattern in full (implicitly
    /// anchored both ends, matching the source regex's `^...$`).
    pub(crate) fn is_match(&self, value: &str) -> bool {
        let chars: Vec<char> = value.chars().collect();
        let mut pos = 0;
        for tok in &self.0 {
            match tok {
                PeriodTok::Literal(expected) => {
                    if chars.get(pos) != Some(expected) {
                        return false;
                    }
                    pos += 1;
                }
                PeriodTok::DigitRepeat(n) => {
                    for _ in 0..*n {
                        if !chars.get(pos).is_some_and(char::is_ascii_digit) {
                            return false;
                        }
                        pos += 1;
                    }
                }
            }
        }
        pos == chars.len()
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

    #[test]
    fn period_pattern_compiles_and_matches_the_psa_apm_regex() {
        let pattern = PeriodPattern::compile(r"^\d{4}-\d{2}-\d{2}\.\.\d{4}-\d{2}-\d{2}$").unwrap();
        assert!(pattern.is_match("2026-04-01..2026-06-30"));
        assert!(!pattern.is_match("2026-04-01.2026-06-30"));
        assert!(!pattern.is_match("2026-4-1..2026-6-30"));
        assert!(!pattern.is_match("2026-04-01..2026-06-30 "));
    }

    #[test]
    fn period_pattern_rejects_unsupported_subset() {
        assert!(PeriodPattern::compile(r"\d{4}").is_none(), "unanchored");
        assert!(
            PeriodPattern::compile(r"^[0-9]{4}$").is_none(),
            "character class"
        );
        assert!(PeriodPattern::compile(r"^a|b$").is_none(), "alternation");
    }
}
