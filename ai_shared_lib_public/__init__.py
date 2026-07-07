"""ai_shared_lib_public — public shared tools for AI-agent workspaces.

Dependency policy: stdlib is preferred for portability, but a justified third-party
dependency is permitted when vendored via the standard mechanism (subject to OSS-license
clearance). Every module here is stdlib-only in fact today — no third-party runtime
dependency, so no network install is needed to import or run. Consumed by path dependency
in dev and by git-tag dependency when shipped.
"""

__version__ = "0.2.0"
