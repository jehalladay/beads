//go:build cgo

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// runBatchScriptInTxWithSession mirrors runBatchScriptInTx but threads a
// closing session into runBatchOp, exactly as batchCmd.RunE does after
// resolving --session / $CLAUDE_SESSION_ID (beads-szy1h).
func runBatchScriptInTxWithSession(t *testing.T, ctx context.Context, st storage.DoltStorage, script, session string) error {
	t.Helper()
	ops, err := parseBatchScript(strings.NewReader(script))
	if err != nil {
		return err
	}
	return st.RunInTransaction(ctx, "test: bd batch session", func(tx storage.Transaction) error {
		for _, op := range ops {
			if _, err := runBatchOp(ctx, tx, op, session); err != nil {
				return err
			}
		}
		return nil
	})
}

// TestBatch_ClosedBySession_szy1h proves the two batch close legs
// (close, update status=closed) stamp closed_by_session from the resolved
// session — parity with bd close (close.go:196) and bd update --status closed
// (update.go:106-114). Before beads-szy1h both legs passed session="" and
// never read the session, silently dropping closer-session provenance on the
// loop→batch refactor the command exists to enable.
func TestBatch_ClosedBySession_szy1h(t *testing.T) {
	tmpDir := t.TempDir()
	st := newTestStoreWithPrefix(t, filepath.Join(tmpDir, ".beads", "beads.db"), "sy")
	ctx := context.Background()

	seedBatchTestIssues(t, ctx, st, "sy-1", "sy-2")

	const sess = "sess-szy1h-123"
	script := "close sy-1 done in batch\nupdate sy-2 status=closed\n"
	if err := runBatchScriptInTxWithSession(t, ctx, st, script, sess); err != nil {
		t.Fatalf("batch run with session: %v", err)
	}

	// close leg
	got1, err := st.GetIssue(ctx, "sy-1")
	if err != nil {
		t.Fatalf("GetIssue sy-1: %v", err)
	}
	if got1.Status != types.StatusClosed {
		t.Fatalf("sy-1 status = %q, want closed", got1.Status)
	}
	if got1.ClosedBySession != sess {
		t.Errorf("batch close leg: sy-1 ClosedBySession = %q, want %q (session dropped — beads-szy1h)", got1.ClosedBySession, sess)
	}

	// update status=closed leg
	got2, err := st.GetIssue(ctx, "sy-2")
	if err != nil {
		t.Fatalf("GetIssue sy-2: %v", err)
	}
	if got2.Status != types.StatusClosed {
		t.Fatalf("sy-2 status = %q, want closed", got2.Status)
	}
	if got2.ClosedBySession != sess {
		t.Errorf("batch update status=closed leg: sy-2 ClosedBySession = %q, want %q (session dropped — beads-szy1h)", got2.ClosedBySession, sess)
	}
}

// TestBatch_ClosedBySession_EmptySessionUnchanged_szy1h confirms the fix is a
// no-op when no session is present: closed_by_session stays empty, matching
// bd close / bd update --status closed run without a session (the injection is
// gated on session != "", update.go:111-113). Guards against a regression that
// would stamp a spurious value.
func TestBatch_ClosedBySession_EmptySessionUnchanged_szy1h(t *testing.T) {
	tmpDir := t.TempDir()
	st := newTestStoreWithPrefix(t, filepath.Join(tmpDir, ".beads", "beads.db"), "se")
	ctx := context.Background()

	seedBatchTestIssues(t, ctx, st, "se-1", "se-2")

	script := "close se-1 done\nupdate se-2 status=closed\n"
	if err := runBatchScriptInTxWithSession(t, ctx, st, script, ""); err != nil {
		t.Fatalf("batch run empty session: %v", err)
	}

	for _, id := range []string{"se-1", "se-2"} {
		got, err := st.GetIssue(ctx, id)
		if err != nil {
			t.Fatalf("GetIssue %s: %v", id, err)
		}
		if got.Status != types.StatusClosed {
			t.Fatalf("%s status = %q, want closed", id, got.Status)
		}
		if got.ClosedBySession != "" {
			t.Errorf("%s ClosedBySession = %q, want empty (no session provided)", id, got.ClosedBySession)
		}
	}
}
