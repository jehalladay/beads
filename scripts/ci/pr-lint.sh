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
ci_time "golangci-lint" -- \
    golangci-lint run --timeout=5m --build-tags=gms_pure_go ./...

# RESOURCE-SAFETY ratchet (beads-r06.4, Mayor ruling Option 2): block NEW
# violations of the deferred linter classes (sqlclosecheck/contextcheck/
# staticcheck-SA) without failing on the pre-existing baseline (burned down
# under beads-yzo). Runs new-from-merge-base; needs full history (fetch-depth:0).
"$SCRIPT_DIR/pr-lint-ratchet.sh"
