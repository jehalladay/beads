//go:build cgo

package main

import (
	"context"
	"strings"
	"testing"
)

// TestProxiedServerPromote is the teeth for the beads-aocj promote leg: `bd
// promote <wisp>` must WORK in proxied-server mode. Before the fix, promote used
// the direct nil `store` in proxiedServerMode (main.go PersistentPreRun returns
// early, before store init, once uowProvider is set) and never routed via
// usesProxiedServer() — so a hub-connected crew got "database not initialized".
// Mirrors the other aocj legs (label/assign/tag/comment/note) and the write
// precedent of beads-92ld (CloseIssue on the UOW IssueUseCase).
func TestProxiedServerPromote(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("happy_path_promotes_wisp_to_permanent", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pp")
		wisp := bdProxiedCreate(t, bd, p.dir, "Promote me", "--type", "task", "--ephemeral")
		if !wisp.Ephemeral {
			t.Fatalf("precondition: created issue should be ephemeral, got Ephemeral=%v", wisp.Ephemeral)
		}

		out, err := bdProxiedRun(t, bd, p.dir, "promote", wisp.ID)
		s := string(out)
		if err != nil {
			t.Fatalf("proxied promote failed: %v\n%s", err, s)
		}
		if strings.Contains(s, "storage is nil") || strings.Contains(s, "database not initialized") {
			t.Fatalf("proxied promote hit the nil-store path (beads-aocj regression): %s", s)
		}
		if !strings.Contains(s, "Promoted") {
			t.Errorf("expected '✓ Promoted' from proxied promote, got: %s", s)
		}

		// Persistence teeth: the wisp row must be gone and a permanent issue row
		// must exist — proving the proxied handler committed the UOW, not just
		// echoed success.
		db := openProxiedDB(t, p)
		var wispCount, issueCount int
		if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM wisps WHERE id = ?", wisp.ID).Scan(&wispCount); err != nil {
			t.Fatalf("query wisps: %v", err)
		}
		if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM issues WHERE id = ?", wisp.ID).Scan(&issueCount); err != nil {
			t.Fatalf("query issues: %v", err)
		}
		if wispCount != 0 {
			t.Errorf("after promote, wisps table still has %d rows for %s, want 0", wispCount, wisp.ID)
		}
		if issueCount != 1 {
			t.Errorf("after promote, issues table has %d rows for %s, want 1", issueCount, wisp.ID)
		}

		got := bdProxiedShow(t, bd, p.dir, wisp.ID)
		if got.Ephemeral {
			t.Errorf("after promote, %s still reports Ephemeral=true", wisp.ID)
		}
	})

	t.Run("reason_recorded_as_comment", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pp2")
		wisp := bdProxiedCreate(t, bd, p.dir, "Promote with reason", "--type", "task", "--ephemeral")

		out, err := bdProxiedRun(t, bd, p.dir, "promote", wisp.ID, "--reason", "Worth keeping")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied promote --reason failed: %v\n%s", err, s)
		}

		db := openProxiedDB(t, p)
		var commentCount int
		if err := db.QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM comments WHERE issue_id = ? AND text LIKE '%Worth keeping%'", wisp.ID).Scan(&commentCount); err != nil {
			t.Fatalf("query comments: %v", err)
		}
		if commentCount != 1 {
			t.Errorf("expected 1 promotion comment carrying the reason, found %d", commentCount)
		}
	})

	t.Run("not_a_wisp_is_rejected", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pp3")
		perm := bdProxiedCreate(t, bd, p.dir, "Already permanent", "--type", "task")

		out, err := bdProxiedRun(t, bd, p.dir, "promote", perm.ID)
		s := string(out)
		if err == nil {
			t.Fatalf("proxied promote of a permanent issue should fail, got success:\n%s", s)
		}
		if !strings.Contains(s, "not a wisp") {
			t.Errorf("expected 'not a wisp' rejection, got: %s", s)
		}
	})

	t.Run("missing_id_is_rejected", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pp4")

		out, err := bdProxiedRun(t, bd, p.dir, "promote", "pp4-nonexistent")
		s := string(out)
		if err == nil {
			t.Fatalf("proxied promote of a nonexistent id should fail, got success:\n%s", s)
		}
		if strings.Contains(s, "storage is nil") || strings.Contains(s, "database not initialized") {
			t.Fatalf("proxied promote hit the nil-store path instead of a clean not-found error: %s", s)
		}
	})
}
