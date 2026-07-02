#!/usr/bin/env bash
# check-resource-safety.sh — RESOURCE-SAFETY policy gate (beads-r06.4).
#
# Closes the unbounded-resource-defect class behind the 134GB-RSS OOM that
# crashed m7i (RCA hq-lcu9o / fix beads-kbw): a bare `bd list` materialized
# EVERY dependency row for a ~1,578-issue DB into one map, growing the process
# to 128-134GB and OOM-killing the host.
#
# golangci-lint's bodyclose/rowserrcheck tier (see .golangci.yml) catches
# leaked HTTP bodies and unchecked rows.Err(). But it CANNOT express the domain
# invariant that the beads-kbw leak actually violated: a DISPLAY/LIST path must
# never load a whole-table result set whose size scales with total DB size
# rather than the number of rows shown. This script is the source-time guard
# for that class.
#
# It fails CI when a known unbounded full-table loader is called from a code
# path that renders/streams a bounded view, unless that call site is in the
# explicit allowlist (migration, fixtures, and other whole-DB consumers where
# loading every row is the actual job and bounded by a one-shot operation).
#
# Companion runtime proof: the C2 soak test (beads-r06.5) demonstrates the
# bound holds under load. This gate makes the regression un-mergeable.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

# Unbounded full-table loaders: methods that materialize an entire table's rows
# (size scales with DB size, not with what is displayed). Calling these from a
# display/list/render path is the beads-kbw defect.
#
# Add a method here when you introduce a new whole-table loader. The bounded
# alternative for dependencies is GetDependencyRecordsForIssues(ctx, ids) /
# displayedIssueDeps (cmd/bd/list.go), which loads only the displayed issues.
unbounded_loaders='GetAllDependencyRecords'

# Allowlist: <file>::<symbol-or-marker>. Call sites where loading the whole
# table is the legitimate, one-shot job (not a per-render display path). Keep
# this list tight and justify every entry — every addition widens the leak
# surface.
#
# A line may also opt out inline with a trailing `// resource-safety:allow`
# comment plus a reason, for one-off justified sites.
allowlist=$(
  cat <<'EOF'
cmd/bd/migrate_issues.go
internal/testutil/fixtures/fixtures.go
internal/storage/dolt/iter_stubs.go
internal/storage/embeddeddolt/iter_stubs.go
internal/storage/dolt/dependencies.go
internal/storage/embeddeddolt/list_queries.go
internal/storage/issueops/dependency_queries.go
internal/storage/dependency_queries.go
EOF
)

status=0
loader_regex="$(echo "$unbounded_loaders" | tr ' ' '|')"

# Scan tracked Go source (excluding tests — a test may legitimately exercise the
# unbounded path to PROVE the bound, e.g. the kbw regression test).
while IFS= read -r f; do
  [[ -f "$f" ]] || continue

  # Whole-file allowlist (loader DEFINITION sites + justified whole-DB consumers).
  if grep -Fxq "$f" <<<"$allowlist"; then
    continue
  fi

  while IFS= read -r hit; do
    lineno="${hit%%:*}"
    line="${hit#*:}"

    # Skip comments and the bounded-alternative docstrings that mention the name.
    [[ "$line" =~ ^[[:space:]]*// ]] && continue
    [[ "$line" =~ ^[[:space:]]*\* ]] && continue

    # Only flag actual CALLS: `Something.GetAllDependencyRecords(` or
    # `GetAllDependencyRecords(`. An interface method declaration ends without
    # `(ctx` arg invocation on a receiver; we match the call paren form.
    if [[ ! "$line" =~ ($loader_regex)\( ]]; then
      continue
    fi

    # Interface/method DECLARATIONS (in non-allowlisted files) look like
    # `GetAllDependencyRecords(ctx context.Context) (...)`. Skip a signature
    # line (contains a typed param list `context.Context`) that is not a call.
    if [[ "$line" =~ ($loader_regex)\(ctx[[:space:]]+context\.Context ]]; then
      continue
    fi

    # Inline opt-out for justified one-off sites.
    if [[ "$line" == *"// resource-safety:allow"* ]]; then
      continue
    fi

    printf 'error: %s:%s: unbounded full-table loader called from a non-allowlisted path\n' "$f" "$lineno" >&2
    printf '       %s\n' "${line#"${line%%[![:space:]]*}"}" >&2
    status=1
  done < <(grep -nE "($loader_regex)\(" "$f" 2>/dev/null || true)
done < <(git ls-files '*.go' | grep -v '_test\.go$')

if (( status != 0 )); then
  cat >&2 <<EOF

RESOURCE-SAFETY violation (beads-r06.4 / RCA hq-lcu9o).

A display/list/render path must not materialize a whole-table result set whose
size scales with total DB size. That is the defect class that grew \`bd list\` to
134GB RSS and OOM-killed the host (fix beads-kbw).

Fix by EITHER:
  1. Load only what you display — e.g. GetDependencyRecordsForIssues(ctx, ids)
     / displayedIssueDeps (see cmd/bd/list.go), or stream via an Iter.
  2. If this call site genuinely needs the whole table for a bounded one-shot
     operation (migration, fixture build), add the file to the allowlist in
     scripts/check-resource-safety.sh with a justification, OR annotate the
     specific line with a trailing \`// resource-safety:allow <reason>\`.
EOF
  exit 1
fi

echo "check-resource-safety: no unbounded full-table loaders in display paths."
