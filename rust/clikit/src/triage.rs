//! [`Triage`]: the fix directive every diagnostic ends in, per
//! `result-record.schema.json`'s `$defs/triage` and its three closed kinds.

use serde::Serialize;

use crate::error::ClikitError;
use crate::validate;

/// What the caller does next about a diagnostic. Exactly one kind, and the
/// kind fixes which fields are present — the Rust enum makes an
/// out-of-shape combination (a `manual` with a `command`, a `reinvoke` with
/// no `command`) unrepresentable rather than merely schema-invalid.
///
/// Internally tagged on `kind` so the wire form matches the schema exactly:
/// `{"kind":"reinvoke","command":[...]}`, not a nested `{"reinvoke":{...}}`.
#[derive(Debug, Clone, PartialEq, Serialize)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum Triage {
    /// Run this same CLI again, this way. `command[0]` is this tool.
    Reinvoke {
        /// argv: element 0 is this tool, every other element one already
        /// unquoted argument.
        command: Vec<String>,
        /// Wait this long before retrying. Only meaningful for a transient
        /// failure; absent means retry immediately.
        #[serde(skip_serializing_if = "Option::is_none")]
        after_seconds: Option<u32>,
        /// Why this command fixes the diagnostic, when it isn't obvious
        /// from the diagnostic's own message.
        #[serde(skip_serializing_if = "Option::is_none")]
        instruction: Option<String>,
    },
    /// Run a different executable, this way — the sanctioned exit from the
    /// CLI-before-raw-OS-tool routing rule.
    RunTool {
        /// argv: element 0 is another executable, not this tool.
        command: Vec<String>,
        /// Why this command fixes the diagnostic, when it isn't obvious
        /// from the diagnostic's own message.
        #[serde(skip_serializing_if = "Option::is_none")]
        instruction: Option<String>,
    },
    /// No invocation fixes this; a person must act.
    Manual {
        /// What a person must do, as one line.
        instruction: String,
    },
}

impl Triage {
    /// A `reinvoke` of `command` (argv, element 0 is this tool).
    #[must_use]
    pub fn reinvoke<I, S>(command: I) -> Self
    where
        I: IntoIterator<Item = S>,
        S: Into<String>,
    {
        Triage::Reinvoke {
            command: command.into_iter().map(Into::into).collect(),
            after_seconds: None,
            instruction: None,
        }
    }

    /// A `run_tool` handoff to `command` (argv, element 0 is not this tool).
    #[must_use]
    pub fn run_tool<I, S>(command: I) -> Self
    where
        I: IntoIterator<Item = S>,
        S: Into<String>,
    {
        Triage::RunTool {
            command: command.into_iter().map(Into::into).collect(),
            instruction: None,
        }
    }

    /// A `manual` directive: no invocation fixes this.
    #[must_use]
    pub fn manual(instruction: impl Into<String>) -> Self {
        Triage::Manual {
            instruction: instruction.into(),
        }
    }

    /// Sets the retry floor on a [`Triage::Reinvoke`]. A no-op on
    /// [`Triage::RunTool`] or [`Triage::Manual`], which the schema forbids
    /// it on.
    #[must_use]
    pub fn after_seconds(mut self, seconds: u32) -> Self {
        if let Triage::Reinvoke { after_seconds, .. } = &mut self {
            *after_seconds = Some(seconds);
        }
        self
    }

    /// Attaches an `instruction` to a [`Triage::Reinvoke`] or
    /// [`Triage::RunTool`]. A no-op on [`Triage::Manual`], which already
    /// carries its instruction at construction.
    #[must_use]
    pub fn instruction(mut self, text: impl Into<String>) -> Self {
        match &mut self {
            Triage::Reinvoke { instruction, .. } | Triage::RunTool { instruction, .. } => {
                *instruction = Some(text.into());
            }
            Triage::Manual { .. } => {}
        }
        self
    }

    /// The wire `kind` token.
    #[must_use]
    pub const fn kind(&self) -> &'static str {
        match self {
            Triage::Reinvoke { .. } => "reinvoke",
            Triage::RunTool { .. } => "run_tool",
            Triage::Manual { .. } => "manual",
        }
    }

    pub(crate) fn validate(&self) -> Result<(), ClikitError> {
        match self {
            Triage::Reinvoke {
                command,
                after_seconds,
                instruction,
            } => {
                validate_command(command)?;
                if let Some(instruction) = instruction {
                    validate::validate_line("triage.instruction", instruction, 4096)?;
                }
                if let Some(seconds) = after_seconds {
                    if *seconds > 86400 {
                        return Err(ClikitError::InvalidValue {
                            field: "triage.after_seconds",
                            reason: "must be at most 86400",
                            value: seconds.to_string(),
                        });
                    }
                }
                Ok(())
            }
            Triage::RunTool {
                command,
                instruction,
            } => {
                validate_command(command)?;
                if let Some(instruction) = instruction {
                    validate::validate_line("triage.instruction", instruction, 4096)?;
                }
                Ok(())
            }
            Triage::Manual { instruction } => {
                validate::validate_line("triage.instruction", instruction, 4096)
            }
        }
    }
}

fn validate_command(command: &[String]) -> Result<(), ClikitError> {
    if command.is_empty() || command.len() > 128 {
        return Err(ClikitError::InvalidValue {
            field: "triage.command",
            reason: "must carry 1-128 elements",
            value: format!("{} elements", command.len()),
        });
    }
    for token in command {
        validate::validate_line("triage.command element", token, 4096)?;
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn kind_matches_the_schema_wire_token() {
        assert_eq!(Triage::reinvoke(["x"]).kind(), "reinvoke");
        assert_eq!(Triage::run_tool(["x"]).kind(), "run_tool");
        assert_eq!(Triage::manual("do it").kind(), "manual");
    }

    #[test]
    fn after_seconds_is_a_no_op_off_reinvoke() {
        let triage = Triage::manual("do it").after_seconds(30);
        assert_eq!(triage, Triage::manual("do it"));
    }

    #[test]
    fn empty_command_is_rejected() {
        let empty: Vec<String> = vec![];
        assert!(Triage::reinvoke(empty).validate().is_err());
    }
}
