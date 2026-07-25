# Post-M0 structural differential

- pre-M0 artifact: `dfe52b23aa5d38fe9cd23051c650e06a5125eda9`
- subcommands covered: 29
- probes per binary: 93
- verdict: 9 structural divergence(s) across 2 subcommand(s): alternation_changed x1, exit_code_changed x2, flag_added x1, flag_requiredness_changed x1, stdout_field_added x1, stdout_field_removed x2, stdout_format_changed x1

## Divergences

| subcommand | kind | where | pre-M0 | post-M0 |
| --- | --- | --- | --- | --- |
| `record` | exit_code_changed | record/done-without-commit | `0` | `2` |
| `record` | stdout_format_changed | record/done-without-commit | `json` | `empty` |
| `self-check` | flag_added | --band | `absent` | `{'type': 'string', 'default': ''}` |
| `self-check` | flag_requiredness_changed | required flags | `['ceiling-effort', 'ceiling-model', 'floor-effort', 'floor-model']` | `['band', 'ceiling-effort', 'ceiling-model', 'floor-effort', 'floor-model']` |
| `self-check` | alternation_changed | mutually exclusive flag groups | `[]` | `[[['ceiling-effort', 'ceiling-model', 'floor-effort', 'floor-model'], ['band']]]` |
| `self-check` | exit_code_changed | self-check/cross-family-band | `0` | `3` |
| `self-check` | stdout_field_removed | self-check/cross-family-band: warnings | `array` | `absent` |
| `self-check` | stdout_field_removed | self-check/cross-family-band: warnings[] | `string` | `absent` |
| `self-check` | stdout_field_added | self-check/cross-family-band: roster_stale | `absent` | `boolean` |

