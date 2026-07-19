//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"
)

// TestEmbeddedDeferNoOpHonest covers beads-fs01: `bd defer <id>` on an issue that
// is ALREADY status=deferred, with no new --until and no --reason, is an
// idempotent no-op and must report an honest "already deferred, no change"
// rather than a false "* Deferred" success. Sibling of the landed close
// already-closed (beads-dr3) / reopen already-open (beads-b0tw) already-in-state
// no-op guards. A re-defer WITH a new --until or --reason is a genuine change
// and must still succeed. Deterministic (server-free once the store exists);
// gated on the embedded-dolt env like its siblings.
func TestEmbeddedDeferNoOpHonest(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "df")

	t.Run("redefer_already_deferred_reports_no_change", func(t *testing.T) {
		iss := bdCreate(t, bd, dir, "Defer no-op A", "--type", "task")
		first := bdDefer(t, bd, dir, iss.ID)
		if !strings.Contains(first, "Deferred") {
			t.Fatalf("first defer should report Deferred: %s", first)
		}
		// Re-defer with no new payload: idempotent no-op — must NOT claim "* Deferred".
		second := bdDefer(t, bd, dir, iss.ID)
		if strings.Contains(second, "* Deferred") {
			t.Errorf("false success: re-deferring an already-deferred issue printed '* Deferred': %s", second)
		}
		if !strings.Contains(second, "no change") {
			t.Errorf("expected an 'already deferred ... no change' message on re-defer, got: %s", second)
		}
	})

	t.Run("redefer_with_new_reason_still_succeeds", func(t *testing.T) {
		iss := bdCreate(t, bd, dir, "Defer no-op B", "--type", "task")
		bdDefer(t, bd, dir, iss.ID)
		// A genuine re-defer carrying a new --reason IS a change (appends notes)
		// and must still report a real success, not a no-op.
		out := bdDefer(t, bd, dir, iss.ID, "--reason", "waiting on API")
		if !strings.Contains(out, "Deferred") || strings.Contains(out, "no change") {
			t.Errorf("re-defer with a new --reason should report a genuine 'Deferred', got: %s", out)
		}
	})

	t.Run("first_defer_of_open_issue_still_succeeds", func(t *testing.T) {
		iss := bdCreate(t, bd, dir, "Defer no-op C", "--type", "task")
		// A fresh open issue: the first defer is a real transition.
		out := bdDefer(t, bd, dir, iss.ID)
		if !strings.Contains(out, "Deferred") || strings.Contains(out, "no change") {
			t.Errorf("first defer of an open issue should report a genuine 'Deferred', got: %s", out)
		}
	})
}
