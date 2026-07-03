#!/usr/bin/env bash
# L10 release-audit gate (beads-nsa, HALF 2 of beads-r06.12).
#
# Automates the 6-criterion checklist every release-gates/*.md file in this repo
# already asserts BY HAND into a scripted, CI-enforceable audit. It verifies the
# three MECHANICAL criteria directly (git + the release test suite) and asserts
# the three RECORDED criteria are present on the feature/review bead, then emits
# a machine-readable PASS/FAIL and generates a release-gates/<bead>-gate.md stub.
#
#   1. Review PASS present         (recorded)  — a "Review … PASS" line on the bead
#   2. Acceptance criteria met     (recorded)  — an "Acceptance criteria met" line
#   3. Tests pass on release branch (mechanical) — --test-cmd exits 0
#   4. No HIGH-severity findings open (recorded) — no open-HIGH-finding line on the bead
#   5. Final release branch clean  (mechanical) — `git status --porcelain` empty
#   6. Branch diverges cleanly     (mechanical) — cherry-picks onto base w/o conflict
#
# Usage:
#   release-audit.sh --bead <id> --branch <ref> --base <ref> [--test-cmd <cmd>] [--json]
#
# Options:
#   --bead <id>       Feature/review bead id whose recorded criteria are asserted (required).
#   --branch <ref>    The release branch under audit (default: current branch / HEAD).
#   --base <ref>      The base the branch must diverge cleanly from (default: origin/main).
#   --test-cmd <cmd>  Command run for criterion 3 (default: `make test`). Runs via `bash -c`.
#   --bd <path>       bd binary to query the bead with (default: `bd` on PATH).
#   --no-stub         Do not generate the release-gates/<bead>-gate.md stub.
#   --json            Emit a machine-readable JSON verdict to stdout.
#
# Exit codes: 0 = overall PASS; 1 = overall FAIL (>=1 criterion failed); 2 = usage/IO error.

set -uo pipefail

die() { echo "release-audit: $*" >&2; exit 2; }

BEAD=""
BRANCH=""
BASE="origin/main"
TEST_CMD="make test"
BD="bd"
JSON=0
STUB=1

while [[ $# -gt 0 ]]; do
    case "$1" in
        --bead)     BEAD="${2:-}"; shift 2 ;;
        --branch)   BRANCH="${2:-}"; shift 2 ;;
        --base)     BASE="${2:-}"; shift 2 ;;
        --test-cmd) TEST_CMD="${2:-}"; shift 2 ;;
        --bd)       BD="${2:-}"; shift 2 ;;
        --no-stub)  STUB=0; shift ;;
        --json)     JSON=1; shift ;;
        -h|--help)  sed -n '2,30p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) die "unknown argument: $1" ;;
    esac
done

[[ -n "$BEAD" ]] || die "--bead <id> is required (the feature/review bead whose criteria are asserted)"

command -v git >/dev/null 2>&1 || die "git not found on PATH"
REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || die "not inside a git repository"
cd "$REPO_ROOT" || die "cannot cd to repo root $REPO_ROOT"

# Default branch = whatever is checked out.
if [[ -z "$BRANCH" ]]; then
    BRANCH="$(git rev-parse --abbrev-ref HEAD 2>/dev/null)" || die "cannot resolve current branch"
fi

git rev-parse --verify --quiet "$BRANCH^{commit}" >/dev/null || die "branch not found: $BRANCH"
git rev-parse --verify --quiet "$BASE^{commit}"   >/dev/null || die "base not found: $BASE"

# --- Result accumulation ------------------------------------------------------
# Parallel arrays keyed by criterion index (1..6).
declare -a CRIT_NAME CRIT_RESULT CRIT_DETAIL
OVERALL_PASS=1

record() { # n name result detail
    CRIT_NAME[$1]="$2"; CRIT_RESULT[$1]="$3"; CRIT_DETAIL[$1]="$4"
    [[ "$3" == "PASS" ]] || OVERALL_PASS=0
}

# --- Recorded criteria: read the bead once ------------------------------------
BEAD_TEXT=""
if command -v "$BD" >/dev/null 2>&1 || [[ -x "$BD" ]]; then
    BEAD_TEXT="$("$BD" show "$BEAD" 2>/dev/null || true)"
fi

# 1. Review PASS present.
if grep -Eiq 'review.*\bpass\b' <<<"$BEAD_TEXT"; then
    record 1 "Review PASS present" PASS "reviewer PASS recorded on $BEAD"
else
    record 1 "Review PASS present" FAIL "no 'Review … PASS' marker recorded on bead $BEAD"
fi

# 2. Acceptance criteria met.
if grep -Eiq 'acceptance criteria met' <<<"$BEAD_TEXT"; then
    record 2 "Acceptance criteria met" PASS "AC walkthrough recorded on $BEAD"
else
    record 2 "Acceptance criteria met" FAIL "no 'Acceptance criteria met' marker recorded on bead $BEAD"
fi

# 4. No HIGH-severity findings open. FAILS only when an OPEN HIGH finding is
#    flagged; an explicit "No HIGH-severity findings open" clearance is fine.
if grep -Eiq 'high-severity finding open|open high-severity finding|high finding open' <<<"$BEAD_TEXT"; then
    record 4 "No HIGH-severity findings open" FAIL "an open HIGH-severity finding is recorded on bead $BEAD"
else
    record 4 "No HIGH-severity findings open" PASS "no open HIGH-severity finding recorded on $BEAD"
fi

# --- Mechanical criteria ------------------------------------------------------

# 3. Tests pass on the release branch.
TEST_LOG="$(mktemp)"
if bash -c "$TEST_CMD" >"$TEST_LOG" 2>&1; then
    record 3 "Tests pass on release branch" PASS "test command succeeded: $TEST_CMD"
else
    rc=$?
    record 3 "Tests pass on release branch" FAIL "test command failed (exit $rc): $TEST_CMD"
fi
rm -f "$TEST_LOG"

# 5. Final release branch is clean.
if [[ -z "$(git status --porcelain 2>/dev/null)" ]]; then
    record 5 "Final release branch clean" PASS "git status --porcelain is empty"
else
    record 5 "Final release branch clean" FAIL "working tree not clean (uncommitted changes present)"
fi

# 6. Branch diverges cleanly from base (cherry-picks without conflict).
#    Done in an isolated detached worktree so the caller's tree/branch is never
#    mutated and no CHERRY_PICK_HEAD is left in the primary .git.
SCRATCH="$(mktemp -d)"
cleanup_worktree() {
    git -C "$REPO_ROOT" worktree remove --force "$SCRATCH" >/dev/null 2>&1 || true
    rm -rf "$SCRATCH" 2>/dev/null || true
    git -C "$REPO_ROOT" worktree prune >/dev/null 2>&1 || true
}
trap cleanup_worktree EXIT

COMMITS="$(git rev-list --reverse "$BASE..$BRANCH" 2>/dev/null)"
if [[ -z "$COMMITS" ]]; then
    record 6 "Branch diverges cleanly from base" PASS "no commits ahead of $BASE (nothing to cherry-pick)"
elif ! git worktree add -q --detach "$SCRATCH" "$BASE" >/dev/null 2>&1; then
    record 6 "Branch diverges cleanly from base" FAIL "could not create scratch worktree at $BASE to test cherry-pick"
else
    if git -C "$SCRATCH" -c core.editor=true cherry-pick $COMMITS >/dev/null 2>&1; then
        record 6 "Branch diverges cleanly from base" PASS "cherry-pick of $BRANCH onto $BASE applied without conflict"
    else
        record 6 "Branch diverges cleanly from base" FAIL "cherry-pick of $BRANCH onto $BASE conflicts (branch does not diverge cleanly)"
        git -C "$SCRATCH" cherry-pick --abort >/dev/null 2>&1 || true
    fi
fi

# --- Emit -------------------------------------------------------------------
VERDICT="PASS"; [[ "$OVERALL_PASS" == "1" ]] || VERDICT="FAIL"

if [[ "$JSON" == "1" ]]; then
    printf '{\n'
    printf '  "bead": "%s",\n' "$BEAD"
    printf '  "branch": "%s",\n' "$BRANCH"
    printf '  "base": "%s",\n' "$BASE"
    printf '  "verdict": "%s",\n' "$VERDICT"
    printf '  "criteria": [\n'
    for n in 1 2 3 4 5 6; do
        sep=","; [[ "$n" == "6" ]] && sep=""
        # Escape double-quotes in detail for JSON safety.
        det="${CRIT_DETAIL[$n]//\"/\\\"}"
        printf '    {"n": %d, "name": "%s", "result": "%s", "detail": "%s"}%s\n' \
            "$n" "${CRIT_NAME[$n]}" "${CRIT_RESULT[$n]}" "$det" "$sep"
    done
    printf '  ]\n}\n'
else
    echo "── L10 release-audit — bead $BEAD (branch $BRANCH vs base $BASE) ──"
    for n in 1 2 3 4 5 6; do
        mark="✅"; [[ "${CRIT_RESULT[$n]}" == "PASS" ]] || mark="❌"
        printf '  %s [%d] %-34s %s — %s\n' "$mark" "$n" "${CRIT_NAME[$n]}" "${CRIT_RESULT[$n]}" "${CRIT_DETAIL[$n]}"
    done
    echo "── VERDICT: $VERDICT ──"
fi

# --- Generate the gate stub ---------------------------------------------------
if [[ "$STUB" == "1" ]]; then
    GATE_DIR="$REPO_ROOT/release-gates"
    mkdir -p "$GATE_DIR" || die "cannot create $GATE_DIR"
    STUB_PATH="$GATE_DIR/${BEAD}-gate.md"
    {
        echo "# Release gate — ${BEAD}"
        echo
        echo "**Generated by:** scripts/ci/release-audit.sh (L10 release-audit gate, beads-nsa)"
        echo "**Bead:** ${BEAD}"
        echo "**Release branch:** \`${BRANCH}\`"
        echo "**Base:** \`${BASE}\`"
        echo "**Test command:** \`${TEST_CMD}\`"
        echo
        echo "## Verdict: ${VERDICT}"
        echo
        echo "## Criteria"
        echo
        echo "| # | Criterion | Result | Evidence |"
        echo "|---|-----------|--------|----------|"
        for n in 1 2 3 4 5 6; do
            echo "| ${n} | ${CRIT_NAME[$n]} | ${CRIT_RESULT[$n]} | ${CRIT_DETAIL[$n]} |"
        done
        echo
        echo "_This stub is machine-generated. Fill in narrative sections (what this ships,"
        echo "test evidence, cherry-pick mechanics) before recording the gate as final._"
    } > "$STUB_PATH" || die "cannot write $STUB_PATH"
fi

[[ "$OVERALL_PASS" == "1" ]] && exit 0 || exit 1
