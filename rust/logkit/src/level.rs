//! The closed level set and the normalization procedure that admits a
//! foreign token into it.
//!
//! Everything here follows `schemas/logkit/logkit-logging.spec.md` ("Levels")
//! and `schemas/logkit/logkit.contract.json` (`level_normalization`).

use std::fmt;

use serde::{Serialize, Serializer};

/// One of the five canonical severities, lowest first.
///
/// This is the Rust house pattern's closed enum: a value of this type is
/// unconditionally a known level, because the type has no other inhabitant.
/// [`Level::known`] exists anyway as the spec's `Known()` predicate, so
/// every implementation exposes the same name for "this level is one the
/// system recognizes" even though Rust's type system makes the answer
/// trivial here (unlike a string-backed level, which can hold an unknown
/// value and needs the check to mean something).
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum Level {
    /// Developer detail for reproducing behavior. Off in a normal run.
    Debug,
    /// An expected milestone worth a permanent record.
    Info,
    /// A degradation that was handled — the run continues correctly.
    Warn,
    /// An operation failed; the run may continue with a reduced result.
    Error,
    /// The process cannot continue. The last record it writes.
    Fatal,
}

impl Level {
    /// The canonical, lowercase wire token — the record's own `level` value.
    #[must_use]
    pub const fn as_str(self) -> &'static str {
        match self {
            Level::Debug => "debug",
            Level::Info => "info",
            Level::Warn => "warn",
            Level::Error => "error",
            Level::Fatal => "fatal",
        }
    }

    /// The severity ordinal from `log-record.schema.json`'s `$defs/severity`.
    /// Never serialized; exists so a threshold comparison is
    /// `severity(level) >= severity(threshold)` for every implementation alike.
    #[must_use]
    pub const fn severity(self) -> u16 {
        match self {
            Level::Debug => 10,
            Level::Info => 20,
            Level::Warn => 30,
            Level::Error => 40,
            Level::Fatal => 50,
        }
    }

    /// Always `true`: a `Level` value is one of the five canonical members by
    /// construction. See the type's own doc comment for why the predicate
    /// still exists.
    #[must_use]
    pub const fn known(self) -> bool {
        true
    }
}

impl fmt::Display for Level {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(self.as_str())
    }
}

impl Serialize for Level {
    fn serialize<S: Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        serializer.serialize_str(self.as_str())
    }
}

/// A token that failed level normalization: absent from the alias map after
/// trim + ASCII-lowercase, or a foreign concept normalization refuses to
/// guess at (`off`, `disabled`, a raw number, ...).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct UnknownLevel {
    /// The offending token, exactly as received.
    pub token: String,
}

impl fmt::Display for UnknownLevel {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "unknown log level {:?}", self.token)
    }
}

impl std::error::Error for UnknownLevel {}

/// The result of normalizing a foreign level token: the canonical [`Level`]
/// it maps to, plus the source token when the mapping was **not** an
/// equivalence (`trace` -> `debug`, `panic` -> `fatal`) — the caller carries
/// that into the record's reserved `fields.native_level` key, per
/// `logkit.contract.json`'s `level_normalization.lossy_token_field`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct NormalizedLevel {
    /// The canonical level the source token normalized to.
    pub level: Level,
    /// The source token, kept only when the mapping was not an equivalence.
    pub native_level: Option<String>,
}

/// Runs the contract's normalization procedure on a token from outside the
/// process (a `--log-level` flag, an env var, a foreign record): trim, then
/// ASCII-only lowercase (never a locale-sensitive one — see the contract's
/// own note on why a Turkish locale would break `INFO` -> `info`), then a
/// lookup in the alias map. A token the map doesn't recognize is
/// [`UnknownLevel`], never a default.
///
/// # Errors
/// Returns [`UnknownLevel`] naming the offending token when it is absent
/// from the alias map (including numeric scales and writer-configuration
/// words like `off`/`disabled`, which are not levels at all).
pub fn normalize(token: &str) -> Result<NormalizedLevel, UnknownLevel> {
    let trimmed = token.trim();
    let lowered = ascii_lowercase(trimmed);

    let (level, equivalent) = match lowered.as_str() {
        "trace" => (Level::Debug, false),
        "debug" => (Level::Debug, true),
        "info" => (Level::Info, true),
        "warn" | "warning" => (Level::Warn, true),
        "error" => (Level::Error, true),
        "fatal" | "critical" => (Level::Fatal, true),
        "panic" => (Level::Fatal, false),
        _ => {
            return Err(UnknownLevel {
                token: token.to_string(),
            })
        }
    };

    Ok(NormalizedLevel {
        level,
        native_level: (!equivalent).then(|| trimmed.to_string()),
    })
}

/// ASCII-only lowercase: folds `A`-`Z` and leaves every other byte, including
/// non-ASCII UTF-8, untouched. `str::to_lowercase` is locale-independent but
/// still Unicode-aware in a way the contract explicitly rules out (its
/// example: a Turkish-locale lowercase maps `I` to dotless `ı`, not `i`);
/// this only ever touches the 26 ASCII letters.
fn ascii_lowercase(s: &str) -> String {
    s.chars()
        .map(|c| {
            if c.is_ascii() {
                c.to_ascii_lowercase()
            } else {
                c
            }
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn severities_are_spaced_by_ten_in_level_order() {
        let levels = [
            Level::Debug,
            Level::Info,
            Level::Warn,
            Level::Error,
            Level::Fatal,
        ];
        for pair in levels.windows(2) {
            assert_eq!(pair[1].severity() - pair[0].severity(), 10);
        }
    }

    #[test]
    fn equivalent_aliases_round_trip_with_no_native_level() {
        for (token, level) in [
            ("info", Level::Info),
            ("WARN", Level::Warn),
            ("warning", Level::Warn),
            ("Error", Level::Error),
            ("fatal", Level::Fatal),
            ("critical", Level::Fatal),
            ("  debug  ", Level::Debug),
        ] {
            let normalized = normalize(token).unwrap();
            assert_eq!(normalized.level, level);
            assert_eq!(normalized.native_level, None);
        }
    }

    #[test]
    fn lossy_aliases_preserve_the_source_token() {
        let trace = normalize("trace").unwrap();
        assert_eq!(trace.level, Level::Debug);
        assert_eq!(trace.native_level.as_deref(), Some("trace"));

        let panic = normalize("PANIC").unwrap();
        assert_eq!(panic.level, Level::Fatal);
        assert_eq!(panic.native_level.as_deref(), Some("PANIC"));
    }

    #[test]
    fn unrecognized_tokens_fail_by_name_never_default() {
        for token in ["off", "disabled", "notset", "none", "5", ""] {
            let err = normalize(token).unwrap_err();
            assert_eq!(err.token, token);
        }
    }

    #[test]
    fn turkish_dotted_i_does_not_fold_like_a_locale_lowercase_would() {
        // A locale-sensitive lowercase under tr_TR maps 'I' to dotless 'ı',
        // which would make this token miss "info" entirely. ASCII-only
        // lowercase must still fold it to "info".
        assert_eq!(normalize("INFO").unwrap().level, Level::Info);
    }
}
