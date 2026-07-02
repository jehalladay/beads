# Linting Policy

Last reviewed: 2026-07-02 (staticcheck SA-class added to the gate, beads-yzo)

Freshness source: `.golangci.yml`, `.golangci-ratchet.yml`,
`.github/workflows/pr.yml`, `.github/workflows/main.yml`,
`scripts/check-resource-safety.sh`, `scripts/ci/pr-lint.sh`, and
`golangci-lint run --timeout=5m --build-tags=gms_pure_go ./...` returning zero
issues.

This document explains the required Go lint gate for this codebase.

## Current Status

Lint is a required CI gate. The PR and main workflows run `golangci-lint` with
the repository configuration and `--build-tags=gms_pure_go`; it is expected to
pass with zero issues.

Run the same check locally with:

```bash
golangci-lint run --timeout=5m --build-tags=gms_pure_go ./...
```

Formatting is a separate required gate:

```bash
make fmt-check
```

## Policy

Treat new lint findings as defects to fix before merge. Do not add a tolerated
failing baseline, and do not configure CI with `--issues-exit-code=0`.

When a linter reports an intentional or false-positive pattern:

- Prefer a narrow `.golangci.yml` exclusion tied to a path, linter, and message.
- Use `//nolint:<linter>` only when the reason is local to a specific line and
  the comment explains why the warning is not actionable.
- Keep broad linter disables as a last resort.

The current configuration already encodes accepted exclusions for intentional
patterns such as deferred cleanup errors, controlled subprocess execution,
test-fixture file reads, and documented security false positives.

## Resource-safety tier (beads-r06.4)

To close the unbounded-resource-defect class behind the 134GB-RSS OOM that
crashed a host (a bare `bd list` materialized every dependency row; RCA
hq-lcu9o / fix beads-kbw), the gate enforces:

- **`bodyclose`** — HTTP response bodies must be closed (fd/goroutine leak).
- **`rowserrcheck`** — `rows.Err()` must be checked after iterating a result
  set (the destructive-path defect swept in beads-r06.15). Three streaming/
  ownership-handoff sites in `internal/storage/dolt/` are excluded with a
  documented reason — they check `Err()` across a boundary the linter can't
  follow.
- **`scripts/check-resource-safety.sh`** — a source-time gate (wired into
  `scripts/ci/pr-policy.sh`) enforcing the domain invariant the linters cannot
  express: a display/list path must never call an unbounded whole-table loader
  (e.g. `GetAllDependencyRecords`). New call sites fail CI unless allowlisted
  with a justification or annotated `// resource-safety:allow <reason>`.
- **`staticcheck`** (SA-class only) — the SA* bug/correctness checks (dead code,
  nil-pointer derefs, deprecated APIs). The 17 pre-existing SA findings were
  burned down in-tree (beads-yzo), so the class is green and now gates. The
  style/simplify/quickfix families (`ST*`/`S*`/`QF*`, ~130 findings) are
  intentionally excluded via `linters.settings.staticcheck.checks: ["SA*"]` —
  they are not the resource-safety/correctness class.

### Ratchet for the deferred classes (Mayor ruling, Option 2)

Some linters carry a pre-existing baseline, so they are **not** in the
zero-tolerance full-tree gate (a tolerated failing baseline is forbidden — see
above). Instead they run in **ratchet** mode: `scripts/ci/pr-lint-ratchet.sh`
runs `.golangci-ratchet.yml` with `golangci-lint --new-from-merge-base
origin/main`, so a PR fails only on **new** violations it introduces; the
existing baseline does not fail. This blocks regressions immediately while the
baseline is burned down under beads-yzo (eng_5). As each class reaches zero it
graduates into `.golangci.yml` as a normal zero-tolerance linter and is removed
from the ratchet config. The ratchet needs full git history (CI checks out
`fetch-depth: 0`).

Currently ratcheted (still carry a baseline):

- **`sqlclosecheck`** — 84 production findings, effectively **all** false
  positives on two correct idioms: (1) the eager `rows.Close()`-in-a-loop
  pattern (a `defer` would accumulate open cursors until function return — a
  leak), and (2) closing a result set before running a sequential query on the
  same transaction (Dolt/the driver cannot hold two result sets open on one
  connection; several sites document this inline, e.g. `stale.go`). The one
  `"not closed"` finding is `DoltStore.queryContext`, which hands the `*sql.Rows`
  to its caller (the same ownership-handoff already excluded for `rowserrcheck`).
  Because there is no code to fix, a new ratchet hit may itself be that idiom;
  the burndown decides exclusion-vs-refactor per site.
- **`contextcheck`** (51) — non-inherited contexts break cancellation lifecycle
  (ctx-threading through CLI/git-subprocess helper chains); not the resource-leak
  class.

**Graduated** to the zero-tolerance gate (removed from the ratchet):

- **`staticcheck`** SA-class — the 17 pre-existing SA findings (dead code,
  nil-deref false-positive, deprecated APIs) were burned down in-tree under
  beads-yzo, so the class is green and now runs at zero-tolerance in
  `.golangci.yml` (`checks: ["SA*"]`). See the resource-safety tier above.

## CI Cleanup Decision

`pr-lint` should stay separate from `pr-policy` and `pr-core` so failures are
easy to identify and rerun. It should include:

- `make fmt-check`
- `golangci-lint run --timeout=5m --build-tags=gms_pure_go ./...`
- the resource-safety ratchet (`scripts/ci/pr-lint-ratchet.sh`)

See [`CI_CLEANUP_PLAN.md`](CI_CLEANUP_PLAN.md) for the full CI tier policy.

## Future Work

- Pin the `golangci-lint` version in CI instead of using `version: latest`.
- Move the final CI shape behind a repository-owned `scripts/ci/pr-lint.sh`
  wrapper.
- Periodically audit `.golangci.yml` exclusions and remove entries that are no
  longer needed.
