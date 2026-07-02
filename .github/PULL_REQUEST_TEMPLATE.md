<!--
This is a starting scaffold to help reviewers (human and agent) parse intent before diff.
Replace, expand, or delete sections freely. CONTRIBUTING.md has the full hygiene rules.
-->

## What

<!-- One or two plain-language sentences: what does this change do? -->

## Why

<!-- The problem this solves, the motivation, or a link to the issue: e.g. "Fixes #123" -->

## Verification

<!-- How you tested. Commands a reviewer can run to confirm. -->

## Resource safety

<!--
MANDATORY for any change that lists/queries data, reads I/O, caches, or starts
goroutines. The unbounded-resource-defect class (a bare `bd list` materialized
every dependency row -> 134GB RSS -> OOM, RCA hq-lcu9o / fix beads-kbw) is
enforced by the RESOURCE-SAFETY lint tier (.golangci.yml) and
scripts/check-resource-safety.sh. Confirm the boxes that apply; strike through
any that don't.
-->

- [ ] **Bounded reads**: every list/SELECT path is bounded or paginated — no
      full-table materialization whose size scales with DB size (use
      `GetDependencyRecordsForIssues`/`displayedIssueDeps` or stream via `Iter`,
      not `GetAllDependencyRecords`, in display paths).
- [ ] **Bounded readers**: no `io.ReadAll` (or equivalent) on an unbounded
      reader (network body, untrusted stream) — caps/limits applied.
      (Review-enforced.)
- [ ] **Closed handles**: HTTP response bodies are closed (CI-enforced by
      bodyclose) and `rows.Err()` is checked after iteration (CI-enforced by
      rowserrcheck). Close `sql.Rows`/`Stmt` too — review-enforced: prefer
      eager `Close()` inside a loop that reopens rows, `defer` otherwise.
- [ ] **Bounded caches**: any cache added/grown is evicted or size-bounded.
      (Review-enforced.)
- [ ] **Goroutine lifecycle**: any goroutine has a defined lifecycle and
      cancellation (inherited `context.Context`). (Review-enforced;
      contextcheck deferred to beads-yzo — see .golangci.yml.)
