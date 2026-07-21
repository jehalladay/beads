//go:build cgo

package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-50dto (PROXIED leg): the proxied `bd update --status open` path
// (checkProxiedUpdateCloseGuards) wired only the closed-epic-parent reopen guard,
// dropping the supersede (8sjb3) and duplicate (8nugc) reopen guards the proxied
// `bd reopen` enforces (reopen_proxied_server.go). So a hub-connected crew could
// reopen a superseded/duplicate issue via update, making it reappear in
// `bd ready`. Runs end-to-end through the proxied-server subprocess
// (BEADS_TEST_PROXIED_SERVER=1). MUTATION-VERIFIED: removing either guard call in
// checkProxiedUpdateCloseGuards lets the proxied update reopen the issue.
func TestProxiedUpdateOpenReopenGuards_50dto(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("update_open_superseded_refused", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pus")
		old := bdProxiedCreate(t, bd, p.dir, "sup old", "--type", "task")
		nw := bdProxiedCreate(t, bd, p.dir, "sup new", "--type", "task")
		if out, err := bdProxiedRun(t, bd, p.dir, "supersede", old.ID, "--with", nw.ID); err != nil {
			t.Fatalf("proxied supersede failed: %v\n%s", err, out)
		}

		out, err := bdProxiedRun(t, bd, p.dir, "update", old.ID, "--status", "open")
		if err == nil {
			t.Fatalf("expected proxied update --status open of a superseded issue to FAIL, got rc=0:\n%s", out)
		}
		if !strings.Contains(string(out), "superseded by") {
			t.Errorf("expected a 'superseded by' guard error, got:\n%s", out)
		}
		if got := bdProxiedShow(t, bd, p.dir, old.ID); got.Status != types.StatusClosed {
			t.Errorf("proxied update --status open on a superseded issue must leave it CLOSED (50dto), got %s", got.Status)
		}
	})

	t.Run("update_open_duplicate_refused", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pud")
		dup := bdProxiedCreate(t, bd, p.dir, "dup old", "--type", "task")
		canonical := bdProxiedCreate(t, bd, p.dir, "dup canonical", "--type", "task")
		if out, err := bdProxiedRun(t, bd, p.dir, "duplicate", dup.ID, "--of", canonical.ID); err != nil {
			t.Fatalf("proxied duplicate failed: %v\n%s", err, out)
		}

		out, err := bdProxiedRun(t, bd, p.dir, "update", dup.ID, "--status", "open")
		if err == nil {
			t.Fatalf("expected proxied update --status open of a duplicate issue to FAIL, got rc=0:\n%s", out)
		}
		if !strings.Contains(string(out), "duplicate of") {
			t.Errorf("expected a 'duplicate of' guard error, got:\n%s", out)
		}
		if got := bdProxiedShow(t, bd, p.dir, dup.ID); got.Status != types.StatusClosed {
			t.Errorf("proxied update --status open on a duplicate issue must leave it CLOSED (50dto), got %s", got.Status)
		}
	})

	// --force override parity + regression (plain closed reopens).
	t.Run("update_open_force_overrides", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "puf")
		old := bdProxiedCreate(t, bd, p.dir, "sup old force", "--type", "task")
		nw := bdProxiedCreate(t, bd, p.dir, "sup new force", "--type", "task")
		if out, err := bdProxiedRun(t, bd, p.dir, "supersede", old.ID, "--with", nw.ID); err != nil {
			t.Fatalf("proxied supersede failed: %v\n%s", err, out)
		}
		if out, err := bdProxiedRun(t, bd, p.dir, "update", old.ID, "--status", "open", "--force"); err != nil {
			t.Fatalf("proxied update --status open --force should succeed: %v\n%s", err, out)
		}
		if got := bdProxiedShow(t, bd, p.dir, old.ID); got.Status != types.StatusOpen {
			t.Errorf("proxied --force should reopen the superseded issue, got %s", got.Status)
		}
	})
}
