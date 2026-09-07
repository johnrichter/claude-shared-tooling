"""Enforce SC-VERSIONING and the no-relative-path-dependency rule.

version-guard checks that a module's tag prefix equals its module path, and
that the Rust workspace declares no relative-path dependency.
"""
