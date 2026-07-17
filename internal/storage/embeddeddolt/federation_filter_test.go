//go:build cgo

package embeddeddolt

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage/versioncontrolops"
	"github.com/steveyegge/beads/internal/types"
)

// openFilterTestStore opens an initialized embedded store in a temp dir with a
// committed baseline, for the federation exclude-types filter tests (beads-t129).
func openFilterTestStore(t *testing.T) (*EmbeddedDoltStore, context.Context) {
	t.Helper()
	ctx := context.Background()
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	store, err := Open(ctx, beadsDir, "beads", "main")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SetConfig(ctx, "issue_prefix", "beads"); err != nil {
		t.Fatalf("SetConfig(issue_prefix): %v", err)
	}
	if err := store.Commit(ctx, "bd init"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return store, ctx
}

// TestEmbeddedFilteredPushExcludesWisp is the beads-t129 regression: the
// federation.exclude_types PRIVACY filter (beads-lgda) existed only on the
// server DoltStore path; EmbeddedDoltStore.Sync pushed UNFILTERED (a plain
// PushTo), leaking excluded/private types to the peer on every sync. This
// verifies the embedded backend now runs the same fail-CLOSED staging-branch
// filter: the excluded type is dropped from what would be pushed, the staging
// branch is always cleaned up, and the original branch is left intact.
func TestEmbeddedFilteredPushExcludesWisp(t *testing.T) {
	store, ctx := openFilterTestStore(t)

	// A regular task (must survive filtering).
	task := &types.Issue{
		ID:        "fed-filter-task",
		Title:     "Regular task",
		IssueType: types.TypeTask,
		Status:    types.StatusOpen,
		Priority:  1,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := store.CreateIssue(ctx, task, "test"); err != nil {
		t.Fatalf("create task: %v", err)
	}

	// An ephemeral issue inserted directly into the committed issues table —
	// simulating the edge case where an ephemeral (wisp) row leaks into
	// committed data. (CreateIssue routes ephemeral rows to the dolt_ignore'd
	// wisps table, so a raw INSERT is required to exercise the filter, matching
	// the server backend's test.) The filter must exclude it from the push.
	insertEphemeralIssue(t, store, ctx, "fed-filter-wisp", "Leaked wisp")
	if err := store.Commit(ctx, "create test issues"); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Both rows exist in issues before filtering.
	assertIssueCount(t, store, ctx, "fed-filter-task", 1)
	assertIssueCount(t, store, ctx, "fed-filter-wisp", 1)

	// filteredPushToPeer with a non-empty exclude list runs the staging-branch
	// filter, then attempts the push (which fails: the peer does not exist).
	// The push failure is expected; we assert the filter machinery around it.
	pushErr := store.filteredPushToPeer(ctx, "nonexistent-peer", []string{"wisp"})
	if pushErr == nil {
		t.Fatal("expected push error for nonexistent peer")
	}

	// The staging branch must always be cleaned up.
	branches, err := store.ListBranches(ctx)
	if err != nil {
		t.Fatalf("list branches: %v", err)
	}
	for _, b := range branches {
		if b == federationStagingBranch {
			t.Errorf("staging branch %s was not cleaned up", federationStagingBranch)
		}
	}

	// The original branch is non-destructive: both issues still present.
	assertIssueCount(t, store, ctx, "fed-filter-task", 1)
	assertIssueCount(t, store, ctx, "fed-filter-wisp", 1)
}

// TestEmbeddedFilteredPushOptOut verifies that an empty exclude list disables
// filtering (delegates directly to PushTo, no staging branch), matching the
// server backend's opt-out behavior.
func TestEmbeddedFilteredPushOptOut(t *testing.T) {
	store, ctx := openFilterTestStore(t)

	insertEphemeralIssue(t, store, ctx, "fed-optout-wisp", "Wisp for opt-out test")
	if err := store.Commit(ctx, "create ephemeral issue"); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Empty exclude list => direct PushTo, no staging branch. Push fails (no
	// remote), but no staging branch may be created.
	pushErr := store.filteredPushToPeer(ctx, "nonexistent-peer", []string{})
	if pushErr == nil {
		t.Fatal("expected push error for nonexistent peer")
	}

	branches, err := store.ListBranches(ctx)
	if err != nil {
		t.Fatalf("list branches: %v", err)
	}
	for _, b := range branches {
		if b == federationStagingBranch {
			t.Errorf("staging branch should not exist when exclude_types is empty")
		}
	}
}

// TestEmbeddedFilteredPushRoundTripOmitsExcluded is the decisive teeth for
// beads-t129: it pushes to a real (file://) peer and clones the peer back to
// prove the excluded type never reached the wire. With the fix reverted
// (Sync/filteredPushToPeer pushing the unfiltered branch), the cloned peer
// would contain the wisp — this asserts it does not, while the non-excluded
// task does.
func TestEmbeddedFilteredPushRoundTripOmitsExcluded(t *testing.T) {
	store, ctx := openFilterTestStore(t)

	// Regular task (must reach the peer) + leaked ephemeral wisp (must NOT).
	task := &types.Issue{
		ID: "rt-task", Title: "Regular task", IssueType: types.TypeTask,
		Status: types.StatusOpen, Priority: 1, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := store.CreateIssue(ctx, task, "test"); err != nil {
		t.Fatalf("create task: %v", err)
	}
	insertEphemeralIssue(t, store, ctx, "rt-wisp", "Leaked wisp")
	if err := store.Commit(ctx, "create round-trip issues"); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// A file:// peer remote the push can actually reach.
	remotePath := filepath.Join(t.TempDir(), "peer")
	if err := store.AddRemote(ctx, "peer", "file://"+remotePath); err != nil {
		t.Fatalf("add peer remote: %v", err)
	}

	if err := store.filteredPushToPeer(ctx, "peer", []string{"wisp"}); err != nil {
		t.Fatalf("filteredPushToPeer to file peer: %v", err)
	}

	// Clone the peer back into a fresh database and inspect what actually landed.
	cloneDir := filepath.Join(t.TempDir(), "clone")
	if err := store.withMutatingPinnedDBConn(ctx, func(db versioncontrolops.DBConn) error {
		_, err := db.ExecContext(ctx, "CALL DOLT_CLONE(?, ?)", "file://"+remotePath, "peerclone")
		return err
	}); err != nil {
		t.Fatalf("clone peer back: %v", err)
	}
	_ = cloneDir

	var taskOnPeer, wispOnPeer int
	if err := store.withMutatingPinnedDBConn(ctx, func(db versioncontrolops.DBConn) error {
		if err := db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM peerclone.issues WHERE id = ?", "rt-task").Scan(&taskOnPeer); err != nil {
			return err
		}
		return db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM peerclone.issues WHERE id = ?", "rt-wisp").Scan(&wispOnPeer)
	}); err != nil {
		t.Fatalf("inspect cloned peer: %v", err)
	}

	if taskOnPeer != 1 {
		t.Errorf("regular task did not reach peer: count=%d, want 1", taskOnPeer)
	}
	if wispOnPeer != 0 {
		t.Errorf("PRIVACY LEAK: excluded wisp reached the peer: count=%d, want 0", wispOnPeer)
	}
}

// TestEmbeddedSyncStatusReturnsPartialOnError is the beads-628e regression:
// SyncStatus must return a non-nil (partial) status even when the underlying
// connection fails, mirroring the server DoltStore.SyncStatus contract. Before
// the fix it returned (nil, err) on a withDBConn failure, and callers that
// ignore the error (cmd/bd/federation.go runFederationStatus `status, _ := ...`)
// then nil-derefed status.Peer in the render loop and panicked.
func TestEmbeddedSyncStatusReturnsPartialOnError(t *testing.T) {
	store, ctx := openFilterTestStore(t)

	// Force the withDBConn path to fail: a closed store makes withDBConn return
	// errClosed. A valid peer name keeps remoteRef valid so we reach that path
	// (an invalid ref would take the early ValidateRef branch instead).
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	status, err := store.SyncStatus(ctx, "peer")
	if err == nil {
		t.Fatal("SyncStatus on a closed store = nil error, want a surfaced error")
	}
	if status == nil {
		t.Fatal("SyncStatus returned a NIL status on error — regression: callers that ignore the error nil-deref/panic (beads-628e)")
	}
	if status.Peer != "peer" {
		t.Errorf("partial status Peer = %q, want %q", status.Peer, "peer")
	}
	if status.LocalAhead != -1 || status.LocalBehind != -1 {
		t.Errorf("partial status ahead/behind = %d/%d, want -1/-1 (unknown)", status.LocalAhead, status.LocalBehind)
	}
}

// insertEphemeralIssue inserts an ephemeral (wisp) row directly into the
// committed issues table. description/design/acceptance_criteria/notes are
// TEXT NOT NULL with no default (migrations/0001), so the raw INSERT must
// supply them (beads-v3rq).
func insertEphemeralIssue(t *testing.T, store *EmbeddedDoltStore, ctx context.Context, id, title string) {
	t.Helper()
	if err := store.withConn(ctx, true, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO issues
			(id, title, description, design, acceptance_criteria, notes, issue_type, status, priority, ephemeral, created_at, updated_at)
			VALUES (?, ?, '', '', '', '', ?, ?, ?, 1, NOW(), NOW())`,
			id, title, "task", "open", 1)
		return err
	}); err != nil {
		t.Fatalf("insert ephemeral issue %q: %v", id, err)
	}
}

// assertIssueCount asserts that exactly want rows with the given id exist on the
// store's current branch.
func assertIssueCount(t *testing.T, store *EmbeddedDoltStore, ctx context.Context, id string, want int) {
	t.Helper()
	var got int
	if err := store.withConn(ctx, false, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM issues WHERE id = ?", id).Scan(&got)
	}); err != nil {
		t.Fatalf("count issue %q: %v", id, err)
	}
	if got != want {
		t.Errorf("issue %q count = %d, want %d", id, got, want)
	}
}
