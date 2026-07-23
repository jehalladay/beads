package versioncontrolops

import (
	"context"
	"fmt"
)

// Flatten squashes all Dolt commit history into a single commit using
// the Tim Sehn recipe:
//  0. Flush any uncommitted working set into a commit (see beads-1yh3g below)
//  1. Create a temp branch from current state
//  2. Checkout temp branch
//  3. Soft-reset to the initial (oldest) commit, collapsing all history
//  4. Stage all + commit as a single snapshot
//  5. Checkout main
//  6. Hard-reset main to the flattened branch
//  7. Delete temp branch
//
// All current data is preserved; only commit-level history (time travel) is lost.
//
// Callers should run DoltGC afterward to reclaim disk space from orphaned history.
//
// conn must be a single database connection (not a pooled *sql.DB) since the
// stored procedures rely on session-scoped state (current branch, working set).
func Flatten(ctx context.Context, conn DBConn) (retErr error) {
	// Find the initial commit hash (oldest ancestor).
	var initialHash string
	if err := conn.QueryRowContext(ctx,
		"SELECT commit_hash FROM dolt_log ORDER BY date ASC LIMIT 1",
	).Scan(&initialHash); err != nil {
		return fmt.Errorf("find initial commit: %w", err)
	}

	// Count commits to check if flatten is needed.
	var commitCount int
	if err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM dolt_log",
	).Scan(&commitCount); err != nil {
		return fmt.Errorf("count commits: %w", err)
	}
	if commitCount <= 1 {
		return nil // already flat
	}

	execSQL := func(name, query string, args ...interface{}) error {
		if _, err := conn.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("flatten step %q: %w", name, err)
		}
		return nil
	}

	// beads-1yh3g: flush any uncommitted working set into a commit BEFORE the
	// squash recipe runs. Under `dolt.auto-commit=batch`/`off`, mutations sit in
	// the working set of the current branch (main) without being committed. The
	// recipe below creates flatten-tmp from main's HEAD (which does NOT include
	// the uncommitted working set — DOLT_CHECKOUT to flatten-tmp carries a clean
	// tree), squashes that clean state, then `DOLT_RESET('--hard', 'flatten-tmp')`
	// on main OVERWRITES main — silently discarding the pending working set. So
	// `bd flatten --force` with pending batch writes lost all uncommitted data.
	// Committing it here first folds it into the squash (mirrors the SIGTERM
	// batch-flush semantics), so no data is lost. DOLT_COMMIT('-Am') on a clean
	// working set is a benign no-op ("nothing to commit"), so guard on
	// workingSetClean to avoid the error and skip the extra commit on the common
	// clean path.
	if !workingSetClean(ctx, conn) {
		if err := execSQL("flush working set", "CALL DOLT_COMMIT('-Am', 'flatten: flush pending working set before squash')"); err != nil {
			return err
		}
	}

	// A prior flatten that failed mid-way can leave the temp branch behind;
	// since "create temp branch" below has no --force, that stale branch would
	// wedge every future flatten with "branch already exists". Best-effort
	// pre-delete clears it (mirrors the Compact recipe's cleanup). Ignore the
	// error: on the normal path there is no such branch.
	_, _ = conn.ExecContext(ctx, "CALL DOLT_BRANCH('-D', 'flatten-tmp')")

	// Best-effort cleanup: if any step fails after creating the temp branch,
	// return to main and delete the temp branch so the session isn't left on
	// flatten-tmp and a re-run isn't blocked by the leftover branch. Matches the
	// defer in versioncontrolops.Compact (beads-wmup — Flatten previously had no
	// such cleanup and self-wedged on retry).
	branchCreated := false
	defer func() {
		if retErr != nil && branchCreated {
			_, _ = conn.ExecContext(ctx, "CALL DOLT_CHECKOUT('main')")
			_, _ = conn.ExecContext(ctx, "CALL DOLT_BRANCH('-D', 'flatten-tmp')")
		}
	}()

	if err := execSQL("create temp branch", "CALL DOLT_BRANCH('flatten-tmp')"); err != nil {
		return err
	}
	branchCreated = true

	steps := []struct {
		name  string
		query string
		args  []interface{}
	}{
		{"checkout temp branch", "CALL DOLT_CHECKOUT('flatten-tmp')", nil},
		{"soft reset to initial", "CALL DOLT_RESET('--soft', ?)", []interface{}{initialHash}},
		{"commit flattened snapshot", "CALL DOLT_COMMIT('-Am', 'flatten: squash all history into single commit')", nil},
		{"checkout main", "CALL DOLT_CHECKOUT('main')", nil},
		{"reset main to flattened", "CALL DOLT_RESET('--hard', 'flatten-tmp')", nil},
		{"delete temp branch", "CALL DOLT_BRANCH('-D', 'flatten-tmp')", nil},
	}

	for _, s := range steps {
		if err := execSQL(s.name, s.query, s.args...); err != nil {
			return err
		}
	}

	return nil
}

// FlattenDryRun returns the commit count and initial hash without modifying anything.
func FlattenDryRun(ctx context.Context, conn DBConn) (commitCount int, initialHash string, err error) {
	if err = conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM dolt_log",
	).Scan(&commitCount); err != nil {
		err = fmt.Errorf("count commits: %w", err)
		return
	}
	if err = conn.QueryRowContext(ctx,
		"SELECT commit_hash FROM dolt_log ORDER BY date ASC LIMIT 1",
	).Scan(&initialHash); err != nil {
		err = fmt.Errorf("find initial commit: %w", err)
		return
	}
	return
}
