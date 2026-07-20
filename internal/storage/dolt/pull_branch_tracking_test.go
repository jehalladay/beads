package dolt

import (
	"context"
	"strings"
	"testing"
)

// TestPullWithAutoResolve_BranchTrackingFallback verifies that when DOLT_PULL
// returns the GH#3144 branch-tracking error (repo_state.json 'branches' map
// is empty because the remote was added via `bd dolt remote add` rather than
// `dolt clone`), pullWithAutoResolve enters the DOLT_FETCH + DOLT_MERGE
// fallback path.
//
// This test covers the fallback error leg (DOLT_FETCH fails because the test
// store has no configured remote). The full success path — where DOLT_FETCH
// and DOLT_MERGE both succeed — is exercised by
// TestPullWithAutoResolve_BranchTrackingSuccess in the integration test file
// (//go:build integration), which requires a remotesapi-accessible Dolt server.
func TestPullWithAutoResolve_BranchTrackingFallback(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	// Create a stored procedure that injects the exact Dolt GH#3144 error text.
	// This reproduces the message DOLT_PULL emits when repo_state.json lacks
	// branch-tracking info for the remote, without requiring a real remote.
	const createSP = `
		CREATE PROCEDURE inject_tracking_error()
		BEGIN
			SIGNAL SQLSTATE 'HY000'
			SET MESSAGE_TEXT = 'Error 1105: You asked to pull from the remote origin, but did not specify a branch. Because this is not the default configured remote for your current branch, you must specify a branch.';
		END`
	if _, err := store.execContext(ctx, createSP); err != nil {
		t.Skipf("stored procedures with SIGNAL not supported by this Dolt version: %v", err)
	}
	defer func() {
		_, _ = store.execContext(context.Background(), "DROP PROCEDURE IF EXISTS inject_tracking_error")
	}()

	// pullWithAutoResolve executes the query inside a transaction, checks the
	// error with isBranchTrackingError, and — on match — falls back to
	// DOLT_FETCH(remote, s.branch). The test store's s.remote is "" (no
	// remote configured), so DOLT_FETCH immediately fails, producing the
	// "fetch from /" error that confirms the fallback was entered.
	err := store.pullWithAutoResolve(ctx, store.remote, "CALL inject_tracking_error()")

	// The error must come from the DOLT_FETCH attempt, not from the original
	// DOLT_PULL proxy. If the fallback was not triggered, the error would
	// surface a different message (e.g. the raw SIGNAL text).
	if err == nil {
		t.Fatal("expected an error from DOLT_FETCH (no remote configured), got nil")
	}
	if strings.Contains(err.Error(), "inject_tracking_error") && strings.Contains(err.Error(), "does not exist") {
		t.Skipf("stored procedure is not visible to pull long-timeout connection on this Dolt version: %v", err)
	}
	if !strings.Contains(err.Error(), "fetch from") {
		t.Errorf("expected 'fetch from' error confirming fallback was triggered; got: %v", err)
	}
}

// The end-to-end success path — DOLT_PULL fails with the GH#3144 tracking
// error, and pullWithAutoResolve recovers via DOLT_FETCH + DOLT_MERGE — is
// covered by TestPullWithAutoResolve_BranchTrackingSuccess in the
// //go:build integration file (pull_branch_tracking_integration_test.go).
// That test stands up a real remotesapi Dolt server the local store can fetch
// from. The earlier default-build TestPullWithAutoResolve_BranchTrackingFallbackSuccess
// tried to reproduce it against the shared test server using a host file://
// remote, but the shared server (a Docker container in the default gate)
// cannot reach the host temp dir, so DOLT_FETCH always failed with
// `branch "main" not found on remote` and the test skipped 100% of the time —
// env-dependent dead coverage (beads-ellm) that still paid the shared-container
// + test-semaphore setup cost on every gate run. It is removed here; the
// integration twin is the authoritative success-path test.
