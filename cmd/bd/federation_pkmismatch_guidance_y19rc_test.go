//go:build cgo

package main

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
)

// TestFederationSyncPKMismatchGuidance_y19rc is the teeth for beads-y19rc, the
// dropped-leg sibling of beads-sa08u. `bd federation sync` printed a bare
// "✗ merge failed: ... different primary keys ..." with NO recovery guidance
// when a peer's history forked the primary key of a shared table (the #4259
// dependencies PK-reshape geometry: two towns upgrading bd independently across
// a PK-reshaping migration while un-synced edits existed on both sides).
//
// `bd dolt push/pull` classify the SAME Dolt error via isAncestorPKMismatchErr
// and print recovery guidance; sa08u added a federation branch for the
// diverged-history case (isDivergedHistoryErr) but NOT for the PK-mismatch case.
// The fix routes the PK-mismatch case through printFederationPKMismatchGuidance
// (federation-specific export/re-seed/import recovery, NOT dolt push/pull's
// bd-bootstrap / --force guidance, which is wrong for a peer↔peer fork where
// neither town is the other's origin).
//
// This drives the REAL runFederationSync path end-to-end into a GENUINE Dolt
// "different primary keys" merge refusal (not a mocked error): removing the
// `else if isAncestorPKMismatchErr(err) { printFederationPKMismatchGuidance }`
// branch makes the guidance vanish and this test goes RED — a pure output-only
// test of the helper would not (the veneer trap).
//
// Geometry (mirrors internal/storage/dolt cross_upgrade_merge_test scenario (b),
// minimized to the federation path). The refusal requires the merge BASE
// (common ancestor) PK to differ from BOTH heads' PK with real row diffs to
// reconcile — a one-sided reshape merges cleanly (Dolt adopts the changed
// side). So:
//
//	town A: init, create a dependency edge, add-peer file:// hub, sync
//	        → the hub now holds A's history C (dependencies at HEAD's id-PK).
//	        C is the shared ancestor / merge base.
//	town B: clone C, reshape dependencies PK to (id, issue_id), commit a
//	        straddling edge (→ b1), push b1 to the hub.
//	town A: reshape its OWN local main's dependencies PK identically, commit a
//	        DIFFERENT straddling edge (→ a1), then federation sync.
//	town A: sync fetches hub/main (b1) and merges it into a1. Merge base C is
//	        id-PK; both heads are (id,issue_id)-PK with divergent rows → Dolt
//	        refuses: "cannot merge because table dependencies has different
//	        primary keys ...".
func TestFederationSyncPKMismatchGuidance_y19rc(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt federation tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	remotePath := t.TempDir() + "/hub"
	remoteURL := "file://" + remotePath

	run := func(dir string, args ...string) (string, error) {
		t.Helper()
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	mustRun := func(dir string, args ...string) string {
		t.Helper()
		out, err := run(dir, args...)
		if err != nil {
			t.Fatalf("bd %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
		}
		return out
	}

	// ── Town A: init, seed two issues + a dependency edge, publish to the hub.
	// The edge lands in `dependencies` so the shared ancestor CARRIES a row
	// under this binary's HEAD PK shape (id). ──
	dirA, beadsDirA, _ := bdInit(t, bd, "--prefix", "towna")
	i1 := bdCreate(t, bd, dirA, "issue one", "-p", "1")
	i2 := bdCreate(t, bd, dirA, "issue two", "-p", "1")
	i3 := bdCreate(t, bd, dirA, "issue three", "-p", "1")
	// i1 blocks i2 → a dependencies row so the shared ancestor carries a real
	// dependencies row under this binary's HEAD (id) PK. `dep add <blocked>
	// <blocker>`: i2 depends on i1.
	mustRun(dirA, "dep", "add", i2.ID, i1.ID)
	mustRun(dirA, "federation", "add-peer", "hub", remoteURL)
	// First sync bootstraps the empty hub with A's history (beads-aapwu) — this
	// commit becomes the shared ancestor C (id-PK) between A and the hub/town B.
	mustRun(dirA, "federation", "sync", "--peer", "hub")

	// ── Town B: clone C from the hub, reshape its dependencies PK, add a
	// straddling edge (i3 → i1), push the fork (b1) back to the hub. ──
	forkPeerAndPush(t, remoteURL, i3.ID, i1.ID)

	// ── Town A: reshape its OWN main identically but with a DIVERGENT straddling
	// edge (i3 → i2, distinct unique key from B's i3 → i1), so the eventual merge
	// is a real two-sided PK divergence from the ancestor with conflicting rows,
	// not a one-sided schema adoption. A's dolt must be quiescent here (no bd
	// subprocess holding it); the OpenSQL handle is closed before the next bd
	// sync subprocess opens the same dir. ──
	reshapeLocalDepsPK(t, beadsDirA, "y19rc-straddle-a", i3.ID, i2.ID)

	// ── Town A syncs against the hub, which now carries B's PK-forked history.
	// Merge base C (id-PK) ≠ both heads (id,issue_id) with divergent rows →
	// "different primary keys" refusal. ──
	out, code := func() (string, int) {
		o, err := run(dirA, "federation", "sync", "--peer", "hub")
		c := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				c = ee.ExitCode()
			} else {
				t.Fatalf("federation sync exec error (not an exit error): %v\n%s", err, o)
			}
		}
		return o, c
	}()

	// Precondition: the merge must actually have been refused for a PK mismatch
	// (the scenario is valid). If Dolt merged cleanly or failed some other way,
	// the test's premise is wrong — skip loud rather than silently pass on a
	// non-PK-mismatch path (embedded PK surgery is environment-sensitive).
	lower := strings.ToLower(out)
	if !strings.Contains(lower, "different primary keys") {
		t.Skipf("y19rc precondition: expected a 'different primary keys' merge refusal, got (code=%d):\n%s", code, out)
	}

	// The fix: federation-specific PK-mismatch recovery guidance must print.
	wantPhrases := []string{
		"schema fork",             // names the condition
		"Recovery",                // actionable recovery section
		"bd export",               // save local-only issues (peer↔peer, not force-push)
		"bd import",               // re-seed from the canonical town
		"can no longer be merged", // sets the retry-won't-help expectation
	}
	for _, p := range wantPhrases {
		if !strings.Contains(out, p) {
			t.Errorf("y19rc: PK-mismatch federation sync must print recovery guidance containing %q; got:\n%s", p, out)
		}
	}

	// It must NOT reuse dolt push/pull's PK-mismatch guidance, which is wrong for
	// a peer↔peer federation fork (no single origin to force-push / adopt).
	for _, wrong := range []string{"bd bootstrap", "--force"} {
		if strings.Contains(out, wrong) {
			t.Errorf("y19rc: federation PK-mismatch guidance must NOT reuse dolt push/pull's %q (peer↔peer has no origin); got:\n%s", wrong, out)
		}
	}
}

// reshapeDepsPK reshapes the `dependencies` primary key away from HEAD's id-PK
// to the composite (id, issue_id), inserts a straddling edge, and commits — all
// on a single pinned connection (USE / ALTER / COMMIT are session/connection
// scoped). It is the shared surgery both the peer clone (town B) and the local
// town A apply so their merge heads share a NEW PK that differs from the
// id-PK common ancestor.
func reshapeDepsPK(t *testing.T, ctx context.Context, conn *sql.Conn, straddleID, issueID, dependsOn, msg string) {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := conn.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	// Superset reshape: HEAD's PK is the surrogate `id`; adding `issue_id` makes
	// the PK column SET differ from the ancestor while keeping every row valid
	// (id is already unique) and avoiding the generated `depends_on_id` column /
	// uk_dep_* unique indexes. FK checks off so the ALTER does not trip.
	exec("SET FOREIGN_KEY_CHECKS = 0")
	exec("ALTER TABLE dependencies DROP PRIMARY KEY")
	exec("ALTER TABLE dependencies ADD PRIMARY KEY (id, issue_id)")
	exec("SET FOREIGN_KEY_CHECKS = 1")
	// A straddling edit so there is a real row diff to reconcile across the PK
	// boundary (an empty diff can fast-forward past the conflict). Each side
	// commits a DIFFERENT edge id so the two heads genuinely diverge.
	exec("INSERT INTO dependencies (id, issue_id, depends_on_issue_id, type, created_at, created_by) "+
		"VALUES (?, ?, ?, 'related', '2020-01-02 00:00:00', 'y19rc')",
		straddleID, issueID, dependsOn)
	exec("CALL DOLT_COMMIT('-Am', ?)", msg)
}

// reshapeLocalDepsPK opens town A's OWN embedded dolt (the data dir is
// <beadsDir>/embeddeddolt; the db name is dbNameFromPrefix("towna")=="towna")
// on branch main and applies the reshape surgery, then closes the handle so a
// subsequent bd subprocess can reopen the same dir. A must be quiescent (no bd
// process open).
func reshapeLocalDepsPK(t *testing.T, beadsDir, straddleID, issueID, dependsOn string) {
	t.Helper()
	ctx := t.Context()
	dataDir := filepath.Join(beadsDir, "embeddeddolt")
	const dbName = "towna"
	sqldb, cleanup, err := embeddeddolt.OpenSQL(ctx, dataDir, dbName, "main")
	if err != nil {
		t.Fatalf("OpenSQL(town A %s db=%s): %v", dataDir, dbName, err)
	}
	defer func() { _ = cleanup() }()
	conn, err := sqldb.Conn(ctx)
	if err != nil {
		t.Fatalf("pin town A connection: %v", err)
	}
	defer func() { _ = conn.Close() }()
	reshapeDepsPK(t, ctx, conn, straddleID, issueID, dependsOn, "fork dependencies PK on town A (y19rc)")
}

// forkPeerAndPush clones the hub into a scratch embedded database (sharing the
// hub's history — a common ancestor with town A), reshapes the cloned
// `dependencies` primary key, commits a straddling dependency edge, and pushes
// the forked history back to the hub. This is town B's half of the minimized
// #4259 geometry.
//
// It uses the proven OpenSQL + DOLT_CLONE path (federation_bootstrap_aapwu_test
// / federation_filter_test) rather than `bd bootstrap`, whose CLI clone contract
// is not exercised here.
func forkPeerAndPush(t *testing.T, remoteURL, issueA, issueB string) {
	t.Helper()

	// Fresh scratch dir with NO database selected, then clone the hub into it.
	dir := filepath.Join(t.TempDir(), "peer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir scratch clone dir: %v", err)
	}
	db, cleanup, err := embeddeddolt.OpenSQL(t.Context(), dir, "", "main")
	if err != nil {
		t.Fatalf("OpenSQL(scratch clone dir): %v", err)
	}
	defer func() { _ = cleanup() }()

	ctx := t.Context()
	// Pin ONE physical connection: *sql.DB is a pool and `USE <db>` / DOLT_CLONE
	// select state is connection-scoped, so an unpinned pool would run the
	// post-clone ALTER on a different connection with no database selected
	// ("Error 1105: no database selected"). Mirrors withMutatingPinnedDBConn.
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("pin clone connection: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Clone the hub into an explicitly-named database (aapwu/filter form), select
	// it, then reshape + straddle + commit.
	const cloned = "peerclone"
	if _, err := conn.ExecContext(ctx, "CALL DOLT_CLONE(?, ?)", remoteURL, cloned); err != nil {
		t.Fatalf("DOLT_CLONE(%s): %v", remoteURL, err)
	}
	if _, err := conn.ExecContext(ctx, "USE `"+cloned+"`"); err != nil {
		t.Fatalf("USE %s: %v", cloned, err)
	}
	reshapeDepsPK(t, ctx, conn, "y19rc-straddle-b", issueA, issueB, "fork dependencies PK on town B (y19rc)")

	// Push the forked history to the hub. b1 descends from the hub's tip (the
	// clone's origin), so a plain push is a fast-forward; keep --force as a
	// fallback in case the clone's default remote/branch mapping differs.
	if _, err := conn.ExecContext(ctx, "CALL DOLT_PUSH(?, ?)", "origin", "main"); err != nil {
		if _, ferr := conn.ExecContext(ctx, "CALL DOLT_PUSH('--force', ?, ?)", "origin", "main"); ferr != nil {
			t.Fatalf("push forked history to hub (plain err: %v; --force err: %v)", err, ferr)
		}
	}
}
