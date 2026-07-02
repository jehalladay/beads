#!/usr/bin/env bash
# Ratcheting coverage floor (beads-r06.12, C3).
#
# Enforces a committed minimum total test-coverage percentage that can only go
# UP. CI fails when measured coverage drops below the floor in `.coverage-floor`;
# the floor is raised (never lowered) via `--bump` once coverage improves. This
# replaces the static 30%/40% threshold in the nightly workflow with a monotonic
# ratchet, so coverage is protected against silent regression while still letting
# genuine improvements lock in a higher bar.
#
# Usage:
#   coverage-ratchet.sh --total <pct>          # check a pre-computed total
#   coverage-ratchet.sh --profile <cover.out>  # compute total from a Go profile
#   coverage-ratchet.sh ... --bump             # also raise the floor on improvement
#   coverage-ratchet.sh ... --floor-file <path># override .coverage-floor location
#
# Exit codes: 0 = at/above floor; 1 = below floor (regression) or usage/IO error.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

FLOOR_FILE="$REPO_ROOT/.coverage-floor"
TOTAL=""
PROFILE=""
BUMP=0

die() { echo "coverage-ratchet: $*" >&2; exit 1; }

while [[ $# -gt 0 ]]; do
    case "$1" in
        --total)      TOTAL="${2:-}"; shift 2 ;;
        --profile)    PROFILE="${2:-}"; shift 2 ;;
        --floor-file) FLOOR_FILE="${2:-}"; shift 2 ;;
        --bump)       BUMP=1; shift ;;
        -h|--help)
            sed -n '2,20p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
            exit 0 ;;
        *) die "unknown argument: $1" ;;
    esac
done

# --- Resolve the measured total coverage percentage ---------------------------
if [[ -n "$PROFILE" ]]; then
    [[ -n "$TOTAL" ]] && die "pass only one of --total / --profile"
    [[ -f "$PROFILE" ]] || die "coverage profile not found: $PROFILE"
    # Compute the statement-weighted total directly from the profile. Each data
    # line is `file:startLine.col,endLine.col numStmts count`; a block counts as
    # covered when count>0. This matches `go tool cover -func` total: but needs
    # no source files present (robust in a detached CI checkout / on fixtures).
    TOTAL="$(awk '
        NR==1 && $1=="mode:" {next}
        NF>=3 {
            stmts=$(NF-1); cnt=$NF;
            total+=stmts; if (cnt+0>0) covered+=stmts;
        }
        END {
            if (total==0) { print ""; exit }
            printf "%.1f", (covered/total)*100;
        }' "$PROFILE")"
    [[ -n "$TOTAL" ]] || die "could not derive total coverage from $PROFILE (no statements?)"
fi

[[ -n "$TOTAL" ]] || die "provide --total <pct> or --profile <cover.out>"
# Normalize a possible trailing '%'.
TOTAL="${TOTAL%\%}"

# --- Read the floor (fail closed if absent/malformed) -------------------------
[[ -f "$FLOOR_FILE" ]] || die "floor file not found: $FLOOR_FILE (fail-closed; a floor must be committed)"
FLOOR="$(tr -d '[:space:]' < "$FLOOR_FILE")"
[[ -n "$FLOOR" ]] || die "floor file is empty: $FLOOR_FILE"

# Validate both are numbers.
awk -v v="$TOTAL" 'BEGIN{ if (v+0 != v && v !~ /^[0-9]+(\.[0-9]+)?$/) exit 1 }' \
    || die "measured coverage is not numeric: '$TOTAL'"
awk -v v="$FLOOR" 'BEGIN{ if (v+0 != v && v !~ /^[0-9]+(\.[0-9]+)?$/) exit 1 }' \
    || die "floor is not numeric: '$FLOOR'"

# --- Compare (awk float comparison; no bc dependency) -------------------------
below="$(awk -v t="$TOTAL" -v f="$FLOOR" 'BEGIN{ print (t+0 < f+0) ? 1 : 0 }')"

if [[ "$below" == "1" ]]; then
    echo "❌ coverage ${TOTAL}% is below the floor ${FLOOR}% — regression blocked." >&2
    echo "   (floor: $FLOOR_FILE)" >&2
    exit 1
fi

echo "✅ coverage ${TOTAL}% meets the floor ${FLOOR}%." >&2

# --- Ratchet up on improvement ------------------------------------------------
if [[ "$BUMP" == "1" ]]; then
    higher="$(awk -v t="$TOTAL" -v f="$FLOOR" 'BEGIN{ print (t+0 > f+0) ? 1 : 0 }')"
    if [[ "$higher" == "1" ]]; then
        printf '%s\n' "$TOTAL" > "$FLOOR_FILE"
        echo "⬆️  ratcheted floor ${FLOOR}% → ${TOTAL}% (${FLOOR_FILE})." >&2
    else
        echo "   floor unchanged (${FLOOR}%); coverage did not improve." >&2
    fi
fi

exit 0
