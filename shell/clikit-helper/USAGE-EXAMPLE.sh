#!/bin/bash
# USAGE-EXAMPLE.sh — practical examples of using clikit-emit from shell surfaces.
#
# These examples show the canonical way to use the clikit shell helper
# instead of hand-writing JSON. Each example is a shell surface calling clikit-emit.
#
# To run: once a clikit emitter binary is built and available, execute this script.
# Before then, the examples are illustrative only.

set -euo pipefail

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly CLIKIT_BIN="${CLIKIT_BIN:-$SCRIPT_DIR/../../go/.bin/clikit}"

# Example 1: Emit a success record with data payload.
# A command that completed successfully with results.
example_success() {
  echo "=== Example 1: Success with data ==="
  "$SCRIPT_DIR/clikit-emit" \
    --command git-tools,worktree,create \
    --status success \
    --exit-code 0 \
    --data '{"path":"/workspace/feature-1"}' || true
  echo ""
}

# Example 2: Emit a conflict error with a retry directive.
# A command that found the subject in an incompatible state.
example_conflict_with_retry() {
  echo "=== Example 2: Conflict with retry directive ==="
  "$SCRIPT_DIR/clikit-emit" \
    --command git-tools,worktree,create \
    --status conflict \
    --exit-code 41 \
    --error conflict.worktree.branch_checked_out "branch 'feat/x' is already checked out at '/w/a'" \
    --error-context branch feat/x \
    --error-context worktree /w/a \
    --error-triage kind reinvoke \
    --error-triage command git-tools,worktree,create,--branch,feat/x-2 || true
  echo ""
}

# Example 3: Emit a precondition error with manual instruction.
# A command that cannot run because required state is missing.
example_precondition_with_manual_fix() {
  echo "=== Example 3: Precondition unmet with manual instruction ==="
  "$SCRIPT_DIR/clikit-emit" \
    --command language-tools,build \
    --status precondition_unmet \
    --exit-code 30 \
    --error precondition.build.no_module "module configuration not found" \
    --error-context directory /home/user/project \
    --error-triage kind manual \
    --error-triage instruction "Run 'go mod init github.com/user/project' to create a module" || true
  echo ""
}

# Example 4: Emit a usage error (bad invocation).
# A command that was invoked incorrectly.
example_usage_error() {
  echo "=== Example 4: Usage error ==="
  "$SCRIPT_DIR/clikit-emit" \
    --command git-tools,worktree,delete \
    --status usage \
    --exit-code 50 \
    --error usage.git.missing_path "WORKTREE_PATH argument is required" || true
  echo ""
}

# Example 5: Emit a not-found error.
# A command looking for something that does not exist.
example_not_found() {
  echo "=== Example 5: Not found ==="
  "$SCRIPT_DIR/clikit-emit" \
    --command git-tools,worktree,delete \
    --status not_found \
    --exit-code 40 \
    --error not_found.worktree.missing "worktree not found" \
    --error-context path /workspace/missing || true
  echo ""
}

# Example 6: Emit a transient error with backoff.
# A command that failed due to a transient condition.
example_transient_with_backoff() {
  echo "=== Example 6: Transient error with backoff ==="
  "$SCRIPT_DIR/clikit-emit" \
    --command language-tools,download \
    --status transient \
    --exit-code 60 \
    --error transient.download.connection_timeout "request timed out after 30 seconds" \
    --error-triage kind reinvoke \
    --error-triage after_seconds 5 || true
  echo ""
}

# Example 7: Emit a caveats record (success with qualifications).
# A command that succeeded but with non-blocking issues.
example_caveats() {
  echo "=== Example 7: Success with caveats ==="
  "$SCRIPT_DIR/clikit-emit" \
    --command language-tools,build \
    --status caveats \
    --exit-code 10 \
    --data '{"artifacts":["./bin/tool"]}' \
    --caveat caveats.build.deprecated_dependency "dependency 'x' is deprecated and will be removed in v2.0.0" \
    --caveat-triage kind manual \
    --caveat-triage instruction "Consider updating to 'y' before v2.0.0 release" || true
  echo ""
}

# Run all examples.
main() {
  if [[ ! -x "$SCRIPT_DIR/clikit-emit" ]]; then
    echo "error: clikit-emit not found at $SCRIPT_DIR/clikit-emit" >&2
    exit 1
  fi

  # Verify clikit emitter binary exists before running examples.
  if [[ ! -x "$CLIKIT_BIN" ]]; then
    echo "Note: clikit emitter binary not found at $CLIKIT_BIN" >&2
    echo "These examples are runnable once the clikit emitter is built." >&2
    echo "To run: build go/clikit (or rust/clikit) and place or symlink the binary" >&2
    echo "        to $CLIKIT_BIN, then execute this script." >&2
    exit 0
  fi

  example_success
  example_conflict_with_retry
  example_precondition_with_manual_fix
  example_usage_error
  example_not_found
  example_transient_with_backoff
  example_caveats
}

main "$@"
