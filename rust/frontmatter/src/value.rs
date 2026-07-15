//! The parser's own, backend-agnostic value representation for top-level
//! frontmatter fields, plus the insertion-ordered map that holds them.
//!
//! [`crate::parse`] is the only module that ever sees a `yaml-rust2` `Yaml`
//! value — everything downstream of it (this module, [`crate::ParsedFrontmatter`],
//! and any future consumer such as the `validate` module) only ever sees
//! [`FrontmatterValue`]. That indirection is deliberate: it lets a future
//! change swap the YAML backend without touching any consumer's code.

/// One top-level frontmatter field's value, typed just enough for a
/// validator to tell "is this a scalar / a list / something else" apart —
/// no richer than that on purpose, so no YAML-library type ever leaks
/// through this enum.
///
/// Scalar values are preserved verbatim as their **string form** — never
/// coerced to a bool/int/float. This matters for two reasons: (1) it keeps
/// the parser type-agnostic (a caller decides what "the right type" means
/// for a given field, this crate doesn't decide for them), and (2)
/// `yaml-rust2` is strict YAML 1.2, so an unquoted `updated:` timestamp or a
/// bare `yes`/`no` already comes back from the YAML backend as a string,
/// not a bool/timestamp — this enum simply keeps that "it's a string" fact
/// visible rather than re-typing it.
///
/// `Serialize`/`Deserialize` are derived so a consumer can persist a parsed
/// document (e.g. navigator's freshness cache) and read it back losslessly.
/// `Deserialize` here is for round-tripping a cache's own previously-written
/// output, not a second entry point for parsing untrusted frontmatter --
/// [`crate::parse`] remains the only path for that.
#[derive(Debug, Clone, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
pub enum FrontmatterValue {
    /// A single value with no children — string, quoted or bare; integer;
    /// float; bool; or an explicit YAML null — always represented as its
    /// string form (a YAML null becomes `Other`, see that variant).
    Scalar(String),
    /// A YAML block or flow sequence (`tags:` / `links:` style `- item`
    /// lists). Elements are assumed scalar (true for every real frontmatter
    /// field in this workspace); a non-scalar element (nested list/mapping)
    /// is rendered via a best-effort debug string rather than dropped or
    /// panicking, since this crate's job is to preserve shape for the
    /// validator, not to reject content.
    Sequence(Vec<String>),
    /// A nested YAML mapping, preserved recursively (a `Mapping`'s own
    /// values can themselves be `Scalar`/`Sequence`/`Mapping`/`Other`).
    /// Needed because the workspace frontmatter schema nests
    /// Skill/Agent/Rule-specific required fields under a top-level
    /// `workspace:` key that a validator must be able to descend into.
    /// This crate only *preserves* the nested map's shape — it does not
    /// flatten `workspace:` up into the top level or otherwise interpret
    /// the nesting; that semantic is the validator's job.
    Mapping(RawFields),
    /// Anything that is neither a plain scalar, a sequence, nor a mapping:
    /// an explicit YAML null (`key:` with nothing after it), a YAML alias,
    /// or a value the YAML backend could not resolve. The validator is
    /// expected to reject most of these for frontmatter fields, which is
    /// why this crate does not try to interpret them further.
    Other,
}

/// An insertion-ordered `key -> value` map for a frontmatter document's
/// top-level fields.
///
/// Backed by a `Vec<(String, FrontmatterValue)>` rather than a hashing map
/// on purpose: this crate's determinism contract requires `raw_fields` to
/// replay keys in the exact order they appeared in the source YAML, and a
/// plain `Vec` makes that guarantee trivial to see by inspection (no reliance
/// on a third-party ordered-map crate's iteration-order contract). Lookups
/// are O(n) in field count, which is fine — frontmatter blocks have a
/// handful of keys, never enough for this to matter.
///
/// `Serialize`/`Deserialize` are derived for the same cache-round-trip
/// reason as [`FrontmatterValue`]'s -- see that type's doc comment. The
/// derived impl serializes the backing `Vec` as a sequence, so insertion
/// order survives a round trip (see `value_serde_tests` below).
#[derive(Debug, Clone, PartialEq, Eq, Default, serde::Serialize, serde::Deserialize)]
pub struct RawFields(Vec<(String, FrontmatterValue)>);

impl RawFields {
    /// Builds a `RawFields` from an already-ordered list of `(key, value)`
    /// pairs. Callers (currently only [`crate::parse`]) are responsible for
    /// supplying pairs in source order; this constructor does not
    /// deduplicate or reorder.
    #[must_use]
    pub fn from_ordered_pairs(pairs: Vec<(String, FrontmatterValue)>) -> Self {
        Self(pairs)
    }

    /// Looks up a key's value. Returns `None` when the key is absent —
    /// never panics, regardless of `key`.
    #[must_use]
    pub fn get(&self, key: &str) -> Option<&FrontmatterValue> {
        self.0.iter().find(|(k, _)| k == key).map(|(_, v)| v)
    }

    /// Whether `key` is present as a top-level field, regardless of its value.
    #[must_use]
    pub fn contains_key(&self, key: &str) -> bool {
        self.0.iter().any(|(k, _)| k == key)
    }

    /// Number of top-level fields.
    #[must_use]
    pub fn len(&self) -> usize {
        self.0.len()
    }

    /// Whether there are no top-level fields (missing or empty frontmatter).
    #[must_use]
    pub fn is_empty(&self) -> bool {
        self.0.is_empty()
    }

    /// Iterates `(key, value)` pairs in source insertion order.
    pub fn iter(&self) -> impl Iterator<Item = (&str, &FrontmatterValue)> {
        self.0.iter().map(|(k, v)| (k.as_str(), v))
    }
}

impl<'a> IntoIterator for &'a RawFields {
    type Item = (&'a str, &'a FrontmatterValue);
    type IntoIter = Box<dyn Iterator<Item = (&'a str, &'a FrontmatterValue)> + 'a>;

    fn into_iter(self) -> Self::IntoIter {
        Box::new(self.iter())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn get_finds_present_key_and_none_for_absent() {
        let raw = RawFields::from_ordered_pairs(vec![(
            "name".to_string(),
            FrontmatterValue::Scalar("x".to_string()),
        )]);
        assert_eq!(
            raw.get("name"),
            Some(&FrontmatterValue::Scalar("x".to_string()))
        );
        assert_eq!(raw.get("missing"), None);
    }

    #[test]
    fn iter_preserves_insertion_order() {
        let raw = RawFields::from_ordered_pairs(vec![
            ("b".to_string(), FrontmatterValue::Scalar("2".to_string())),
            ("a".to_string(), FrontmatterValue::Scalar("1".to_string())),
        ]);
        let keys: Vec<&str> = raw.iter().map(|(k, _)| k).collect();
        assert_eq!(
            keys,
            vec!["b", "a"],
            "must replay source order, not sort keys"
        );
    }

    #[test]
    fn empty_raw_fields_reports_empty() {
        let raw = RawFields::default();
        assert!(raw.is_empty());
        assert_eq!(raw.len(), 0);
        assert!(!raw.contains_key("anything"));
    }
}

#[cfg(test)]
mod value_serde_tests {
    use super::*;

    #[test]
    fn scalar_round_trips() {
        let value = FrontmatterValue::Scalar("x".to_string());
        let json = serde_json::to_string(&value).unwrap();
        assert_eq!(
            serde_json::from_str::<FrontmatterValue>(&json).unwrap(),
            value
        );
    }

    #[test]
    fn sequence_round_trips() {
        let value = FrontmatterValue::Sequence(vec!["a".to_string(), "b".to_string()]);
        let json = serde_json::to_string(&value).unwrap();
        assert_eq!(
            serde_json::from_str::<FrontmatterValue>(&json).unwrap(),
            value
        );
    }

    #[test]
    fn other_round_trips() {
        let value = FrontmatterValue::Other;
        let json = serde_json::to_string(&value).unwrap();
        assert_eq!(
            serde_json::from_str::<FrontmatterValue>(&json).unwrap(),
            value
        );
    }

    #[test]
    fn nested_mapping_round_trips_recursively() {
        // A Mapping whose own value is another Mapping -- proves the
        // derived impl recurses through FrontmatterValue's own variants,
        // not just one level.
        let inner = RawFields::from_ordered_pairs(vec![(
            "leaf".to_string(),
            FrontmatterValue::Scalar("v".to_string()),
        )]);
        let value = FrontmatterValue::Mapping(RawFields::from_ordered_pairs(vec![
            ("nested".to_string(), FrontmatterValue::Mapping(inner)),
            (
                "tags".to_string(),
                FrontmatterValue::Sequence(vec!["t1".to_string()]),
            ),
        ]));
        let json = serde_json::to_string(&value).unwrap();
        assert_eq!(
            serde_json::from_str::<FrontmatterValue>(&json).unwrap(),
            value
        );
    }

    #[test]
    fn raw_fields_round_trip_preserves_insertion_order() {
        let raw = RawFields::from_ordered_pairs(vec![
            ("z".to_string(), FrontmatterValue::Scalar("1".to_string())),
            ("a".to_string(), FrontmatterValue::Scalar("2".to_string())),
        ]);
        let json = serde_json::to_string(&raw).unwrap();
        let restored: RawFields = serde_json::from_str(&json).unwrap();
        assert_eq!(restored, raw);
        assert_eq!(
            restored.iter().map(|(k, _)| k).collect::<Vec<_>>(),
            vec!["z", "a"],
            "deserialized RawFields must replay the same key order, not resort"
        );
    }
}
