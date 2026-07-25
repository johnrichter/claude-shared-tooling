"""Structural CLI-surface differential for go/build-helpers.

Captures the same prose-free structural surface from two builds of the binary and diffs them:
argv flag grammar, stdin handling, stdout JSON field names/types/presence, and exit codes. Free
text (messages, reasons, errors, flag and help descriptions) never enters the comparison.
"""
