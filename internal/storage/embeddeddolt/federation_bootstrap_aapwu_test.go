//go:build cgo

package embeddeddolt

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage/versioncontrolops"
	"github.com/steveyegge/beads/internal/types"
)

// TestEmbeddedSyncBootstrapsFreshEmptyPeer is the beads-aapwu regression: the
// documented federation onboarding (add-peer -> sync) must be able to PUBLISH
// the local town to a peer that starts EMPTY. Before the fix, Sync's Step 3
// merge of the remote-tracking branch (peer/main) hard-failed with
// "branch not found: peer/main" — because a fresh peer has no branch yet — and
// returned BEFORE Step 5's push that would have created it, a chicken-and-egg
// bootstrap deadlock (the push is gated behind a merge that cannot succeed
// until the push has already run). The fix treats a "branch not found" merge
// error as the bootstrap case (nothing to merge yet) and falls through to the
// push. This exercises the real Sync entry point against a fresh file:// peer,
// which federation_filter_test.go never did (it pushed via filteredPushToPeer
// directly, bypassing Sync).
func TestEmbeddedSyncBootstrapsFreshEmptyPeer(t *testing.T) {
	store, ctx := openFilterTestStore(t)

	// A local issue we expect to publish to the fresh peer.
	task := &types.Issue{
		ID: "aapwu-task", Title: "Bootstrap task", IssueType: types.TypeTask,
		Status: types.StatusOpen, Priority: 1, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := store.CreateIssue(ctx, task, "test"); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := store.Commit(ctx, "create bootstrap issue"); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// A file:// peer remote that exists but has never been seeded — the normal
	// state of a peer immediately after `bd federation add-peer`.
	remotePath := filepath.Join(t.TempDir(), "freshpeer")
	if err := store.AddRemote(ctx, "hub", "file://"+remotePath); err != nil {
		t.Fatalf("add peer remote: %v", err)
	}

	// The documented first sync of an empty peer must PUBLISH, not hard-fail.
	result, err := store.Sync(ctx, "hub", "")
	if err != nil {
		t.Fatalf("Sync against a fresh empty peer failed (bootstrap deadlock, beads-aapwu): %v", err)
	}
	if !result.Fetched {
		t.Errorf("expected Fetched=true against fresh peer, got false")
	}
	if !result.Pushed {
		t.Errorf("bootstrap publish did not happen: Pushed=false, PushError=%v (beads-aapwu)", result.PushError)
	}

	// Prove the local town actually reached the peer: clone it back and look
	// for the local issue.
	if err := store.withMutatingPinnedDBConn(ctx, func(db versioncontrolops.DBConn) error {
		_, err := db.ExecContext(ctx, "CALL DOLT_CLONE(?, ?)", "file://"+remotePath, "bootclone")
		return err
	}); err != nil {
		t.Fatalf("clone bootstrapped peer back: %v (the push never created the peer's main branch)", err)
	}

	var taskOnPeer int
	if err := store.withMutatingPinnedDBConn(ctx, func(db versioncontrolops.DBConn) error {
		return db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM bootclone.issues WHERE id = ?", "aapwu-task").Scan(&taskOnPeer)
	}); err != nil {
		t.Fatalf("inspect bootstrapped peer: %v", err)
	}
	if taskOnPeer != 1 {
		t.Errorf("local issue did not reach the bootstrapped peer: count=%d, want 1 (beads-aapwu)", taskOnPeer)
	}
}
