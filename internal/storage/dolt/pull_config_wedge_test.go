package dolt

import (
	"context"
	"database/sql"
	"testing"
)

// configDirty reports whether the config table has uncommitted changes in the
// working set — the exact condition that makes DOLT_MERGE refuse to start.
func configDirty(t *testing.T, ctx context.Context, db *sql.DB) bool {
	t.Helper()
	var n int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM dolt_status WHERE table_name = 'config'").Scan(&n); err != nil {
		t.Fatalf("query dolt_status: %v", err)
	}
	return n > 0
}

// TestCommitBeforePullIncludesConfig is the regression test for the pull config
// wedge: persistent memories live in config as kv.memory.* rows, plain Commit()
// excludes config (GH#2455), so they sit permanently uncommitted and the
// pre-pull "clean the working set" step leaves config dirty — DOLT_MERGE then
// refuses to start ("cannot merge with uncommitted changes"). commitBeforePull
// must stage config explicitly and leave the working set clean.
func TestCommitBeforePullIncludesConfig(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	db := store.db

	// Simulate `bd remember`: a kv.memory.* row in the synced config table.
	if _, err := db.ExecContext(ctx,
		"INSERT INTO config (`key`, value) VALUES ('kv.memory.test-wedge', 'v1')"); err != nil {
		t.Fatalf("insert config memory row: %v", err)
	}

	// Plain Commit() leaves config dirty — the wedge precondition. (With only
	// config dirty it commits nothing and returns nil.)
	if err := store.Commit(ctx, "commit excluding config"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !configDirty(t, ctx, db) {
		t.Fatalf("Commit() unexpectedly committed config; the wedge precondition no longer reproduces (did GH#2455's config exclusion change?)")
	}

	// commitBeforePull must stage config and leave the working set clean so the
	// subsequent merge can start.
	if err := store.commitBeforePull(ctx, "auto-commit before pull"); err != nil {
		t.Fatalf("commitBeforePull: %v", err)
	}
	if configDirty(t, ctx, db) {
		t.Fatalf("commitBeforePull left config dirty; DOLT_MERGE would still refuse to start")
	}
}

// TestPullAutoResolveMemoryConfigConflicts verifies that a merge conflict
// limited to kv.memory.* config rows is auto-resolved with "theirs" — the same
// machine-convergent policy used for metadata. Without this, making config a
// synced table (so memories round-trip) would turn same-memory edits across
// clones into an operator-visible pull wedge.
func TestPullAutoResolveMemoryConfigConflicts(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	db := store.db

	// Stage config explicitly: -Am was observed not to stage config reliably
	// under the server-mode stored-procedure path, so the test must not depend
	// on it to set up the conflict.
	commitConfig := func(msg string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, "CALL DOLT_ADD('config')"); err != nil {
			t.Fatalf("dolt add config: %v", err)
		}
		if _, err := db.ExecContext(ctx, "CALL DOLT_COMMIT('-m', ?)", msg); err != nil {
			t.Fatalf("dolt commit: %v", err)
		}
	}

	value := resolveConfigConflict(t, ctx, store, "kv.memory.shared", "ours", "theirs", commitConfig)
	if value != "theirs" {
		t.Errorf("resolved memory value = %q, want \"theirs\" (--theirs convergent)", value)
	}
}

// TestPullAutoResolveSkipsNonMemoryConfigConflicts verifies the prefix boundary:
// a config key under kv. but NOT kv.memory. (e.g. a user kv setting, or
// issue_prefix) is a real semantic conflict and must be left for the operator,
// so the whole config table is declined.
func TestPullAutoResolveSkipsNonMemoryConfigConflicts(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	db := store.db

	commitConfig := func(msg string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, "CALL DOLT_ADD('config')"); err != nil {
			t.Fatalf("dolt add config: %v", err)
		}
		if _, err := db.ExecContext(ctx, "CALL DOLT_COMMIT('-m', ?)", msg); err != nil {
			t.Fatalf("dolt commit: %v", err)
		}
	}

	resolved := tryResolveConfigConflict(t, ctx, store, "kv.custom.setting", "ours", "theirs", commitConfig)
	if resolved {
		t.Fatalf("non-memory config conflict was auto-resolved; only kv.memory.* keys are safe")
	}
}

// resolveConfigConflict sets up a same-key config conflict on a divergent
// branch, merges it, runs the auto-resolver, and (asserting it resolved)
// commits and returns the resolved value. commitFn stages+commits config.
func resolveConfigConflict(t *testing.T, ctx context.Context, store *DoltStore, key, ours, theirs string, commitFn func(string)) string {
	t.Helper()
	resolved, db := runConfigConflictMerge(t, ctx, store, key, ours, theirs, commitFn, true)
	var value string
	if err := db.QueryRowContext(ctx, "SELECT value FROM config WHERE `key` = ?", key).Scan(&value); err != nil {
		t.Fatalf("read resolved config value: %v", err)
	}
	if !resolved {
		t.Fatalf("config conflict on %q was not auto-resolved", key)
	}
	return value
}

// tryResolveConfigConflict is resolveConfigConflict's negative-path sibling: it
// returns whether the resolver accepted the conflict without requiring it to.
func tryResolveConfigConflict(t *testing.T, ctx context.Context, store *DoltStore, key, ours, theirs string, commitFn func(string)) bool {
	t.Helper()
	resolved, _ := runConfigConflictMerge(t, ctx, store, key, ours, theirs, commitFn, false)
	return resolved
}

// runConfigConflictMerge builds a same-key config conflict (ours on the current
// branch, theirs on a divergent "remote" branch), merges remote into current
// with conflict-tolerant session flags, and runs tryAutoResolveMergeConflicts.
// When commitOnResolve and the resolver succeeds, the merge tx is committed so
// callers can read the settled value; otherwise the tx is rolled back.
func runConfigConflictMerge(t *testing.T, ctx context.Context, store *DoltStore, key, ours, theirs string, commitFn func(string), commitOnResolve bool) (bool, *sql.DB) {
	t.Helper()
	db := store.db

	var currentBranch string
	if err := db.QueryRowContext(ctx, "SELECT active_branch()").Scan(&currentBranch); err != nil {
		t.Fatalf("active_branch: %v", err)
	}

	// Our value on the current branch.
	if _, err := db.ExecContext(ctx, "INSERT INTO config (`key`, value) VALUES (?, ?)", key, ours); err != nil {
		t.Fatalf("insert local config: %v", err)
	}
	commitFn("local config")

	// Their value on a branch forked from the common ancestor (HEAD~1).
	remoteBranch := currentBranch + "_cfgremote"
	if _, err := db.ExecContext(ctx, "CALL DOLT_BRANCH(?, 'HEAD~1')", remoteBranch); err != nil {
		t.Fatalf("create remote branch: %v", err)
	}
	defer func() {
		db.ExecContext(ctx, "CALL DOLT_CHECKOUT(?)", currentBranch)
		db.ExecContext(ctx, "CALL DOLT_BRANCH('-D', ?)", remoteBranch)
	}()
	if _, err := db.ExecContext(ctx, "CALL DOLT_CHECKOUT(?)", remoteBranch); err != nil {
		t.Fatalf("checkout remote branch: %v", err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO config (`key`, value) VALUES (?, ?)", key, theirs); err != nil {
		t.Fatalf("insert remote config: %v", err)
	}
	commitFn("remote config")
	if _, err := db.ExecContext(ctx, "CALL DOLT_CHECKOUT(?)", currentBranch); err != nil {
		t.Fatalf("checkout current branch: %v", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if _, err := tx.ExecContext(ctx, "SET @@dolt_allow_commit_conflicts = 1"); err != nil {
		_ = tx.Rollback()
		t.Fatalf("set dolt_allow_commit_conflicts: %v", err)
	}
	_, mergeErr := tx.ExecContext(ctx, "CALL DOLT_MERGE(?)", remoteBranch)

	resolved, resolveErr := store.tryAutoResolveMergeConflicts(ctx, tx)
	if resolveErr != nil {
		_ = tx.Rollback()
		t.Fatalf("tryAutoResolveMergeConflicts error: %v (mergeErr: %v)", resolveErr, mergeErr)
	}
	if !resolved {
		_ = tx.Rollback()
		return false, db
	}
	if !commitOnResolve {
		_ = tx.Rollback()
		return true, db
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit after auto-resolve: %v", err)
	}
	return true, db
}
