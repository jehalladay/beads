//go:build cgo

package embeddeddolt_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/versioncontrolops"
)

// configCommittedClean asserts the config table has no uncommitted changes —
// i.e. the preceding commit actually staged config. This is the embedded-engine
// check for the pull config wedge: persistent memories live in config as
// kv.memory.* rows, and a commit that fails to stage config leaves the working
// set permanently dirty so DOLT_MERGE refuses to start.
func configCommittedClean(t *testing.T, ctx context.Context, conn *sql.Conn) {
	t.Helper()
	var n int
	if err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM dolt_status WHERE table_name = 'config'").Scan(&n); err != nil {
		t.Fatalf("query dolt_status: %v", err)
	}
	if n > 0 {
		t.Fatalf("config still dirty after commit: the embedded commit path does not stage config, so pulls will wedge")
	}
}

// TestEmbeddedConfigCommitStagesConfig documents the embedded engine's commit
// behavior for config: the embedded store commits via DOLT_COMMIT('-Am') (see
// EmbeddedDoltStore.Commit), and the pre-pull auto-commit relies on that to
// stage memory rows. If this fails, the embedded pre-pull path needs the same
// explicit-config-staging fix the server-mode path got.
func TestEmbeddedConfigCommitStagesConfig(t *testing.T) {
	te := newTestEnv(t, "cfgstage")
	ctx := t.Context()
	conn := openSettleConn(t, ctx, te)

	if _, err := conn.ExecContext(ctx,
		"INSERT INTO config (`key`, value) VALUES ('kv.memory.stage-check', 'v1')"); err != nil {
		t.Fatalf("insert config memory row: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "CALL DOLT_COMMIT('-Am', 'commit memory')"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	configCommittedClean(t, ctx, conn)
}

// TestEmbeddedMergeAndSettleMemoryConfigConflict is the embedded-engine
// counterpart of TestPullAutoResolveMemoryConfigConflicts: a merge conflict
// limited to kv.memory.* config rows is auto-resolved with "theirs", so two
// clones that edited the same memory converge instead of wedging the pull.
func TestEmbeddedMergeAndSettleMemoryConfigConflict(t *testing.T) {
	te := newTestEnv(t, "cfgsettle")
	ctx := t.Context()
	conn := openSettleConn(t, ctx, te)

	if _, err := conn.ExecContext(ctx,
		"INSERT INTO config (`key`, value) VALUES ('kv.memory.shared', 'ours')"); err != nil {
		t.Fatalf("insert config on main: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "CALL DOLT_COMMIT('-Am', 'local memory')"); err != nil {
		t.Fatalf("commit on main: %v", err)
	}
	// Guard the setup: if -Am did not stage config, no conflict would form and
	// the test would pass vacuously.
	configCommittedClean(t, ctx, conn)

	if _, err := conn.ExecContext(ctx, "CALL DOLT_BRANCH('cfgpeer', 'HEAD~1')"); err != nil {
		t.Fatalf("create peer branch: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "CALL DOLT_CHECKOUT('cfgpeer')"); err != nil {
		t.Fatalf("checkout peer: %v", err)
	}
	if _, err := conn.ExecContext(ctx,
		"INSERT INTO config (`key`, value) VALUES ('kv.memory.shared', 'theirs')"); err != nil {
		t.Fatalf("insert config on peer: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "CALL DOLT_COMMIT('-Am', 'peer memory')"); err != nil {
		t.Fatalf("commit on peer: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "CALL DOLT_CHECKOUT('main')"); err != nil {
		t.Fatalf("checkout main: %v", err)
	}

	if err := versioncontrolops.MergeAndSettle(ctx, conn, "cfgpeer"); err != nil {
		t.Fatalf("settling memory-config-conflicted merge: %v", err)
	}

	var value string
	if err := conn.QueryRowContext(ctx,
		"SELECT value FROM config WHERE `key` = 'kv.memory.shared'").Scan(&value); err != nil {
		t.Fatalf("read resolved config: %v", err)
	}
	if value != "theirs" {
		t.Errorf("resolved memory value = %q, want \"theirs\" (--theirs convergent)", value)
	}
}

// TestEmbeddedMergeAndSettleSkipsNonMemoryConfigConflict verifies the prefix
// boundary: a config key under kv. but NOT kv.memory. is a real semantic
// conflict, so the resolver declines the whole config table and the merge is
// surfaced to the operator as a MergeConflictsError naming config.
func TestEmbeddedMergeAndSettleSkipsNonMemoryConfigConflict(t *testing.T) {
	te := newTestEnv(t, "cfgskip")
	ctx := t.Context()
	conn := openSettleConn(t, ctx, te)

	if _, err := conn.ExecContext(ctx,
		"INSERT INTO config (`key`, value) VALUES ('kv.custom.setting', 'ours')"); err != nil {
		t.Fatalf("insert config on main: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "CALL DOLT_COMMIT('-Am', 'local config')"); err != nil {
		t.Fatalf("commit on main: %v", err)
	}
	configCommittedClean(t, ctx, conn)

	if _, err := conn.ExecContext(ctx, "CALL DOLT_BRANCH('cfgskippeer', 'HEAD~1')"); err != nil {
		t.Fatalf("create peer branch: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "CALL DOLT_CHECKOUT('cfgskippeer')"); err != nil {
		t.Fatalf("checkout peer: %v", err)
	}
	if _, err := conn.ExecContext(ctx,
		"INSERT INTO config (`key`, value) VALUES ('kv.custom.setting', 'theirs')"); err != nil {
		t.Fatalf("insert config on peer: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "CALL DOLT_COMMIT('-Am', 'peer config')"); err != nil {
		t.Fatalf("commit on peer: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "CALL DOLT_CHECKOUT('main')"); err != nil {
		t.Fatalf("checkout main: %v", err)
	}

	err := versioncontrolops.MergeAndSettle(ctx, conn, "cfgskippeer")
	var mce *versioncontrolops.MergeConflictsError
	if !errors.As(err, &mce) {
		t.Fatalf("want MergeConflictsError for non-memory config conflict, got: %v", err)
	}
	foundConfig := false
	for _, c := range mce.Conflicts {
		if c.Field == "config" {
			foundConfig = true
		}
	}
	if !foundConfig {
		t.Errorf("captured conflicts %+v do not name the config table", mce.Conflicts)
	}
	// The local value must survive the aborted merge.
	var value string
	if err := conn.QueryRowContext(ctx,
		"SELECT value FROM config WHERE `key` = 'kv.custom.setting'").Scan(&value); err != nil {
		t.Fatalf("read config after abort: %v", err)
	}
	if value != "ours" {
		t.Errorf("local config value = %q after aborted merge, want %q", value, "ours")
	}
}

// statusHasConfig reports whether the config table has staged or unstaged
// working-set changes, observed through the store's own connection — the same
// session Sync commits on. A separate OpenSQL handle is avoided on purpose:
// embedded uncommitted working-set writes are not reliably visible across
// handles until shutdown-flush, so the dirty precondition must be read here.
func statusHasConfig(t *testing.T, ctx context.Context, te *testEnv) bool {
	t.Helper()
	st, err := te.store.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	for _, e := range st.Staged {
		if e.Table == "config" {
			return true
		}
	}
	for _, e := range st.Unstaged {
		if e.Table == "config" {
			return true
		}
	}
	return false
}

// TestEmbeddedSyncCommitsConfigBeforeMerge is the embedded-engine counterpart of
// server-mode TestFederationSyncCommitsConfigBeforeFetch: EmbeddedDoltStore.Sync
// must auto-commit pending changes before its merge, exactly as embedded
// Pull/PullRemote/PullFrom already do. Persistent memories live in config as
// kv.memory.* rows that `bd remember` leaves uncommitted; without the pre-merge
// CommitPending, a dirty config working set survives into DOLT_MERGE and wedges
// `bd federation sync` ("cannot merge with uncommitted changes"). This test pins
// that the config working set is clean once Sync has run — proving the pre-merge
// commit staged it.
//
// beads-aapwu: a file:// remote that has never been seeded is the fresh/empty
// peer case, NOT a hard error — DOLT_FETCH auto-creates the remote dir and
// succeeds, then the peer/main merge fails "branch not found", which Sync now
// treats as the bootstrap case and publishes to (Pushed=true, err=nil). So this
// test no longer asserts an early Sync failure (that proxy was never accurate —
// fetch succeeded, only the merge failed); it asserts the real GH#2474 invariant
// directly: config is committed, not left dirty.
func TestEmbeddedSyncCommitsConfigBeforeMerge(t *testing.T) {
	te := newTestEnv(t, "syncwedge")
	ctx := t.Context()

	// Register a fresh (never-seeded) file peer: Sync runs its pre-merge
	// auto-commit, fetches, then bootstrap-publishes to the empty peer. The
	// remote lives under t.TempDir() so it is genuinely empty on EVERY run:
	// a fixed shared path (e.g. /tmp/...) would be SEEDED by the first run's
	// bootstrap push, and the next run's merge would then fail "no common
	// ancestor" instead of exercising the empty-peer bootstrap (beads-aapwu).
	peer := &storage.FederationPeer{
		Name:        "peer-sync-wedge",
		RemoteURL:   "file://" + filepath.Join(t.TempDir(), "freshpeer"),
		Sovereignty: "T2",
	}
	if err := te.store.AddFederationPeer(ctx, peer); err != nil {
		t.Fatalf("add federation peer: %v", err)
	}
	// Commit the peer registration so the only dirty table going into Sync is the
	// config row seeded below — the precise `bd remember` wedge precondition.
	if err := te.store.Commit(ctx, "register sync peer"); err != nil {
		t.Fatalf("commit peer registration: %v", err)
	}

	// Simulate `bd remember`: a kv.memory.* config write, left uncommitted.
	if err := te.store.SetConfig(ctx, "kv.memory.sync-wedge", "v1"); err != nil {
		t.Fatalf("SetConfig memory row: %v", err)
	}
	if !statusHasConfig(t, ctx, te) {
		t.Fatal("memory write did not dirty config; cannot reproduce the wedge precondition")
	}

	// Sync must NOT wedge on the dirty config: the pre-merge CommitPending stages
	// it, so DOLT_MERGE never sees "cannot merge with uncommitted changes". A
	// fresh empty peer bootstrap-publishes rather than erroring (beads-aapwu).
	if _, err := te.store.Sync(ctx, peer.Name, ""); err != nil {
		t.Fatalf("Sync wedged/failed instead of committing config before merge (GH#2474): %v", err)
	}

	if statusHasConfig(t, ctx, te) {
		t.Fatal("embedded Sync left config dirty before merge; bd federation sync would wedge DOLT_MERGE (GH#2474)")
	}
}
