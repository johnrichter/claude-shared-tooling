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
    /// A pack given to [`Profile::from_packs`] fails structural validation:
    /// its `kind` is not `"extension-pack"`, it declares a core-profile-only
    /// key (`mechanisms`/`cardinality_types`/`violation_cascade`/`codes`/
    /// `authorship`), its `extends` is not a `name@version` string, or one
    /// of its `removes` entries is not a recognized `kind:target` reference.
    /// Carries the offending pack's declared `version` and a detail string.
    MetaSchemaViolation(String, String),
    /// A pack's declared `extends` does not match the core profile's own
    /// `version` -- the pack was authored against a different core than the
    /// one [`Profile::from_packs`] was given.
    VersionSkew {
        /// The pack's own declared `version`.
        pack: String,
        /// The pack's declared `extends`.
        extends: String,
        /// The core profile's actual `version`.
        core: String,
    },
    /// A dangling reference found only after every layer's merge and every
    /// `removes` directive has been applied -- e.g. a namespace's `parents`
    /// entry, or `report`'s trigger/required/period namespace, names a
    /// namespace no layer's merged result still has; or `file_class.default`
    /// names a class [`Profile`]'s merged `description_caps` has no entry
    /// for. Fails closed rather than leaving the merged [`Profile`] in an
    /// inconsistent state a caller could not detect until runtime.
    IntegrityViolation {
        /// Human-readable detail of the dangling reference.
        detail: String,
        /// The dangling target's key.
        key: String,
        /// The layer id(s) implicated in the violation (the layer owning
        /// the referencing entry), when known.
        layers: Vec<String>,
    },
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
            Self::MetaSchemaViolation(pack_id, detail) => {
                write!(f, "pack '{pack_id}' fails meta-schema validation: {detail}")
            }
            Self::VersionSkew {
                pack,
                extends,
                core,
            } => write!(
                f,
                "pack '{pack}' declares extends '{extends}', but the supplied core is '{core}'"
            ),
            Self::IntegrityViolation {
                detail,
                key,
                layers,
            } => write!(
                f,
                "post-merge integrity violation for '{key}' (layers: {layers:?}): {detail}"
            ),
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
    /// mechanisms unchanged. A single-pack special case of
    /// [`Profile::from_packs`]; any [`MergeWarning`] it could produce is
    /// necessarily empty (a lone pack has nothing to override or remove
    /// against), so this discards the warnings vector rather than returning
    /// it.
    ///
    /// # Errors
    /// See [`Profile::from_packs`].
    pub fn from_pack_json(pack_json: &str) -> Result<Self, ProfileError> {
        Self::from_json(EMBEDDED_CORE_JSON, pack_json)
    }

    /// Builds a `Profile` from caller-supplied core and pack JSON text. A
    /// single-pack special case of [`Profile::from_packs`] -- see that
    /// method's doc for the full merge contract; with exactly one pack there
    /// is nothing to override or remove, so every vocabulary entry merges in
    /// silently and the discarded warnings vector is always empty.
    ///
    /// # Errors
    /// See [`Profile::from_packs`].
    pub fn from_json(core_json: &str, pack_json: &str) -> Result<Self, ProfileError> {
        Self::from_packs(core_json, &[pack_json]).map(|(profile, _warnings)| profile)
    }

    /// Builds a `Profile` by layering one or more extension packs onto a
    /// core profile, `pack_jsons` earliest-first: the core's own mechanisms
    /// (cardinality types, cascade, message templates) are structurally
    /// inviolable -- a pack that declares `kind: "core-profile"` or any
    /// core-only key (`mechanisms`/`cardinality_types`/`violation_cascade`/
    /// `codes`/`authorship`) is rejected outright, never silently merged.
    /// Above that fixed floor, each pack's VOCABULARY (`required_fields` by
    /// field, `description_caps` by file class, `namespaces` by name,
    /// `report`'s trigger/period/required-namespace entries, `file_class`
    /// rules and default, `exempt`'s filenames/dirs/globs) merges key by
    /// key, later pack wins:
    /// - a key no earlier pack defined merges in silently;
    /// - a key an earlier pack already defined, redefined with the SAME
    ///   value, is a silent no-op;
    /// - a key redefined with a DIFFERENT value is an [`MergeWarning::Override`],
    ///   the later value winning (last-definer-wins) -- WARN severity if the
    ///   overridden definition came from `pack_jsons[0]` (the base
    ///   vocabulary pack), INFO if from any other pack;
    /// - a pack's `removes: ["kind:target", ...]` directive drops the named
    ///   key from the merge (same WARN/INFO severity rule); removing a key
    ///   nothing defined is a no-op, still warned (naming the dangling
    ///   target) rather than silently ignored.
    ///
    /// After every layer merges, a fixed set of cross-references is
    /// checked against the FINAL merged vocabulary (never against an
    /// intermediate layer) -- a namespace's `parents`, `report`'s
    /// trigger/required/period namespaces, and `file_class.default`'s
    /// description cap must all still resolve; a dangling one is
    /// [`ProfileError::IntegrityViolation`], failing closed rather than
    /// shipping a `Profile` whose cross-references are broken.
    ///
    /// # Determinism
    /// The merged `Profile` and the returned `Vec<MergeWarning>` are pure
    /// functions of `(core_json, pack_jsons)`: identical input (including
    /// pack order) always produces a byte-identical merged profile and
    /// warning list, on every platform and call; reordering `pack_jsons`
    /// can change the result (later wins), but does so deterministically.
    /// Warnings emit in a fixed order: layer application order (the given
    /// `pack_jsons` order), then a fixed per-layer dimension order, then
    /// each dimension's key order (a pack's own declared array order for
    /// additive/override entries; a pack's own declared `removes` array
    /// order for removals) -- never a hash-map iteration order.
    ///
    /// # Errors
    /// [`ProfileError::InvalidCore`] / [`ProfileError::InvalidPack`] on
    /// malformed JSON or a shape that doesn't deserialize into this crate's
    /// `Core`/`Pack`; [`ProfileError::MetaSchemaViolation`] if a pack's
    /// `kind` is not `"extension-pack"`, it declares a core-only key, its
    /// `extends` is not a `name@version` string, or a `removes` entry is
    /// not a recognized `kind:target` reference;
    /// [`ProfileError::VersionSkew`] if a pack's `extends` does not match
    /// the core's own `version`; [`ProfileError::IntegrityViolation`] on a
    /// post-merge dangling reference (see above);
    /// [`ProfileError::InvalidPeriodRegex`] / [`ProfileError::InvalidGlob`]
    /// if the FINAL merged `report.period.regex` / `file_class`/`exempt`
    /// globs don't compile.
    pub fn from_packs(
        core_json: &str,
        pack_jsons: &[&str],
    ) -> Result<(Self, Vec<MergeWarning>), ProfileError> {
        if pack_jsons.is_empty() {
            return Err(ProfileError::MetaSchemaViolation(
                "<none>".to_string(),
                "from_packs requires at least one pack".to_string(),
            ));
        }
        let core: Core = serde_json::from_str(core_json).map_err(ProfileError::InvalidCore)?;

        let mut state = MergeState::default();
        let mut warnings = Vec::new();

        for (layer_index, pack_json) in pack_jsons.iter().enumerate() {
            let raw: serde_json::Value =
                serde_json::from_str(pack_json).map_err(ProfileError::InvalidPack)?;
            let layer: PackLayer =
                serde_json::from_str(pack_json).map_err(ProfileError::InvalidPack)?;
            validate_pack_structure(&raw, &layer)?;
            if layer.extends != core.version {
                return Err(ProfileError::VersionSkew {
                    pack: layer.version.clone(),
                    extends: layer.extends.clone(),
                    core: core.version.clone(),
                });
            }
            merge_layer(&mut state, layer_index, &layer, &mut warnings);
            for reference in &layer.removes {
                apply_removal(&mut state, &layer.version, reference, &mut warnings);
            }
        }

        let pack = build_pack(&state);
        check_integrity(&pack, &state)?;

        let period_pattern = Regex::new(&pack.report.period.regex).map_err(|err| {
            ProfileError::InvalidPeriodRegex(pack.report.period.regex.clone(), err)
        })?;
        let globs = CompiledGlobs::compile(&pack)?;

        Ok((
            Self {
                core,
                pack,
                period_pattern,
                globs,
            },
            warnings,
        ))
    }

    /// The merged namespace named `name`'s [`FacetType`], or `None` if no
    /// merged namespace has that name. The read path a downstream query
    /// consumer (facetquery) uses to decide whether a facet is
    /// equality-only (`String`) or range/comparison-eligible
    /// (`Date`/`Numeric`).
    #[must_use]
    pub fn namespace_facet_type(&self, name: &str) -> Option<FacetType> {
        self.pack
            .namespaces
            .iter()
            .find(|n| n.name == name)
            .map(|n| n.facet_type)
    }

    /// The merged pack's required fields, in pack-declared order: each
    /// entry is `(field name, authorship)`, `authorship` being the pack's
    /// own declared string (e.g. `"human_authored"`, `"machine_derivable"`).
    /// The read path the Tier-1 fixer (`crate::fix`) uses to decide which
    /// required field it may only stub (a placeholder a human/model must
    /// still author) versus which it can fully repair itself -- driven
    /// entirely by this schema value, never a hardcoded field-name list.
    pub fn required_fields(&self) -> impl Iterator<Item = (&str, &str)> {
        self.pack
            .required_fields
            .iter()
            .map(|rf| (rf.field.as_str(), rf.authorship.as_str()))
    }

    /// The description-length cap for `file_class`, or `None` if the merged
    /// pack has no cap for that class (an uncapped class). The same value
    /// [`crate::validate::validate`] checks a description against.
    #[must_use]
    pub fn description_cap(&self, file_class: &str) -> Option<u64> {
        self.pack.description_caps.get(file_class)
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
    /// The core's own `name@version` (e.g. `"core@1"`) -- compared against
    /// every pack's declared `extends` by [`Profile::from_packs`] to catch
    /// version skew before any merge work happens.
    pub(crate) version: String,
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

#[derive(Debug, Clone, Deserialize, PartialEq)]
pub(crate) struct RequiredField {
    pub(crate) field: String,
    /// Read by [`Profile::required_fields`] -- the Tier-1 fixer's
    /// machine-vs-human boundary; `validate` itself only needs `field`.
    pub(crate) authorship: String,
}

/// `file_class -> max description length`. Deserialized from a JSON object
/// that also carries a non-numeric `source` provenance string alongside
/// the numeric class entries (see the pack file) -- [`DescriptionCaps::get`]
/// is the only accessor, so that shape detail stays contained here.
///
/// Backed by `serde_json::Map` (a `BTreeMap` in this crate's build, since no
/// dependency here enables `serde_json`'s `preserve_order` feature), not
/// `std::collections::HashMap` -- [`DescriptionCaps::caps_entries`] iterates
/// it for [`Profile::from_packs`]' merge, which needs a platform- and
/// process-stable key order; `std::HashMap`'s randomized-per-process
/// iteration order would break that determinism contract.
#[derive(Debug, Clone, Deserialize)]
pub(crate) struct DescriptionCaps(serde_json::Map<String, serde_json::Value>);

impl DescriptionCaps {
    /// The cap for `file_class`, or `None` if the pack has no cap for that
    /// class (see `frontmatter-core.schema.json`'s `description_caps`
    /// mechanism: an absent class is uncapped).
    pub(crate) fn get(&self, file_class: &str) -> Option<u64> {
        self.0.get(file_class).and_then(serde_json::Value::as_u64)
    }

    /// This pack's cap entries as `(file_class, cap)` pairs, in the
    /// underlying map's stable iteration order -- every non-numeric entry
    /// (the `source` provenance string) is dropped, since it is not a
    /// mergeable vocabulary key.
    pub(crate) fn caps_entries(&self) -> Vec<(String, u64)> {
        self.0
            .iter()
            .filter_map(|(class, cap)| cap.as_u64().map(|cap| (class.clone(), cap)))
            .collect()
    }

    /// Rebuilds a `DescriptionCaps` from merged `(file_class, cap)` pairs --
    /// the inverse of [`DescriptionCaps::caps_entries`], used to assemble
    /// [`Profile::from_packs`]' merged pack.
    pub(crate) fn from_entries(entries: &[(String, u64)]) -> Self {
        let mut map = serde_json::Map::new();
        for (class, cap) in entries {
            map.insert(class.clone(), serde_json::Value::from(*cap));
        }
        Self(map)
    }
}

#[derive(Debug, Clone, Deserialize)]
pub(crate) struct FileClassRules {
    pub(crate) default: String,
    pub(crate) rules: Vec<FileClassRule>,
}

#[derive(Debug, Clone, Deserialize, PartialEq)]
pub(crate) struct FileClassRule {
    pub(crate) class: String,
    #[serde(rename = "match")]
    pub(crate) matcher: FileClassMatch,
}

#[derive(Debug, Clone, Deserialize, PartialEq)]
pub(crate) struct FileClassMatch {
    pub(crate) glob: String,
}

/// One namespace's cascade participation: its cardinality plus the two
/// optional axes (`parents`, `report_only`) that add extra cascade phases
/// for that namespace. Array order in the pack (this struct's source
/// position in `Pack::namespaces`) IS iteration order -- see
/// `namespaces_order_note` in the pack file.
#[derive(Debug, Clone, Deserialize, PartialEq)]
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

#[derive(Debug, Clone, Deserialize, PartialEq)]
pub(crate) struct ReportTrigger {
    pub(crate) namespace: String,
    pub(crate) value: String,
}

#[derive(Debug, Clone, Deserialize, PartialEq)]
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
// Layered merge (Profile::from_packs)
// ---------------------------------------------------------------------------

/// One input layer to [`Profile::from_packs`]: an extension pack's merge
/// metadata (`kind`/`version`/`extends`/`removes`) alongside the same
/// vocabulary fields a single caller-supplied pack has -- `#[serde(flatten)]`
/// deserializes both from one JSON object into this struct's `vocabulary`
/// field, so [`Pack`] stays the crate's one pack-vocabulary shape whether it
/// arrives alone (`Profile::from_json`) or as one of several merge layers.
#[derive(Debug, Clone, Deserialize)]
struct PackLayer {
    kind: String,
    version: String,
    extends: String,
    #[serde(default)]
    removes: Vec<String>,
    #[serde(flatten)]
    vocabulary: Pack,
}

/// A dimension of the pack vocabulary [`Profile::from_packs`] merges --
/// fixes both the meaning of a [`MergeWarning`]'s `dimension` and the order
/// dimensions are merged within one layer (declared here in the same order
/// as [`Pack`]'s own fields), part of this crate's warning-ordering
/// determinism contract.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Dimension {
    /// `required_fields`, keyed by `field`.
    RequiredField,
    /// `description_caps`, keyed by file class.
    DescriptionCap,
    /// `file_class.default` (a single always-present key).
    FileClassDefault,
    /// `file_class.rules`, keyed by `class`.
    FileClassRule,
    /// `namespaces`, keyed by `name`.
    Namespace,
    /// `report.trigger` (a single always-present key).
    ReportTrigger,
    /// `report.period` (a single always-present key).
    ReportPeriod,
    /// `report.required_namespaces`, keyed by namespace name.
    ReportRequired,
    /// `exempt.filenames`, keyed by filename.
    ExemptFilename,
    /// `exempt.dir_components`, keyed by directory component.
    ExemptDir,
    /// `exempt.path_globs`, keyed by glob pattern.
    ExemptGlob,
}

/// A record of one non-additive merge decision [`Profile::from_packs`] made
/// -- a later layer overriding or removing an earlier layer's vocabulary
/// key. A purely additive merge key (no earlier layer defined it) or an
/// identical restatement (a later layer redeclares a key with the SAME
/// value) produces no warning at all; only a genuine value change or a
/// removal does.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum MergeWarning {
    /// A later layer redefined `key` (in `dimension`) with a value that
    /// differs from `from_layer`'s -- `to_layer`'s value won (last-definer-
    /// wins). `base_layer` is `true` when `from_layer` was the FIRST pack
    /// given to [`Profile::from_packs`] (the base vocabulary pack) -- the
    /// higher-severity case (WARN, vs. INFO for a pack overriding another
    /// pack).
    Override {
        /// Which vocabulary dimension the overridden key belongs to.
        dimension: Dimension,
        /// The overridden key.
        key: String,
        /// The layer id (`version`) whose value was replaced.
        from_layer: String,
        /// The layer id (`version`) whose value won.
        to_layer: String,
        /// `true` iff `from_layer` was `pack_jsons[0]`.
        base_layer: bool,
    },
    /// `removing_layer`'s `removes` directive dropped `key` (in
    /// `dimension`). `removed_from_layer` is the layer id that had defined
    /// the removed key, or `None` if the directive named a key nothing had
    /// defined (a no-op, still warned rather than silently ignored).
    /// `base_layer` is `true` iff `removed_from_layer` was `pack_jsons[0]`.
    Removal {
        /// Which vocabulary dimension the removed key belongs to.
        dimension: Dimension,
        /// The removed (or dangling-target) key.
        key: String,
        /// The layer id (`version`) whose `removes` directive fired.
        removing_layer: String,
        /// The layer id that had defined `key`, or `None` for a no-op
        /// removal of a key nothing defined.
        removed_from_layer: Option<String>,
        /// `true` iff `removed_from_layer` was `pack_jsons[0]`.
        base_layer: bool,
    },
}

/// One merged vocabulary key: its current value(s) (more than one only for
/// [`Dimension::FileClassRule`], where one pack layer may legitimately
/// declare several rules sharing one `class`) and the layer that last
/// defined it. Position in the owning `Vec<MergedSlot<V>>` IS the merged
/// pack's iteration order for that dimension -- a new key appends at the
/// end; an override replaces this slot's `values`/layer IN PLACE, keeping
/// its original position, so a later layer overriding an EARLIER namespace/
/// required-field/file-class-rule never reshuffles the cascade order that
/// key's position encodes.
struct MergedSlot<V> {
    key: String,
    values: Vec<V>,
    layer_index: usize,
    layer_id: String,
}

/// The full merge-in-progress state, one ordered slot list per
/// [`Dimension`] -- assembled across every layer by [`merge_layer`] and
/// [`apply_removal`], then read once by [`build_pack`] to produce the final
/// merged [`Pack`].
#[derive(Default)]
struct MergeState {
    required_fields: Vec<MergedSlot<RequiredField>>,
    description_caps: Vec<MergedSlot<u64>>,
    file_class_default: Vec<MergedSlot<String>>,
    file_class_rules: Vec<MergedSlot<FileClassRule>>,
    namespaces: Vec<MergedSlot<NamespaceSpec>>,
    report_trigger: Vec<MergedSlot<ReportTrigger>>,
    report_period: Vec<MergedSlot<ReportPeriod>>,
    report_required: Vec<MergedSlot<()>>,
    exempt_filenames: Vec<MergedSlot<()>>,
    exempt_dirs: Vec<MergedSlot<()>>,
    exempt_globs: Vec<MergedSlot<()>>,
}

/// Groups `items` by key, preserving each key's first-appearance position
/// and each key's own values in their given order -- the shape
/// [`merge_grouped`] needs to treat "one pack layer declares the same key
/// more than once" (the bundled psa-apm pack's two `class: "skill"`
/// `file_class` rules) as ONE bundle, not a spurious same-layer conflict.
fn group_by_key<V>(items: Vec<(String, V)>) -> Vec<(String, Vec<V>)> {
    let mut groups: Vec<(String, Vec<V>)> = Vec::new();
    for (key, value) in items {
        match groups.iter_mut().find(|(k, _)| *k == key) {
            Some((_, values)) => values.push(value),
            None => groups.push((key, vec![value])),
        }
    }
    groups
}

/// Merges one layer's `groups` (this layer's own vocabulary for one
/// dimension, already bundled by [`group_by_key`]) into `slots`, appending
/// [`MergeWarning::Override`]s to `warnings` for every genuine value change
/// -- the additive/override half of the merge rule; see [`Profile::from_packs`].
fn merge_grouped<V: Clone + PartialEq>(
    slots: &mut Vec<MergedSlot<V>>,
    layer_index: usize,
    layer_id: &str,
    dimension: Dimension,
    groups: Vec<(String, Vec<V>)>,
    warnings: &mut Vec<MergeWarning>,
) {
    for (key, values) in groups {
        match slots.iter().position(|slot| slot.key == key) {
            None => slots.push(MergedSlot {
                key,
                values,
                layer_index,
                layer_id: layer_id.to_string(),
            }),
            Some(pos) => {
                if slots[pos].values != values {
                    warnings.push(MergeWarning::Override {
                        dimension,
                        key: key.clone(),
                        from_layer: slots[pos].layer_id.clone(),
                        to_layer: layer_id.to_string(),
                        base_layer: slots[pos].layer_index == 0,
                    });
                    slots[pos] = MergedSlot {
                        key,
                        values,
                        layer_index,
                        layer_id: layer_id.to_string(),
                    };
                }
                // Identical restatement (`slots[pos].values == values`):
                // silent no-op, including a layer re-bundling its own
                // multi-rule key -- `group_by_key` already folded a single
                // layer's repeated key into one `values` bundle, so this
                // branch is reached only by a genuine cross-layer
                // redeclaration.
            }
        }
    }
}

/// Merges every dimension of one pack layer (in [`Dimension`]'s declared
/// order) into `state`. Removals are NOT applied here -- per
/// [`Profile::from_packs`]' contract, a layer's `removes` directives apply
/// after its own additive/override entries, so the caller applies them via
/// [`apply_removal`] once this returns.
fn merge_layer(
    state: &mut MergeState,
    layer_index: usize,
    layer: &PackLayer,
    warnings: &mut Vec<MergeWarning>,
) {
    merge_layer_vocabulary(state, layer_index, layer, warnings);
    merge_layer_report(state, layer_index, layer, warnings);
    merge_layer_exempt(state, layer_index, layer, warnings);
}

/// The `required_fields`/`description_caps`/`file_class`/`namespaces`
/// slice of one layer's [`Dimension`] set -- split out of [`merge_layer`]
/// purely to stay under this crate's function-length lint; see that
/// function's doc for the ordering contract this and its siblings jointly
/// uphold.
fn merge_layer_vocabulary(
    state: &mut MergeState,
    layer_index: usize,
    layer: &PackLayer,
    warnings: &mut Vec<MergeWarning>,
) {
    let pack = &layer.vocabulary;
    let layer_id = &layer.version;

    merge_grouped(
        &mut state.required_fields,
        layer_index,
        layer_id,
        Dimension::RequiredField,
        group_by_key(
            pack.required_fields
                .iter()
                .map(|rf| (rf.field.clone(), rf.clone()))
                .collect(),
        ),
        warnings,
    );
    merge_grouped(
        &mut state.description_caps,
        layer_index,
        layer_id,
        Dimension::DescriptionCap,
        group_by_key(pack.description_caps.caps_entries()),
        warnings,
    );
    merge_grouped(
        &mut state.file_class_default,
        layer_index,
        layer_id,
        Dimension::FileClassDefault,
        vec![("default".to_string(), vec![pack.file_class.default.clone()])],
        warnings,
    );
    merge_grouped(
        &mut state.file_class_rules,
        layer_index,
        layer_id,
        Dimension::FileClassRule,
        group_by_key(
            pack.file_class
                .rules
                .iter()
                .map(|rule| (rule.class.clone(), rule.clone()))
                .collect(),
        ),
        warnings,
    );
    merge_grouped(
        &mut state.namespaces,
        layer_index,
        layer_id,
        Dimension::Namespace,
        group_by_key(
            pack.namespaces
                .iter()
                .map(|ns| (ns.name.clone(), ns.clone()))
                .collect(),
        ),
        warnings,
    );
}

/// The `report` slice of one layer's [`Dimension`] set -- see
/// [`merge_layer_vocabulary`]'s doc for why this is split out.
fn merge_layer_report(
    state: &mut MergeState,
    layer_index: usize,
    layer: &PackLayer,
    warnings: &mut Vec<MergeWarning>,
) {
    let pack = &layer.vocabulary;
    let layer_id = &layer.version;

    merge_grouped(
        &mut state.report_trigger,
        layer_index,
        layer_id,
        Dimension::ReportTrigger,
        vec![("trigger".to_string(), vec![pack.report.trigger.clone()])],
        warnings,
    );
    merge_grouped(
        &mut state.report_period,
        layer_index,
        layer_id,
        Dimension::ReportPeriod,
        vec![("period".to_string(), vec![pack.report.period.clone()])],
        warnings,
    );
    merge_grouped(
        &mut state.report_required,
        layer_index,
        layer_id,
        Dimension::ReportRequired,
        group_by_key(
            pack.report
                .required_namespaces
                .iter()
                .map(|ns| (ns.clone(), ()))
                .collect(),
        ),
        warnings,
    );
}

/// The `exempt` slice of one layer's [`Dimension`] set -- see
/// [`merge_layer_vocabulary`]'s doc for why this is split out.
fn merge_layer_exempt(
    state: &mut MergeState,
    layer_index: usize,
    layer: &PackLayer,
    warnings: &mut Vec<MergeWarning>,
) {
    let pack = &layer.vocabulary;
    let layer_id = &layer.version;

    merge_grouped(
        &mut state.exempt_filenames,
        layer_index,
        layer_id,
        Dimension::ExemptFilename,
        group_by_key(
            pack.exempt
                .filenames
                .iter()
                .map(|name| (name.clone(), ()))
                .collect(),
        ),
        warnings,
    );
    merge_grouped(
        &mut state.exempt_dirs,
        layer_index,
        layer_id,
        Dimension::ExemptDir,
        group_by_key(
            pack.exempt
                .dir_components
                .iter()
                .map(|dir| (dir.clone(), ()))
                .collect(),
        ),
        warnings,
    );
    merge_grouped(
        &mut state.exempt_globs,
        layer_index,
        layer_id,
        Dimension::ExemptGlob,
        group_by_key(
            pack.exempt
                .path_globs
                .iter()
                .map(|glob| (glob.clone(), ()))
                .collect(),
        ),
        warnings,
    );
}

/// Removes every slot keyed `key` from `slots` (there is at most one after
/// [`merge_grouped`]'s override collapsing, but this removes all matches
/// defensively), returning the removed slot's owning `(layer_index,
/// layer_id)`, if any.
fn remove_slot<V>(slots: &mut Vec<MergedSlot<V>>, key: &str) -> Option<(usize, String)> {
    let mut owner = None;
    slots.retain(|slot| {
        if slot.key == key {
            owner.get_or_insert_with(|| (slot.layer_index, slot.layer_id.clone()));
            false
        } else {
            true
        }
    });
    owner
}

/// Applies one `reference` (`"kind:target"`, already pattern-validated by
/// [`validate_pack_structure`]) from `removing_layer`'s `removes` array,
/// dropping the named key from the matching dimension's slots and recording
/// a [`MergeWarning::Removal`] -- always, even for a dangling target (a
/// no-op, per [`Profile::from_packs`]' contract).
fn apply_removal(
    state: &mut MergeState,
    removing_layer: &str,
    reference: &str,
    warnings: &mut Vec<MergeWarning>,
) {
    let Some((kind, target)) = reference.split_once(':') else {
        // Unreachable given `validate_pack_structure` already rejected any
        // reference without a ':' -- guarded rather than unwrapped, so a
        // future change to that check fails safe (a no-op skip) instead of
        // panicking here.
        return;
    };
    let (dimension, owner) = match kind {
        "namespace" => (
            Dimension::Namespace,
            remove_slot(&mut state.namespaces, target),
        ),
        "required_field" => (
            Dimension::RequiredField,
            remove_slot(&mut state.required_fields, target),
        ),
        "description_cap" => (
            Dimension::DescriptionCap,
            remove_slot(&mut state.description_caps, target),
        ),
        "file_class_rule" => (
            Dimension::FileClassRule,
            remove_slot(&mut state.file_class_rules, target),
        ),
        "report_required" => (
            Dimension::ReportRequired,
            remove_slot(&mut state.report_required, target),
        ),
        "exempt_filename" => (
            Dimension::ExemptFilename,
            remove_slot(&mut state.exempt_filenames, target),
        ),
        "exempt_dir" => (
            Dimension::ExemptDir,
            remove_slot(&mut state.exempt_dirs, target),
        ),
        "exempt_glob" => (
            Dimension::ExemptGlob,
            remove_slot(&mut state.exempt_globs, target),
        ),
        _ => return, // unreachable: kind already pattern-validated
    };
    let base_layer = owner.as_ref().is_some_and(|(index, _)| *index == 0);
    warnings.push(MergeWarning::Removal {
        dimension,
        key: target.to_string(),
        removing_layer: removing_layer.to_string(),
        base_layer,
        removed_from_layer: owner.map(|(_, layer_id)| layer_id),
    });
}

/// Rebuilds the final merged [`Pack`] from `state` -- each dimension's
/// slots, in their final (post-removal) order, flattened back into the
/// shape `validate::validate` already knows how to read: a key's position
/// in its `Vec<MergedSlot<_>>` is preserved exactly, so cascade-order-
/// sensitive dimensions (`namespaces`, `required_fields`,
/// `report.required_namespaces`) come out in the order the layers
/// converged on, not re-sorted.
fn build_pack(state: &MergeState) -> Pack {
    Pack {
        required_fields: state
            .required_fields
            .iter()
            .flat_map(|slot| slot.values.iter().cloned())
            .collect(),
        description_caps: DescriptionCaps::from_entries(
            &state
                .description_caps
                .iter()
                .flat_map(|slot| slot.values.iter().map(|cap| (slot.key.clone(), *cap)))
                .collect::<Vec<_>>(),
        ),
        file_class: FileClassRules {
            default: state
                .file_class_default
                .first()
                .and_then(|slot| slot.values.first())
                .cloned()
                .unwrap_or_default(),
            rules: state
                .file_class_rules
                .iter()
                .flat_map(|slot| slot.values.iter().cloned())
                .collect(),
        },
        namespaces: state
            .namespaces
            .iter()
            .flat_map(|slot| slot.values.iter().cloned())
            .collect(),
        report: ReportSpec {
            trigger: state
                .report_trigger
                .first()
                .and_then(|slot| slot.values.first())
                .cloned()
                .unwrap_or_else(|| ReportTrigger {
                    namespace: String::new(),
                    value: String::new(),
                }),
            required_namespaces: state
                .report_required
                .iter()
                .map(|slot| slot.key.clone())
                .collect(),
            period: state
                .report_period
                .first()
                .and_then(|slot| slot.values.first())
                .cloned()
                .unwrap_or_else(|| ReportPeriod {
                    namespace: String::new(),
                    regex: String::new(),
                }),
        },
        exempt: ExemptSpec {
            filenames: state
                .exempt_filenames
                .iter()
                .map(|s| s.key.clone())
                .collect(),
            dir_components: state.exempt_dirs.iter().map(|s| s.key.clone()).collect(),
            path_globs: state.exempt_globs.iter().map(|s| s.key.clone()).collect(),
        },
    }
}

/// Cross-reference checks run once, after every layer has merged and every
/// `removes` directive has applied -- see [`Profile::from_packs`]'
/// `IntegrityViolation` contract. Checked in a fixed order (namespace
/// parents, then `report`'s trigger/required/period, then
/// `file_class.default`'s cap), so the first dangling reference found is
/// always the same one for identical input.
fn check_integrity(pack: &Pack, state: &MergeState) -> Result<(), ProfileError> {
    let namespace_exists = |name: &str| pack.namespaces.iter().any(|ns| ns.name == name);
    let namespace_layers = |name: &str| -> Vec<String> {
        state
            .namespaces
            .iter()
            .find(|slot| slot.key == name)
            .map(|slot| vec![slot.layer_id.clone()])
            .unwrap_or_default()
    };

    for ns in &pack.namespaces {
        for parent in &ns.parents {
            if !namespace_exists(parent) {
                return Err(ProfileError::IntegrityViolation {
                    detail: format!(
                        "namespace '{}' declares parent '{parent}', which is absent from the merged profile",
                        ns.name
                    ),
                    key: parent.clone(),
                    layers: namespace_layers(&ns.name),
                });
            }
        }
    }
    if !namespace_exists(&pack.report.trigger.namespace) {
        return Err(ProfileError::IntegrityViolation {
            detail: format!(
                "report.trigger references namespace '{}', which is absent from the merged profile",
                pack.report.trigger.namespace
            ),
            key: pack.report.trigger.namespace.clone(),
            layers: state
                .report_trigger
                .first()
                .map(|slot| vec![slot.layer_id.clone()])
                .unwrap_or_default(),
        });
    }
    for required in &pack.report.required_namespaces {
        if !namespace_exists(required) {
            return Err(ProfileError::IntegrityViolation {
                detail: format!(
                    "report.required_namespaces references namespace '{required}', which is absent from the merged profile"
                ),
                key: required.clone(),
                layers: state
                    .report_required
                    .iter()
                    .find(|slot| slot.key == *required)
                    .map(|slot| vec![slot.layer_id.clone()])
                    .unwrap_or_default(),
            });
        }
    }
    if !namespace_exists(&pack.report.period.namespace) {
        return Err(ProfileError::IntegrityViolation {
            detail: format!(
                "report.period references namespace '{}', which is absent from the merged profile",
                pack.report.period.namespace
            ),
            key: pack.report.period.namespace.clone(),
            layers: state
                .report_period
                .first()
                .map(|slot| vec![slot.layer_id.clone()])
                .unwrap_or_default(),
        });
    }
    if pack
        .description_caps
        .get(&pack.file_class.default)
        .is_none()
    {
        return Err(ProfileError::IntegrityViolation {
            detail: format!(
                "file_class.default '{}' has no description_caps entry",
                pack.file_class.default
            ),
            key: pack.file_class.default.clone(),
            layers: state
                .file_class_default
                .first()
                .map(|slot| vec![slot.layer_id.clone()])
                .unwrap_or_default(),
        });
    }
    Ok(())
}

/// The `kind:target` reference shape [`PackLayer::removes`] entries must
/// match -- kept as a Rust literal (rather than reading it live off
/// `frontmatter-profile.meta.schema.json` at construction time) so a
/// malformed embedded meta-schema file can never turn an ordinary
/// `Profile::from_packs` call into a panic; a test in this module's
/// `#[cfg(test)]` block cross-checks this literal against the meta-schema's
/// own pattern so the two cannot silently drift.
const REMOVES_REFERENCE_PATTERN: &str = r"^(namespace|required_field|description_cap|file_class_rule|report_required|exempt_filename|exempt_dir|exempt_glob):.+$";

/// The `extends`/`version` `name@version` shape both the core profile and
/// every pack must satisfy -- mirrors `frontmatter-profile.meta.schema.json`'s
/// `version`/`extends` pattern (see [`REMOVES_REFERENCE_PATTERN`]'s doc for
/// why this is a literal, not a live meta-schema read).
const VERSIONED_NAME_PATTERN: &str = r"^[a-z0-9-]+@[0-9]+$";

/// Core-profile-only top-level keys a pack must never declare -- their
/// presence means a pack is trying to redefine cardinality types, the
/// cascade, or a message template, which [`Profile::from_packs`]' doc
/// guarantees are structurally inviolable.
const RESERVED_CORE_ONLY_KEYS: &[&str] = &[
    "cardinality_types",
    "authorship",
    "mechanisms",
    "violation_cascade",
    "codes",
];

/// Structural checks [`Profile::from_packs`] runs on every pack layer after
/// it has already deserialized cleanly into [`PackLayer`] (a syntax/shape
/// failure there is [`ProfileError::InvalidPack`], not this function's
/// concern): `kind` must be `"extension-pack"`; `raw` (the same JSON, as a
/// [`serde_json::Value`]) must not declare any [`RESERVED_CORE_ONLY_KEYS`];
/// `extends` must match [`VERSIONED_NAME_PATTERN`]; every `removes` entry
/// must match [`REMOVES_REFERENCE_PATTERN`]. This -- serde's own typed
/// deserialization plus these targeted checks -- is this crate's chosen
/// meta-schema-conformance mechanism; see this task's delivery report for
/// why a full JSON-Schema-2020-12 validator crate was judged unnecessary.
fn validate_pack_structure(raw: &serde_json::Value, layer: &PackLayer) -> Result<(), ProfileError> {
    let pack_id = layer.version.clone();
    if layer.kind != "extension-pack" {
        return Err(ProfileError::MetaSchemaViolation(
            pack_id,
            format!(
                "kind must be 'extension-pack', found '{}' -- a pack cannot declare core-profile mechanisms",
                layer.kind
            ),
        ));
    }
    if let Some(object) = raw.as_object() {
        for reserved in RESERVED_CORE_ONLY_KEYS {
            if object.contains_key(*reserved) {
                return Err(ProfileError::MetaSchemaViolation(
                    pack_id,
                    format!(
                        "pack declares core-only key '{reserved}' -- cardinality types, the cascade, and message templates are core-profile-inviolable"
                    ),
                ));
            }
        }
    }
    let versioned_name = Regex::new(VERSIONED_NAME_PATTERN)
        .expect("VERSIONED_NAME_PATTERN is a fixed, tested literal");
    if !versioned_name.is_match(&layer.extends) {
        return Err(ProfileError::MetaSchemaViolation(
            pack_id,
            format!(
                "extends '{}' does not match the required 'name@version' shape",
                layer.extends
            ),
        ));
    }
    let removes_reference = Regex::new(REMOVES_REFERENCE_PATTERN)
        .expect("REMOVES_REFERENCE_PATTERN is a fixed, tested literal");
    for reference in &layer.removes {
        if !removes_reference.is_match(reference) {
            return Err(ProfileError::MetaSchemaViolation(
                pack_id,
                format!("removes entry '{reference}' is not a recognized 'kind:target' reference"),
            ));
        }
    }
    Ok(())
}

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

    // -- SDET: meta-schema conformance --------------------------------------
    //
    // A full JSON-Schema-2020-12 validator crate would be a heavy dep for
    // what serde's own typed deserialization plus `validate_pack_structure`'s
    // targeted checks already cover (see that function's doc). This section
    // pins what those targeted checks are cross-checked against:
    //   1. the meta-schema's own `removes` pattern and `namespaces[].type`
    //      enum, read live off the checked-in meta-schema JSON (not
    //      hand-copied), behave as documented, and match this crate's own
    //      `REMOVES_REFERENCE_PATTERN` literal exactly (drift check);
    //   2. the bundled core/pack JSON satisfy the meta-schema's top-level
    //      `required` shape for their `kind`;
    //   3. `type`'s serde enum enforcement (see
    //      `an_unknown_facet_type_value_is_a_typed_error_not_a_panic` above)
    //      and `removes`-entry enforcement (see the `Profile::from_packs`
    //      tests below) are both live paths through this crate today, not
    //      just meta-schema-documented shapes.
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
    fn a_malformed_removes_entry_is_rejected_as_a_meta_schema_violation() {
        let mut pack = bundled_pack_as_value_standalone();
        pack["removes"] = serde_json::json!(["not-a-valid-typed-reference"]);
        let result = Profile::from_pack_json(&pack.to_string());
        assert!(
            matches!(result, Err(ProfileError::MetaSchemaViolation(_, _))),
            "a `removes` entry that doesn't match the typed-reference shape must be rejected: {result:?}"
        );
    }

    #[test]
    fn removes_reference_pattern_literal_matches_the_meta_schemas_own_pattern() {
        // Cross-checks the hard-coded `REMOVES_REFERENCE_PATTERN` against
        // the live meta-schema's `removes.items.pattern`, so the two cannot
        // silently drift (see `REMOVES_REFERENCE_PATTERN`'s doc).
        let schema = meta_schema();
        let live_pattern = schema["$defs"]["extensionPack"]["properties"]["removes"]["items"]
            ["pattern"]
            .as_str()
            .expect("meta-schema must declare removes.items.pattern");
        assert_eq!(live_pattern, REMOVES_REFERENCE_PATTERN);
    }

    // -- Profile::from_packs -- the layered merge engine -------------------

    const MERGE_BASE_PACK: &str = r#"{
        "kind": "extension-pack",
        "version": "base@1",
        "extends": "core@1",
        "required_fields": [{"field": "name", "authorship": "human_authored"}],
        "description_caps": {"context": 100, "extra": 50},
        "file_class": {"default": "context", "rules": []},
        "namespaces": [
            {"name": "type", "cardinality": "singleton"},
            {"name": "period", "cardinality": "optional", "type": "date"}
        ],
        "report": {
            "trigger": {"namespace": "type", "value": "report"},
            "required_namespaces": [],
            "period": {"namespace": "period", "regex": "^.*$"}
        },
        "exempt": {"filenames": [], "dir_components": [], "path_globs": []}
    }"#;

    const MERGE_EXT_PACK: &str = r#"{
        "kind": "extension-pack",
        "version": "ext@1",
        "extends": "core@1",
        "removes": ["required_field:name"],
        "required_fields": [],
        "description_caps": {"context": 200},
        "file_class": {"default": "context", "rules": []},
        "namespaces": [
            {"name": "team", "cardinality": "optional"},
            {"name": "period", "cardinality": "optional", "type": "numeric"}
        ],
        "report": {
            "trigger": {"namespace": "type", "value": "report"},
            "required_namespaces": [],
            "period": {"namespace": "period", "regex": "^.*$"}
        },
        "exempt": {"filenames": [], "dir_components": [], "path_globs": []}
    }"#;

    const MERGE_THIRD_PACK: &str = r#"{
        "kind": "extension-pack",
        "version": "third@1",
        "extends": "core@1",
        "removes": ["description_cap:ghost"],
        "required_fields": [],
        "description_caps": {"context": 300},
        "file_class": {"default": "context", "rules": []},
        "namespaces": [
            {"name": "team", "cardinality": "optional"}
        ],
        "report": {
            "trigger": {"namespace": "type", "value": "report"},
            "required_namespaces": [],
            "period": {"namespace": "period", "regex": "^.*$"}
        },
        "exempt": {"filenames": [], "dir_components": [], "path_globs": []}
    }"#;

    /// The exact warning sequence three layers (`base`/`ext`/`third`
    /// above) must produce -- one instance of every non-additive case this
    /// merge engine handles: a base-layer override (WARN), a pack-over-pack
    /// override (INFO), a removal of a base-layer key, and a no-op removal
    /// of a key nothing defined. An additive key (`team` on `ext@1`) and an
    /// identical redefinition (`team` restated verbatim on `third@1`)
    /// produce no warning at all, which this expected list proves by
    /// omission.
    fn expected_merge_warnings() -> Vec<MergeWarning> {
        vec![
            MergeWarning::Override {
                dimension: Dimension::DescriptionCap,
                key: "context".to_string(),
                from_layer: "base@1".to_string(),
                to_layer: "ext@1".to_string(),
                base_layer: true,
            },
            MergeWarning::Override {
                dimension: Dimension::Namespace,
                key: "period".to_string(),
                from_layer: "base@1".to_string(),
                to_layer: "ext@1".to_string(),
                base_layer: true,
            },
            MergeWarning::Removal {
                dimension: Dimension::RequiredField,
                key: "name".to_string(),
                removing_layer: "ext@1".to_string(),
                removed_from_layer: Some("base@1".to_string()),
                base_layer: true,
            },
            MergeWarning::Override {
                dimension: Dimension::DescriptionCap,
                key: "context".to_string(),
                from_layer: "ext@1".to_string(),
                to_layer: "third@1".to_string(),
                base_layer: false,
            },
            MergeWarning::Removal {
                dimension: Dimension::DescriptionCap,
                key: "ghost".to_string(),
                removing_layer: "third@1".to_string(),
                removed_from_layer: None,
                base_layer: false,
            },
        ]
    }

    #[test]
    fn golden_merge_produces_the_expected_profile_and_warning_sequence() {
        let (profile, warnings) = Profile::from_packs(
            EMBEDDED_CORE_JSON,
            &[MERGE_BASE_PACK, MERGE_EXT_PACK, MERGE_THIRD_PACK],
        )
        .expect("three consistent layers must merge cleanly");

        assert_eq!(warnings, expected_merge_warnings());
        assert!(
            profile.pack.required_fields.is_empty(),
            "removed field must be gone"
        );
        assert_eq!(profile.pack.description_caps.get("context"), Some(300));
        assert_eq!(profile.pack.description_caps.get("extra"), Some(50));
        assert_eq!(
            profile
                .pack
                .namespaces
                .iter()
                .map(|ns| ns.name.clone())
                .collect::<Vec<_>>(),
            vec!["type", "period", "team"],
            "namespace position is preserved across override, not reshuffled"
        );
        assert_eq!(
            profile.namespace_facet_type("period"),
            Some(FacetType::Numeric)
        );
        assert_eq!(
            profile.namespace_facet_type("type"),
            Some(FacetType::String)
        );
        assert_eq!(profile.namespace_facet_type("no-such-namespace"), None);
    }

    #[test]
    fn merge_is_a_pure_function_of_its_ordered_inputs() {
        let (profile_a, warnings_a) = Profile::from_packs(
            EMBEDDED_CORE_JSON,
            &[MERGE_BASE_PACK, MERGE_EXT_PACK, MERGE_THIRD_PACK],
        )
        .expect("merge must succeed");
        let (profile_b, warnings_b) = Profile::from_packs(
            EMBEDDED_CORE_JSON,
            &[MERGE_BASE_PACK, MERGE_EXT_PACK, MERGE_THIRD_PACK],
        )
        .expect("merge must succeed");

        assert_eq!(warnings_a, warnings_b);
        assert_eq!(
            format!("{profile_a:?}"),
            format!("{profile_b:?}"),
            "repeated merge of identical input must be byte-identical"
        );
    }

    #[test]
    fn reordering_the_pack_set_changes_the_result_deterministically() {
        let (forward, _) =
            Profile::from_packs(EMBEDDED_CORE_JSON, &[MERGE_BASE_PACK, MERGE_EXT_PACK])
                .expect("merge must succeed");
        let (reversed, _) =
            Profile::from_packs(EMBEDDED_CORE_JSON, &[MERGE_EXT_PACK, MERGE_BASE_PACK])
                .expect("merge must succeed");

        assert_eq!(
            forward.pack.description_caps.get("context"),
            Some(200),
            "ext wins when applied last"
        );
        assert_eq!(
            reversed.pack.description_caps.get("context"),
            Some(100),
            "base wins when applied last"
        );

        // And each order is itself deterministic on repetition.
        let (forward_again, _) =
            Profile::from_packs(EMBEDDED_CORE_JSON, &[MERGE_BASE_PACK, MERGE_EXT_PACK])
                .expect("merge must succeed");
        assert_eq!(format!("{forward:?}"), format!("{forward_again:?}"));
    }

    #[test]
    fn from_pack_json_and_bundled_psa_apm_are_unaffected_by_from_packs() {
        // Single-pack special cases must keep behaving exactly as before --
        // no warnings surface (there is nothing to override or remove
        // against with only one pack) and the profile is unchanged.
        let _bundled = Profile::bundled_psa_apm();
        let via_from_pack_json = Profile::from_pack_json(EMBEDDED_PSA_APM_PACK_JSON)
            .expect("bundled pack JSON must still build a Profile");
        assert_eq!(via_from_pack_json.pack.required_fields.len(), 6);
    }

    #[test]
    fn a_pack_declaring_kind_core_profile_is_a_meta_schema_violation() {
        let mut pack: serde_json::Value =
            serde_json::from_str(MERGE_BASE_PACK).expect("fixture must parse");
        pack["kind"] = serde_json::json!("core-profile");
        let result = Profile::from_packs(EMBEDDED_CORE_JSON, &[&pack.to_string()]);
        assert!(matches!(
            result,
            Err(ProfileError::MetaSchemaViolation(_, _))
        ));
    }

    #[test]
    fn a_pack_declaring_a_core_only_key_is_a_meta_schema_violation() {
        let mut pack: serde_json::Value =
            serde_json::from_str(MERGE_BASE_PACK).expect("fixture must parse");
        pack["mechanisms"] = serde_json::json!({});
        let result = Profile::from_packs(EMBEDDED_CORE_JSON, &[&pack.to_string()]);
        assert!(matches!(
            result,
            Err(ProfileError::MetaSchemaViolation(_, _))
        ));
    }

    #[test]
    fn a_pack_extending_a_different_core_version_is_a_version_skew_error() {
        let mut pack: serde_json::Value =
            serde_json::from_str(MERGE_BASE_PACK).expect("fixture must parse");
        pack["extends"] = serde_json::json!("core@99");
        let result = Profile::from_packs(EMBEDDED_CORE_JSON, &[&pack.to_string()]);
        assert!(matches!(
            result,
            Err(ProfileError::VersionSkew { extends, core, .. })
                if extends == "core@99" && core == "core@1"
        ));
    }

    #[test]
    fn a_namespace_parent_dangling_after_merge_is_an_integrity_violation() {
        let mut pack: serde_json::Value =
            serde_json::from_str(MERGE_BASE_PACK).expect("fixture must parse");
        pack["namespaces"][1]["parents"] = serde_json::json!(["ghost-parent"]);
        let result = Profile::from_packs(EMBEDDED_CORE_JSON, &[&pack.to_string()]);
        assert!(matches!(
            result,
            Err(ProfileError::IntegrityViolation { ref key, .. }) if key == "ghost-parent"
        ));
    }

    #[test]
    fn removing_the_description_cap_backing_the_default_file_class_is_an_integrity_violation() {
        // `MERGE_BASE_PACK`'s `file_class.default` is "context"; removing
        // its description cap leaves that default dangling only once every
        // layer has merged -- exactly the case `check_integrity` exists for.
        let removing_pack = r#"{
            "kind": "extension-pack",
            "version": "removes-default-cap@1",
            "extends": "core@1",
            "removes": ["description_cap:context"],
            "required_fields": [],
            "description_caps": {},
            "file_class": {"default": "context", "rules": []},
            "namespaces": [],
            "report": {
                "trigger": {"namespace": "type", "value": "report"},
                "required_namespaces": [],
                "period": {"namespace": "period", "regex": "^.*$"}
            },
            "exempt": {"filenames": [], "dir_components": [], "path_globs": []}
        }"#;
        let result = Profile::from_packs(EMBEDDED_CORE_JSON, &[MERGE_BASE_PACK, removing_pack]);
        assert!(matches!(
            result,
            Err(ProfileError::IntegrityViolation { ref key, .. }) if key == "context"
        ));
    }
}

/// SDET verification for `M2.P3.T3` (`Profile::from_packs` layered merge).
/// Targets acceptance points the tests above don't cover: the 5 dimensions
/// the golden 3-layer test leaves untouched (`FileClassDefault`/
/// `FileClassRule`/`ReportTrigger`/`ReportPeriod`/`ReportRequired`) plus the
/// 3 exempt dimensions, the exact fixed layer->dimension->key warning order
/// across a realistic multi-dimension merge, `removes`-applied-after-
/// additive ordering within one layer, a dangling reference introduced two
/// layers after its target was defined, determinism as a property (not
/// just one golden comparison), a cross-process determinism check
/// targeting the HashMap->Map fix's per-process-seed failure mode directly,
/// and two reported (not fixed) meta-schema-adequacy gaps.
#[cfg(test)]
mod sdet_merge_verification {
    use super::*;

    // -----------------------------------------------------------------
    // Full-dimension-coverage + fixed-warning-order probe
    // -----------------------------------------------------------------

    const BASE: &str = r#"{
        "kind": "extension-pack",
        "version": "base@1",
        "extends": "core@1",
        "required_fields": [{"field": "name", "authorship": "human_authored"}],
        "description_caps": {"context": 100},
        "file_class": {"default": "context", "rules": [{"class": "skill", "match": {"glob": "**/*.skill.md"}}]},
        "namespaces": [
            {"name": "type", "cardinality": "singleton"},
            {"name": "period", "cardinality": "optional", "type": "date"}
        ],
        "report": {
            "trigger": {"namespace": "type", "value": "report"},
            "required_namespaces": ["type"],
            "period": {"namespace": "period", "regex": "^.*$"}
        },
        "exempt": {
            "filenames": ["README.md"],
            "dir_components": ["node_modules"],
            "path_globs": ["**/vendor/**"]
        }
    }"#;

    const EXT: &str = r#"{
        "kind": "extension-pack",
        "version": "ext@1",
        "extends": "core@1",
        "required_fields": [{"field": "name", "authorship": "machine_derivable"}],
        "description_caps": {"agent": 200},
        "file_class": {
            "default": "agent",
            "rules": [
                {"class": "skill", "match": {"glob": "**/*.SKILL.md"}},
                {"class": "agent", "match": {"glob": "**/*.agent.md"}}
            ]
        },
        "namespaces": [
            {"name": "type", "cardinality": "singleton"}
        ],
        "report": {
            "trigger": {"namespace": "type", "value": "REPORT"},
            "required_namespaces": ["type", "period"],
            "period": {"namespace": "period", "regex": "^[0-9]+$"}
        },
        "exempt": {
            "filenames": ["README.md", "CHANGELOG.md"],
            "dir_components": ["node_modules", "dist"],
            "path_globs": ["**/vendor/**"]
        }
    }"#;

    const THIRD: &str = r#"{
        "kind": "extension-pack",
        "version": "third@1",
        "extends": "core@1",
        "removes": [
            "file_class_rule:agent",
            "report_required:period",
            "exempt_filename:CHANGELOG.md",
            "exempt_dir:dist",
            "exempt_glob:no-such-glob",
            "exempt_filename:README.md"
        ],
        "required_fields": [],
        "description_caps": {},
        "file_class": {"default": "agent", "rules": []},
        "namespaces": [],
        "report": {
            "trigger": {"namespace": "type", "value": "REPORT"},
            "required_namespaces": [],
            "period": {"namespace": "period", "regex": "^[0-9]+$"}
        },
        "exempt": {"filenames": [], "dir_components": [], "path_globs": []}
    }"#;

    /// The exact warning sequence `BASE`/`EXT`/`THIRD` must produce. Covers
    /// override for every dimension the golden 3-layer test above leaves
    /// untouched (`FileClassDefault`, `FileClassRule`, `ReportTrigger`,
    /// `ReportPeriod`), additive+identical-noop+removal for all 3 exempt
    /// dimensions (which structurally can never produce an Override -- their
    /// key IS their value), and an override for `RequiredField` (the golden
    /// test only exercises its removal). The removal order in `THIRD` is
    /// deliberately NOT dimension order (it interleaves `file_class_rule`,
    /// `report_required`, `exempt_filename`, `exempt_dir`, `exempt_glob`,
    /// then `exempt_filename` again) -- proving removals emit in the
    /// layer's own declared `removes` ARRAY order, not re-sorted into
    /// dimension order.
    fn expected_warnings() -> Vec<MergeWarning> {
        vec![
            MergeWarning::Override {
                dimension: Dimension::RequiredField,
                key: "name".to_string(),
                from_layer: "base@1".to_string(),
                to_layer: "ext@1".to_string(),
                base_layer: true,
            },
            MergeWarning::Override {
                dimension: Dimension::FileClassDefault,
                key: "default".to_string(),
                from_layer: "base@1".to_string(),
                to_layer: "ext@1".to_string(),
                base_layer: true,
            },
            MergeWarning::Override {
                dimension: Dimension::FileClassRule,
                key: "skill".to_string(),
                from_layer: "base@1".to_string(),
                to_layer: "ext@1".to_string(),
                base_layer: true,
            },
            MergeWarning::Override {
                dimension: Dimension::ReportTrigger,
                key: "trigger".to_string(),
                from_layer: "base@1".to_string(),
                to_layer: "ext@1".to_string(),
                base_layer: true,
            },
            MergeWarning::Override {
                dimension: Dimension::ReportPeriod,
                key: "period".to_string(),
                from_layer: "base@1".to_string(),
                to_layer: "ext@1".to_string(),
                base_layer: true,
            },
            MergeWarning::Removal {
                dimension: Dimension::FileClassRule,
                key: "agent".to_string(),
                removing_layer: "third@1".to_string(),
                removed_from_layer: Some("ext@1".to_string()),
                base_layer: false,
            },
            MergeWarning::Removal {
                dimension: Dimension::ReportRequired,
                key: "period".to_string(),
                removing_layer: "third@1".to_string(),
                removed_from_layer: Some("ext@1".to_string()),
                base_layer: false,
            },
            MergeWarning::Removal {
                dimension: Dimension::ExemptFilename,
                key: "CHANGELOG.md".to_string(),
                removing_layer: "third@1".to_string(),
                removed_from_layer: Some("ext@1".to_string()),
                base_layer: false,
            },
            MergeWarning::Removal {
                dimension: Dimension::ExemptDir,
                key: "dist".to_string(),
                removing_layer: "third@1".to_string(),
                removed_from_layer: Some("ext@1".to_string()),
                base_layer: false,
            },
            MergeWarning::Removal {
                dimension: Dimension::ExemptGlob,
                key: "no-such-glob".to_string(),
                removing_layer: "third@1".to_string(),
                removed_from_layer: None,
                base_layer: false,
            },
            MergeWarning::Removal {
                dimension: Dimension::ExemptFilename,
                key: "README.md".to_string(),
                removing_layer: "third@1".to_string(),
                removed_from_layer: Some("base@1".to_string()),
                base_layer: true,
            },
        ]
    }

    #[test]
    fn full_dimension_coverage_and_fixed_warning_order() {
        let (profile, warnings) = Profile::from_packs(EMBEDDED_CORE_JSON, &[BASE, EXT, THIRD])
            .expect("3 consistent layers merge");

        assert_eq!(
            warnings,
            expected_warnings(),
            "warning sequence must match the fixed layer->dimension->key order, \
             including THIRD's own declared (non-dimension-sorted) removes order"
        );

        assert_eq!(profile.pack.file_class.default, "agent");
        assert_eq!(
            profile
                .pack
                .file_class
                .rules
                .iter()
                .map(|r| r.class.clone())
                .collect::<Vec<_>>(),
            vec!["skill"],
            "ext's additive 'agent' rule was removed by third; ext's override of \
             'skill' survives"
        );
        assert_eq!(profile.pack.report.trigger.value, "REPORT");
        assert_eq!(profile.pack.report.period.regex, "^[0-9]+$");
        assert_eq!(
            profile.pack.report.required_namespaces,
            vec!["type".to_string()],
            "base's 'type' survives (ext restated it identically -- a no-op, \
             not a removal); ext's additive 'period' was removed by third"
        );
        assert!(
            profile.pack.exempt.filenames.is_empty(),
            "both README.md (base) and CHANGELOG.md (ext) were removed by third"
        );
        assert_eq!(profile.pack.exempt.dir_components, vec!["node_modules"]);
        assert_eq!(profile.pack.exempt.path_globs, vec!["**/vendor/**"]);
    }

    // -----------------------------------------------------------------
    // Removal-ordering: a layer that removes-then-re-adds ITS OWN key
    // -----------------------------------------------------------------

    /// A pack that both additively declares a namespace AND removes it, in
    /// the same layer. Per `Profile::from_packs`' documented contract ("a
    /// layer's `removes` directives apply after its own additive/override
    /// entries"), the correct final state is ABSENT -- if the order were
    /// reversed (remove first, then add), the final state would be PRESENT
    /// instead. Direct differential test for that ordering clause.
    const ADDS_AND_REMOVES_OWN_KEY: &str = r#"{
        "kind": "extension-pack",
        "version": "self-remove@1",
        "extends": "core@1",
        "removes": ["namespace:temp"],
        "required_fields": [{"field": "name", "authorship": "human_authored"}],
        "description_caps": {"context": 10},
        "file_class": {"default": "context", "rules": []},
        "namespaces": [
            {"name": "type", "cardinality": "singleton"},
            {"name": "period", "cardinality": "optional"},
            {"name": "temp", "cardinality": "optional"}
        ],
        "report": {
            "trigger": {"namespace": "type", "value": "report"},
            "required_namespaces": [],
            "period": {"namespace": "period", "regex": "^.*$"}
        },
        "exempt": {"filenames": [], "dir_components": [], "path_globs": []}
    }"#;

    #[test]
    fn a_layer_removing_a_key_it_just_additively_declared_ends_absent() {
        let (profile, warnings) =
            Profile::from_packs(EMBEDDED_CORE_JSON, &[ADDS_AND_REMOVES_OWN_KEY])
                .expect("a single self-consistent layer must merge cleanly");

        assert!(
            !profile.pack.namespaces.iter().any(|ns| ns.name == "temp"),
            "additive-then-remove within one layer must leave 'temp' absent -- \
             if removal were applied BEFORE the additive merge (wrong order), \
             'temp' would end up present instead, since the additive add would \
             run after a no-op removal of a not-yet-existing key"
        );
        assert_eq!(
            warnings,
            vec![MergeWarning::Removal {
                dimension: Dimension::Namespace,
                key: "temp".to_string(),
                removing_layer: "self-remove@1".to_string(),
                removed_from_layer: Some("self-remove@1".to_string()),
                base_layer: true,
            }],
            "the removal warning must name THIS layer as both remover and \
             definer -- proof the additive add ran and was then undone by this \
             same layer's own removes, not a no-op removal of something absent"
        );
    }

    // -----------------------------------------------------------------
    // Dangling reference introduced two layers after its target's
    // definition
    // -----------------------------------------------------------------

    const LEAF_LAYER: &str = r#"{
        "kind": "extension-pack",
        "version": "leaf@1",
        "extends": "core@1",
        "required_fields": [],
        "description_caps": {"context": 10},
        "file_class": {"default": "context", "rules": []},
        "namespaces": [
            {"name": "type", "cardinality": "singleton"},
            {"name": "period", "cardinality": "optional"},
            {"name": "leaf", "cardinality": "optional"}
        ],
        "report": {
            "trigger": {"namespace": "type", "value": "report"},
            "required_namespaces": [],
            "period": {"namespace": "period", "regex": "^.*$"}
        },
        "exempt": {"filenames": [], "dir_components": [], "path_globs": []}
    }"#;

    const CHILD_LAYER: &str = r#"{
        "kind": "extension-pack",
        "version": "child@1",
        "extends": "core@1",
        "required_fields": [],
        "description_caps": {},
        "file_class": {"default": "context", "rules": []},
        "namespaces": [
            {"name": "child", "cardinality": "optional", "parents": ["leaf"]}
        ],
        "report": {
            "trigger": {"namespace": "type", "value": "report"},
            "required_namespaces": [],
            "period": {"namespace": "period", "regex": "^.*$"}
        },
        "exempt": {"filenames": [], "dir_components": [], "path_globs": []}
    }"#;

    const REMOVE_LEAF_LAYER: &str = r#"{
        "kind": "extension-pack",
        "version": "remover@1",
        "extends": "core@1",
        "removes": ["namespace:leaf"],
        "required_fields": [],
        "description_caps": {},
        "file_class": {"default": "context", "rules": []},
        "namespaces": [],
        "report": {
            "trigger": {"namespace": "type", "value": "report"},
            "required_namespaces": [],
            "period": {"namespace": "period", "regex": "^.*$"}
        },
        "exempt": {"filenames": [], "dir_components": [], "path_globs": []}
    }"#;

    #[test]
    fn removing_a_namespace_two_layers_after_a_dependent_parent_reference_is_defined_fails_closed()
    {
        // leaf@1 defines 'leaf'; child@1 (one layer later) defines 'child'
        // with parents: ['leaf'] -- valid at that point; remover@1 (two
        // layers after 'leaf' was defined) removes 'leaf' itself, leaving
        // 'child's parent reference dangling only once every layer has
        // merged. Integrity is checked ONCE at the end, against the final
        // state, never per-layer -- this proves that.
        let result = Profile::from_packs(
            EMBEDDED_CORE_JSON,
            &[LEAF_LAYER, CHILD_LAYER, REMOVE_LEAF_LAYER],
        );
        match result {
            Err(ProfileError::IntegrityViolation { key, layers, .. }) => {
                assert_eq!(key, "leaf");
                assert!(
                    !layers.is_empty(),
                    "the violation must name the layer(s) implicated"
                );
            }
            other => {
                panic!("expected IntegrityViolation on dangling parent 'leaf', got {other:?}")
            }
        }
    }

    #[test]
    fn the_same_two_layers_without_the_removal_merge_cleanly() {
        // Control: leaf+child alone (no removal) must NOT be an integrity
        // violation -- isolates that the violation above is caused
        // specifically by the removal, not by some other defect in the
        // parents chain.
        let result = Profile::from_packs(EMBEDDED_CORE_JSON, &[LEAF_LAYER, CHILD_LAYER]);
        assert!(
            result.is_ok(),
            "leaf+child without removal must merge cleanly: {result:?}"
        );
    }

    // -----------------------------------------------------------------
    // DescriptionCaps ordering probe (the HashMap->Map determinism fix)
    // -----------------------------------------------------------------

    /// `DescriptionCaps` is backed by `serde_json::Map` without the
    /// `preserve_order` feature enabled -- i.e. a `BTreeMap`, alphabetically
    /// ordered by key, not insertion order. Pins that the merge's
    /// `description_caps` dimension iterates (and warns) in ALPHABETICAL key
    /// order regardless of the packs' own declared JSON key order, and that
    /// this holds identically across repeated calls (no per-process
    /// `HashMap` seed randomization affecting it).
    const CAPS_UNSORTED_BASE: &str = r#"{
        "kind": "extension-pack",
        "version": "caps-base@1",
        "extends": "core@1",
        "required_fields": [],
        "description_caps": {"zzz-class": 900, "aaa-class": 100, "mmm-class": 500},
        "file_class": {"default": "aaa-class", "rules": []},
        "namespaces": [
            {"name": "type", "cardinality": "singleton"},
            {"name": "period", "cardinality": "optional"}
        ],
        "report": {
            "trigger": {"namespace": "type", "value": "report"},
            "required_namespaces": [],
            "period": {"namespace": "period", "regex": "^.*$"}
        },
        "exempt": {"filenames": [], "dir_components": [], "path_globs": []}
    }"#;

    const CAPS_OVERRIDE_LAYER: &str = r#"{
        "kind": "extension-pack",
        "version": "caps-override@1",
        "extends": "core@1",
        "required_fields": [],
        "description_caps": {"mmm-class": 501, "bbb-class": 50},
        "file_class": {"default": "aaa-class", "rules": []},
        "namespaces": [],
        "report": {
            "trigger": {"namespace": "type", "value": "report"},
            "required_namespaces": [],
            "period": {"namespace": "period", "regex": "^.*$"}
        },
        "exempt": {"filenames": [], "dir_components": [], "path_globs": []}
    }"#;

    #[test]
    fn description_caps_merge_and_warning_order_is_alphabetical_not_declaration_order() {
        let (profile, warnings) = Profile::from_packs(
            EMBEDDED_CORE_JSON,
            &[CAPS_UNSORTED_BASE, CAPS_OVERRIDE_LAYER],
        )
        .expect("merge must succeed");

        assert_eq!(
            profile.pack.description_caps.caps_entries(),
            vec![
                ("aaa-class".to_string(), 100),
                ("bbb-class".to_string(), 50),
                ("mmm-class".to_string(), 501),
                ("zzz-class".to_string(), 900),
            ],
            "merged description_caps must iterate in alphabetical (BTreeMap) \
             key order, not the base pack's declared zzz/aaa/mmm order nor the \
             override layer's mmm/bbb order"
        );
        assert_eq!(
            warnings,
            vec![MergeWarning::Override {
                dimension: Dimension::DescriptionCap,
                key: "mmm-class".to_string(),
                from_layer: "caps-base@1".to_string(),
                to_layer: "caps-override@1".to_string(),
                base_layer: true,
            }],
            "only mmm-class genuinely changed value (500->501); zzz/aaa are \
             untouched (no warning) and bbb-class is purely additive (no \
             warning) -- the single warning that does fire is keyed correctly \
             regardless of alphabetical vs declared order"
        );

        // Stability across repeated in-process calls.
        for _ in 0..20 {
            let (repeat, repeat_warnings) = Profile::from_packs(
                EMBEDDED_CORE_JSON,
                &[CAPS_UNSORTED_BASE, CAPS_OVERRIDE_LAYER],
            )
            .expect("merge must succeed");
            assert_eq!(
                repeat.pack.description_caps.caps_entries(),
                profile.pack.description_caps.caps_entries()
            );
            assert_eq!(repeat_warnings, warnings);
        }
    }

    // -----------------------------------------------------------------
    // Determinism as a property: synthetic interleaved packs, N repeats,
    // fixed-order byte-identity, and order-change-is-deterministic.
    // -----------------------------------------------------------------

    /// Builds one synthetic pack layer with a namespace/required-field/
    /// description-cap footprint parameterized by `seed`, so a caller can
    /// build several layers whose additive/override/removal relationships
    /// interleave across dimensions in a way no single hand-picked fixture
    /// would (some `seed`s redefine an earlier seed's key with a DIFFERENT
    /// value -- a genuine override -- others restate it identically, others
    /// introduce a brand new key, and every third layer removes a
    /// low-numbered key, exercising all three merge outcomes against a
    /// moving target).
    fn synthetic_pack(seed: usize) -> String {
        let ns_a = format!("ns{}", seed % 4);
        let ns_b = format!("ns{}", (seed + 1) % 4);
        let cardinality = if seed.is_multiple_of(2) {
            "optional"
        } else {
            "singleton"
        };
        let cap_value = 50 + (seed * 7) % 200;
        let removes = if seed >= 3 && seed.is_multiple_of(3) {
            format!(r#""removes": ["namespace:ns{}"],"#, (seed - 3) % 4)
        } else {
            String::new()
        };
        format!(
            r#"{{
                "kind": "extension-pack",
                "version": "syn{seed}@1",
                "extends": "core@1",
                {removes}
                "required_fields": [{{"field": "name", "authorship": "human_authored"}}],
                "description_caps": {{"context": {cap_value}}},
                "file_class": {{"default": "context", "rules": []}},
                "namespaces": [
                    {{"name": "type", "cardinality": "singleton"}},
                    {{"name": "period", "cardinality": "optional"}},
                    {{"name": "{ns_a}", "cardinality": "{cardinality}"}},
                    {{"name": "{ns_b}", "cardinality": "optional"}}
                ],
                "report": {{
                    "trigger": {{"namespace": "type", "value": "report"}},
                    "required_namespaces": [],
                    "period": {{"namespace": "period", "regex": "^.*$"}}
                }},
                "exempt": {{"filenames": [], "dir_components": [], "path_globs": []}}
            }}"#
        )
    }

    #[test]
    fn synthetic_multi_layer_merge_is_deterministic_across_many_repeats() {
        let packs: Vec<String> = (0..12).map(synthetic_pack).collect();
        let pack_refs: Vec<&str> = packs.iter().map(String::as_str).collect();

        let (first_profile, first_warnings) = Profile::from_packs(EMBEDDED_CORE_JSON, &pack_refs)
            .expect("synthetic layers must merge cleanly");
        let first_repr = format!("{first_profile:?}");

        for _ in 0..50 {
            let (profile, warnings) = Profile::from_packs(EMBEDDED_CORE_JSON, &pack_refs)
                .expect("synthetic layers must merge cleanly");
            assert_eq!(format!("{profile:?}"), first_repr, "repeat run diverged");
            assert_eq!(warnings, first_warnings, "warning-vec repeat run diverged");
        }
    }

    #[test]
    fn synthetic_multi_layer_merge_reordering_changes_result_deterministically() {
        let packs: Vec<String> = (0..8).map(synthetic_pack).collect();
        let forward: Vec<&str> = packs.iter().map(String::as_str).collect();
        let mut reversed = forward.clone();
        reversed.reverse();

        let (forward_profile, _) = Profile::from_packs(EMBEDDED_CORE_JSON, &forward)
            .expect("forward order must merge cleanly");
        let (reversed_profile, _) = match Profile::from_packs(EMBEDDED_CORE_JSON, &reversed) {
            Ok(result) => result,
            Err(err) => panic!(
                "reversed order must also merge cleanly (a removal in the \
                 synthetic set referencing a namespace defined earlier in \
                 forward order can land AFTER its target in reversed order, \
                 which is a legitimate no-op removal, not a hard error): {err:?}"
            ),
        };

        let forward_repr = format!("{forward_profile:?}");
        let reversed_repr = format!("{reversed_profile:?}");
        assert_ne!(
            forward_repr, reversed_repr,
            "reordering 8 interleaved-override layers must change the result -- \
             if this ever spuriously matches, widen the synthetic set's override \
             density before treating it as a real finding"
        );

        // And each order is independently deterministic on repetition.
        let (forward_again, _) = Profile::from_packs(EMBEDDED_CORE_JSON, &forward)
            .expect("forward order must merge cleanly");
        assert_eq!(format!("{forward_again:?}"), forward_repr);
    }

    // -----------------------------------------------------------------
    // Cross-process determinism -- targets the HashMap->Map fix's specific
    // failure mode: per-process hash-seed randomization, which repeat
    // calls WITHIN one process can never reveal (the seed is fixed for the
    // process's lifetime). Spawns this same test binary 3x, filtered to a
    // helper test that prints the merge result to stdout, and diffs the 3
    // captures.
    // -----------------------------------------------------------------

    const CROSS_PROCESS_PRINT_MARKER: &str = "CROSS_PROCESS_MERGE_RESULT: ";

    /// Not asserted directly by `cargo test` in the normal run except to
    /// print and pass -- its real job is to be invoked as a CHILD process by
    /// `cross_process_determinism_matches_across_separate_invocations`,
    /// which greps this marker out of the child's captured stdout.
    #[test]
    fn helper_prints_cross_process_merge_result_and_passes() {
        let packs: Vec<String> = (0..10).map(synthetic_pack).collect();
        let pack_refs: Vec<&str> = packs.iter().map(String::as_str).collect();
        let (profile, warnings) = Profile::from_packs(EMBEDDED_CORE_JSON, &pack_refs)
            .expect("synthetic layers must merge cleanly");
        println!("{CROSS_PROCESS_PRINT_MARKER}{profile:?}|||{warnings:?}");
    }

    #[test]
    fn cross_process_determinism_matches_across_separate_invocations() {
        let exe = std::env::current_exe().expect("test binary path must be resolvable");
        let mut captures = Vec::new();
        for _ in 0..3 {
            let output = std::process::Command::new(&exe)
                .args([
                    "--exact",
                    "profile::sdet_merge_verification::helper_prints_cross_process_merge_result_and_passes",
                    "--nocapture",
                ])
                .output()
                .expect("spawning this same test binary as a child process must succeed");
            assert!(
                output.status.success(),
                "child invocation must pass: stdout={} stderr={}",
                String::from_utf8_lossy(&output.stdout),
                String::from_utf8_lossy(&output.stderr)
            );
            let stdout = String::from_utf8_lossy(&output.stdout).into_owned();
            let line = stdout
                .lines()
                .find(|l| l.contains(CROSS_PROCESS_PRINT_MARKER))
                .unwrap_or_else(|| panic!("child stdout missing marker; got: {stdout}"))
                .to_string();
            captures.push(line);
        }
        assert_eq!(
            captures[0], captures[1],
            "merge result diverged across separate process invocations (run 1 vs 2) -- \
             this is exactly the per-process HashMap-iteration-order failure mode the \
             HashMap->Map fix targets"
        );
        assert_eq!(
            captures[1], captures[2],
            "merge result diverged across separate process invocations (run 2 vs 3)"
        );
    }

    // -----------------------------------------------------------------
    // Hard errors: malformed `removes` reference and malformed `extends`,
    // in the multi-layer `from_packs` path specifically (the single-pack
    // path is already covered by the `tests` module above).
    // -----------------------------------------------------------------

    #[test]
    fn a_second_layer_with_a_malformed_removes_entry_is_a_meta_schema_violation() {
        let malformed_second_layer = r#"{
            "kind": "extension-pack",
            "version": "bad@1",
            "extends": "core@1",
            "removes": ["not-a-typed-reference"],
            "required_fields": [],
            "description_caps": {},
            "file_class": {"default": "context", "rules": []},
            "namespaces": [],
            "report": {
                "trigger": {"namespace": "type", "value": "report"},
                "required_namespaces": [],
                "period": {"namespace": "period", "regex": "^.*$"}
            },
            "exempt": {"filenames": [], "dir_components": [], "path_globs": []}
        }"#;
        let result = Profile::from_packs(EMBEDDED_CORE_JSON, &[BASE, malformed_second_layer]);
        assert!(
            matches!(result, Err(ProfileError::MetaSchemaViolation(ref pack_id, _)) if pack_id == "bad@1"),
            "expected MetaSchemaViolation naming the SECOND layer, got {result:?}"
        );
    }

    #[test]
    fn a_second_layer_with_a_malformed_extends_is_a_meta_schema_violation_not_version_skew() {
        // "core1" (no '@') fails VERSIONED_NAME_PATTERN before the
        // extends==core.version comparison ever runs -- MetaSchemaViolation,
        // not VersionSkew.
        let malformed_extends = r#"{
            "kind": "extension-pack",
            "version": "bad@1",
            "extends": "core1",
            "required_fields": [],
            "description_caps": {},
            "file_class": {"default": "context", "rules": []},
            "namespaces": [],
            "report": {
                "trigger": {"namespace": "type", "value": "report"},
                "required_namespaces": [],
                "period": {"namespace": "period", "regex": "^.*$"}
            },
            "exempt": {"filenames": [], "dir_components": [], "path_globs": []}
        }"#;
        let result = Profile::from_packs(EMBEDDED_CORE_JSON, &[BASE, malformed_extends]);
        assert!(
            matches!(result, Err(ProfileError::MetaSchemaViolation(_, _))),
            "expected MetaSchemaViolation for a malformed extends string, got {result:?}"
        );
    }

    // -----------------------------------------------------------------
    // Meta-schema-validation adequacy probe (SDET finding for the
    // operator's no-JSON-Schema-crate sign-off -- see the delivery
    // report, section 7).
    // -----------------------------------------------------------------

    /// FINDING (reported, not fixed): the checked-in meta-schema
    /// (`frontmatter-profile.meta.schema.json`) declares `required_fields`'
    /// `minItems: 1` -- a pack must require at least one field. Neither
    /// `validate_pack_structure` nor `Pack`'s serde derive enforces this: an
    /// empty `required_fields` array deserializes and merges cleanly. A real
    /// JSON-Schema-2020-12 validator run against the ALREADY-CHECKED-IN
    /// meta-schema would reject this pack; the current targeted-regex-plus-
    /// serde approach does not.
    #[test]
    fn finding_empty_required_fields_array_is_accepted_despite_meta_schemas_minitems_one() {
        let empty_required_fields_pack = r#"{
            "kind": "extension-pack",
            "version": "empty-required@1",
            "extends": "core@1",
            "required_fields": [],
            "description_caps": {"context": 10},
            "file_class": {"default": "context", "rules": []},
            "namespaces": [
                {"name": "type", "cardinality": "singleton"},
                {"name": "period", "cardinality": "optional"}
            ],
            "report": {
                "trigger": {"namespace": "type", "value": "report"},
                "required_namespaces": [],
                "period": {"namespace": "period", "regex": "^.*$"}
            },
            "exempt": {"filenames": [], "dir_components": [], "path_globs": []}
        }"#;
        let result = Profile::from_packs(EMBEDDED_CORE_JSON, &[empty_required_fields_pack]);
        assert!(
            result.is_ok(),
            "documents the gap: this pack has zero required_fields (meta-schema \
             says minItems: 1) and still merges without error: {result:?}"
        );
    }

    /// FINDING (reported, not fixed): an unrecognized/misspelled top-level
    /// pack key is silently dropped by serde's default `#[serde(flatten)]`
    /// behavior (no `deny_unknown_fields` -- and it is not combinable with
    /// `flatten` anyway). A pack author who typos a dimension name (e.g.
    /// `"namespcaes"` instead of `"namespaces"`) gets no error at all; the
    /// field they meant to set is silently absent from the merge. NOTE: the
    /// checked-in meta-schema itself does not set `additionalProperties:
    /// false` on `extensionPack` either, so a JSON-Schema-2020-12 validator
    /// run against THIS SPECIFIC meta-schema would not catch it either --
    /// this is a meta-schema permissiveness gap, not a tooling-choice gap.
    /// Recorded for the operator's sign-off either way, since it's the same
    /// class of "malformed pack slips through" concern the mandate asks
    /// about.
    #[test]
    fn finding_a_misspelled_top_level_key_is_silently_dropped_not_rejected() {
        let mut typoed_pack: serde_json::Value =
            serde_json::from_str(BASE).expect("BASE fixture must parse");
        let real_namespaces = typoed_pack["namespaces"].take();
        typoed_pack["namespacess"] = real_namespaces; // typo: extra 's'
        typoed_pack["namespaces"] = serde_json::json!([]);
        // A pack with `namespaces: []` and everything else from BASE fails
        // integrity (report.trigger references 'type', now absent) --
        // proving the typo really did drop the intended namespaces rather
        // than coincidentally still working. Confirm THAT failure mode
        // specifically, not just "some error happened."
        let result = Profile::from_packs(EMBEDDED_CORE_JSON, &[&typoed_pack.to_string()]);
        assert!(
            matches!(result, Err(ProfileError::IntegrityViolation { ref key, .. }) if key == "type"),
            "expected the typo to silently drop 'namespaces' (surfacing later as \
             an unrelated-looking IntegrityViolation on 'type', not a clear \
             'unrecognized key' error), got {result:?}"
        );
    }

    // -----------------------------------------------------------------
    // Purity: from_packs never reads the filesystem, takes strings only.
    // -----------------------------------------------------------------

    #[test]
    fn from_packs_signature_is_pure_strings_in_result_out() {
        // Compile-time proof by construction: this call only ever passes
        // `&str` and reads back a `Result` -- if `from_packs` took a path or
        // performed I/O internally, that would be a `src/profile.rs`
        // implementation concern (verified separately by `grep`), but the
        // PUBLIC CONTRACT itself is pinned here: no path-shaped parameter
        // exists to accidentally pass a filesystem path into.
        let _: Result<(Profile, Vec<MergeWarning>), ProfileError> =
            Profile::from_packs(EMBEDDED_CORE_JSON, &[BASE]);
    }
}
