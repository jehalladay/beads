# Linting Policy

Last reviewed: 2026-07-02

Freshness source: `.golangci.yml`, `.github/workflows/pr.yml`,
`.github/workflows/main.yml`, `scripts/check-resource-safety.sh`, and
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

Deliberately **not** in the required gate yet, because each would land
pre-existing violations and a tolerated failing baseline is forbidden (see
above); burndown is tracked under epic beads-r06 (see beads-yzo):

- **`sqlclosecheck`** — 77 production findings, almost all false positives on
  the correct eager `rows.Close()`-in-a-loop idiom (where a `defer` would leak
  cursors). Not mechanizable without excluding ~31 files of correct code.
- **`contextcheck`** (42) and **`staticcheck`** SA-class (14) — lifecycle and
  dead-code/deprecation findings, not the resource-leak class.

## CI Cleanup Decision

`pr-lint` should stay separate from `pr-policy` and `pr-core` so failures are
easy to identify and rerun. It should include:

- `make fmt-check`
- `golangci-lint run --timeout=5m --build-tags=gms_pure_go ./...`

See [`CI_CLEANUP_PLAN.md`](CI_CLEANUP_PLAN.md) for the full CI tier policy.

## Future Work

- Pin the `golangci-lint` version in CI instead of using `version: latest`.
- Move the final CI shape behind a repository-owned `scripts/ci/pr-lint.sh`
  wrapper.
- Periodically audit `.golangci.yml` exclusions and remove entries that are no
  longer needed.
