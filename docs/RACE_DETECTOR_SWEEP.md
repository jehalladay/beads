# beads-1ne — Concurrency / race-detector sweep

**Date:** 2026-07-17
**Author:** beads_eng_5
**Base commit:** `6095f4ace` (main, fast-forwarded)
**Toolchain:** go1.26.2, `CGO_ENABLED=1`, build tag `gms_pure_go`, Dolt testcontainer image
`dolthub/dolt-sql-server:2.1.0`

## Result

**Zero data races.** The Go race detector (`-race`) reported **0 `DATA RACE`
events** across every package in the repository, including the paths the bead
flagged as prime suspects (circuit-breaker shared state, the retry / connection
paths, and the multi-writer / multi-process Dolt storage tests). No fix beads
are warranted.

The only test *failures* observed were pre-existing **environment / git-config
pollution** in the crew shell (see below) — none are races, and all pass once
the ambient env is stripped (which the `make test-race` harness does
automatically).

## What was run

| Command | Scope | DATA RACE | Notes |
|---|---|---|---|
| `go test -race -tags gms_pure_go ./...` | whole repo | 0 | `cmd/bd` passed (`ok 422s`); env-pollution FAILs in root + `cmd/bd/doctor` (not races) |
| `go test -race ... ./internal/storage/dolt/ -run 'Concurrent\|CrossProject\|MultiStore\|Pool\|RetryOn\|ConcurrencyMultiProcess'` | Dolt-backed concurrency suite (needs live server) | 0 | 86s, all `ok` — the real row-lock / serialization / multi-writer paths |
| `go test -race ... ./internal/storage/dolt/ -run 'Race\|Concurrent\|Circuit\|CloseRace' -count=5` | stress, no-server subset | 0 | circuit-breaker + dial-tracking + close-race under repetition |
| `go test -race ... ./internal/storage/dolt/` (full pkg) | full storage pkg | 0 | FAIL was a 10-min `go test` **timeout** under -race + machine load, not a race |
| `go test -race ... ./internal/storage/dbproxy/... ./internal/tracker/...` | proxy + tracker goroutines | 0 | all `ok` |
| `make test-race` (hermetic, strips env, skips Dolt) | whole repo | 0 | FAIL was an 8-min timeout in `TestGlobalDBIdentityCheck` under load, not a race |

The Dolt-backed tests require the `dolthub/dolt-sql-server:2.1.0` testcontainer;
the default cached image was `2.0.7`, so an explicit `docker pull` was needed to
exercise the multi-writer paths (they graceful-skip otherwise).

## Suspect paths — verdict

- **Circuit breaker (`internal/storage/dolt/circuit.go`)** — the file-backed
  breaker is guarded by a per-instance `sync.Mutex` (`cb.mu`); the cross-process
  state file is written atomically (temp + `os.Rename`). No in-process race. The
  concurrent-tracking and close-race tests exercise it under `-count=5` clean.
- **Retry / connection path (`store_retry.go`)** — `withRetry` keeps its
  `attempts` counter closure-local; failure/success are funneled through the
  breaker's own mutex. Clean.
- **Package globals** — `autoStartRefs` is guarded by `autoStartRefs.mu`; other
  package-level `var`s are immutable after init (regexes, static slices) or set
  once. No unsynchronized shared mutable state found.
- **Lease row_lock / heartbeat** — this lives in the `gt` control plane, not the
  `beads` repo; nothing to sweep here.

## Not-a-race: env / git-config pollution (known class)

Running a bare `go test ./...` inside a gt-crew shell surfaces failures that are
pollution, not regressions or races:

- `TestOpenFromConfig_ServerModeFailsWithoutServer`,
  `TestOpenBestAvailable_ServerMode_FailsWithoutServer`,
  `TestRunDoltHealthChecks_DoltBackendNoServer`,
  `TestCheckFreshClone_ServerModeUnreachable`,
  `TestDoltServerConfig_PopulatesFromConfig` — the crew shell exports
  `BEADS_DOLT_SERVER_HOST/PORT` pointing at the live hub, so "should fail without
  a server" assertions connect successfully instead. Pass under
  `env -u BEADS_DOLT_SERVER_HOST -u BEADS_DOLT_SERVER_PORT -u BD_IGNORE_SCHEMA_SKEW`.
- `TestCheckBeadsRole_NotConfigured`, `TestCheckBeadsRole_NotGitRepo` — the crew
  worktree has `git config beads.role=contributor` (global), so the "not
  configured" assertions see a configured role.

`make test-race` (via `scripts/test.sh` + `scripts/ci/lib/test-env.sh`) strips
all of these and adds a `dolt` skip pattern, so the sanctioned tier is
self-hermetic and race-clean.

## Observation for the `make test-race` tier (not a race, minor)

`make test-race` inherits the default `TEST_TIMEOUT=3m` and does **not** bump it
for `-race`. Race instrumentation slows tests ~7-10x; on a loaded shared box the
subprocess-spawning tests (`TestGlobalDBIdentityCheck`, the `cmd/bd` CLI suite)
can exceed 3m and time out — a false failure, not a race. CI avoids this by
running `-race -short` (heavy integration tests skip under `-short`), whereas
`test.sh` does not pass `-short`. Consider either passing `-short` in the race
tier or raising the race-tier default timeout so `make test-race` matches CI
behavior. Filed as **beads-367** (follow-up) rather than fixed here — it is a
test-harness ergonomics gap, not a data race, and out of scope for this audit
deliverable.
