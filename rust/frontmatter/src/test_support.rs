//! Test-only synthetic pack, shared across this crate's `#[cfg(test)]`
//! modules (`validate`, `fix`, `query`) via `crate::test_support::...`. A
//! self-authored extension pack -- not a real shipped bundle -- so the
//! crate's own tests exercise generic schema mechanics without coupling to
//! any one named pack's vocabulary. Its namespace/rule-set/exempt shape
//! mirrors a typical adopting pack's (same cardinalities, parent chains, a
//! `type:report` conditional rule-set with a date-interval value-format)
//! precisely so the existing test suite's tag literals (`type:report`,
//! `topic:apm`, `period:...`, etc.) keep exercising the same cascade paths.

use crate::profile::Profile;

/// A synthetic extension pack -- versioned distinctly from any embedded
/// bundle, so a test built against it can never be confused with an
/// artifact-acceptance test against a real shipped pack.
pub(crate) const SYNTHETIC_PACK_JSON: &str = r#"{
  "kind": "extension-pack",
  "profile": "synthetic-test",
  "version": "synthetic-test@1",
  "extends": "core@2.0.0",
  "description": "Self-authored test fixture pack -- generic vocabulary for this crate's own test suite, not a shipped bundle.",
  "required_fields": [
    { "field": "name", "authorship": "human_authored", "source": "test fixture" },
    { "field": "description", "authorship": "human_authored", "source": "test fixture" },
    { "field": "id", "authorship": "human_authored", "source": "test fixture" },
    { "field": "tags", "authorship": "human_authored", "source": "test fixture" },
    { "field": "links", "authorship": "human_authored", "source": "test fixture" },
    { "field": "updated", "authorship": "machine_derivable", "source": "test fixture" }
  ],
  "description_caps": {
    "context": 350,
    "skill": 500,
    "agent": 750,
    "source": "test fixture"
  },
  "file_class": {
    "default": "context",
    "rules": [
      { "class": "skill", "match": { "glob": "*.claude/skills/*/SKILL.md" }, "source": "test fixture" },
      { "class": "skill", "match": { "glob": "**/SKILL.md" }, "source": "test fixture" },
      { "class": "agent", "match": { "glob": ".claude/agents/*.md" }, "source": "test fixture" }
    ],
    "note": "test fixture"
  },
  "namespaces": [
    { "name": "type", "cardinality": "singleton", "source": "test fixture" },
    { "name": "status", "cardinality": "singleton", "source": "test fixture" },
    { "name": "privacy", "cardinality": "singleton", "source": "test fixture" },
    { "name": "owner", "cardinality": "singleton", "source": "test fixture" },
    { "name": "topic", "cardinality": "at_least_one", "source": "test fixture" },
    { "name": "lead", "cardinality": "at_most_one", "source": "test fixture" },
    { "name": "feature", "cardinality": "optional", "parents": ["product", "suite"], "source": "test fixture" },
    { "name": "product", "cardinality": "optional", "parents": ["suite"], "source": "test fixture" },
    { "name": "suite", "cardinality": "optional", "source": "test fixture" },
    { "name": "source", "cardinality": "optional", "source": "test fixture" },
    { "name": "period", "cardinality": "optional", "type": "date_interval", "source": "test fixture" },
    { "name": "audience", "cardinality": "optional", "source": "test fixture" },
    { "name": "cadence", "cardinality": "optional", "source": "test fixture" },
    { "name": "team", "cardinality": "optional", "source": "test fixture" }
  ],
  "rule_sets": [
    {
      "match": { "namespace": "type", "value": "report" },
      "apply": {
        "require_namespaces": ["source", "period"],
        "forbidden_unless_matched": ["source", "period", "audience", "cadence"],
        "value_formats": [
          { "namespace": "period", "regex": "^[0-9]{4}-[0-9]{2}-[0-9]{2}/[0-9]{4}-[0-9]{2}-[0-9]{2}$", "message": "'{value}' is not YYYY-MM-DD/YYYY-MM-DD" }
        ]
      },
      "source": "test fixture"
    }
  ],
  "exempt": {
    "filenames": ["plan.md", "execution.md", "CLAUDE.md"],
    "dir_components": [".pytest_cache", "__pycache__", ".git", "node_modules"],
    "path_globs": [
      "work/daily-briefing/20*.md",
      "work/daily-briefing/archive/20*.md",
      "work/daily-briefing/archive/manual-priorities-resolved.md",
      "work/projects/*/findings/*.md",
      "work/workspace/scratchpad.md"
    ],
    "source": "test fixture"
  }
}"#;

/// Builds a `Profile` from the embedded core plus [`SYNTHETIC_PACK_JSON`] --
/// the shared fixture behind `validate`/`fix`/`query`'s own `#[cfg(test)]`
/// `profile()` helpers.
///
/// # Panics
/// Never: `SYNTHETIC_PACK_JSON` is a committed literal in this file,
/// checked by this crate's own test suite.
pub(crate) fn synthetic_profile() -> Profile {
    Profile::from_pack_json(SYNTHETIC_PACK_JSON)
        .expect("SYNTHETIC_PACK_JSON must deserialize into a valid Profile")
}
