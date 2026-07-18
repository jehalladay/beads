#!/usr/bin/env bash
# Required PR formatting and Go lint contract.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# shellcheck source=../.buildflags
source "$REPO_ROOT/.buildflags"
# shellcheck source=lib/timing.sh
source "$REPO_ROOT/scripts/ci/lib/timing.sh"

cd "$REPO_ROOT"

ci_time "gofmt check" -- make fmt-check
# --allow-serial-runners: golangci-lint holds a global /tmp/golangci-lint.lock;
# on shared /fsx CI/crew nodes a concurrent run would otherwise fail this step
# with "parallel golangci-lint is running" instead of waiting (beads-ub3).
#
# BEADS_CI_STEP_TIMEOUT (beads-0lu9): golangci's own --timeout=5m is its ANALYSIS
# budget and does NOT fire when the process is HUNG at 0% CPU (blocked on the
# shared flock / disk under /fsx contention) — which wedged the refinery gate
# 50min+ on 2026-07-18. Bound it with an OS-level timeout a bit above the 5m
# analysis budget so a genuine hang is killed (exit 124 → gate fails cleanly +
# refinery re-queues) while a normal slow run still completes.
BEADS_CI_STEP_TIMEOUT="${BEADS_CI_LINT_TIMEOUT:-480}" \
ci_time "golangci-lint" -- \
    golangci-lint run --timeout=5m --allow-serial-runners --build-tags=gms_pure_go ./...

# RESOURCE-SAFETY ratchet (beads-r06.4, Mayor ruling Option 2): block NEW
# violations of the deferred linter classes (sqlclosecheck/contextcheck/
# staticcheck-SA) without failing on the pre-existing baseline (burned down
# under beads-yzo). Runs new-from-merge-base; needs full history (fetch-depth:0).
"$SCRIPT_DIR/pr-lint-ratchet.sh"
