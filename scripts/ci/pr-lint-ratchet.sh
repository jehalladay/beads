#!/usr/bin/env bash
# RESOURCE-SAFETY ratchet gate (beads-r06.4, Mayor ruling Option 2).
#
# Blocks only NEW violations of the three linters that carry a pre-existing
# baseline (sqlclosecheck / contextcheck / staticcheck-SA) — see
# .golangci-ratchet.yml. Pre-existing sites do NOT fail; they are burned down
# under beads-yzo (eng_5). This keeps the required full-tree gate (pr-lint.sh)
# at zero-tolerance for bodyclose+rowserrcheck+the rest while still preventing
# regressions in the deferred classes.
#
# Mechanism: golangci-lint --new-from-merge-base compares against the best
# common ancestor with origin/main, so only issues touched by this branch's
# diff are reported. Requires full git history (CI checks out fetch-depth: 0).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# shellcheck source=../.buildflags
source "$REPO_ROOT/.buildflags"
# shellcheck source=lib/timing.sh
source "$REPO_ROOT/scripts/ci/lib/timing.sh"

cd "$REPO_ROOT"

# Base ref to diff against. Overridable for local runs; defaults to origin/main.
RATCHET_BASE="${RATCHET_BASE:-origin/main}"

# --new-from-merge-base needs the base ref present in the local object store.
# CI checks out fetch-depth:0, so origin/main resolves; guard for shallow/local.
if ! git rev-parse --verify --quiet "$RATCHET_BASE" >/dev/null; then
    echo "ratchet: base ref '$RATCHET_BASE' not found; fetching..." >&2
    git fetch --no-tags --quiet origin main || true
fi

if ! git rev-parse --verify --quiet "$RATCHET_BASE" >/dev/null; then
    echo "ratchet: WARNING base ref '$RATCHET_BASE' still unavailable; skipping ratchet (cannot compute new-only diff safely)." >&2
    exit 0
fi

ci_time "golangci-lint ratchet (new violations only)" -- \
    golangci-lint run \
        --config "$REPO_ROOT/.golangci-ratchet.yml" \
        --new-from-merge-base "$RATCHET_BASE" \
        --timeout=5m \
        --build-tags=gms_pure_go \
        ./...
