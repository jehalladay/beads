//go:build cgo

package main

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-kpwl9 (PROXIED undefer parity for WISP targets).
//
// undeferProxiedOne (cmd/bd/undefer_proxied_server.go) guarded existence with a
// bare `issueUC.GetIssue(id)` — the ISSUES table only — so `bd undefer <wisp>`
// on an ephemeral WISP target was rejected ("Error resolving <wisp>") for
// hub-connected (proxied, store==nil) crew. Meanwhile `defer`'s proxied handler
// resolves wisp-aware (proxiedResolveIssueOrWisp) and writes via ApplyUpdate
// (routes on isWispID) → a wisp CAN be deferred in proxied mode, then got stuck
// undeferrable. The DIRECT path (undefer.go → ResolvePartialID) undefers wisps,
// so proxied was at a parity deficit. Fix: resolve issue-or-wisp via
// proxiedGetIssueOrWisp; the write leg (ApplyUpdate) was already wisp-aware, so
// only the guard needed the fallback.
//
// Driven END-TO-END through the ACTUAL proxied `bd` subprocess and asserted
// against the WISP table directly (a UOW-level helper would false-green by
// skipping the CLI resolve/guard plumbing). MUTATION-VERIFIED: revert the
// proxiedGetIssueOrWisp guard back to the bare GetIssue and undefer_on_wisp
// goes RED (guard rejects the wisp).
func TestProxiedUndeferWisp_kpwl9(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("undefer_on_wisp_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uw")
		// A deferred WISP: defer's proxied handler is wisp-aware, so this lands
		// with status=deferred in the wisps table (the stuck state undefer must
		// clear).
		wisp := bdProxiedCreate(t, bd, p.dir, "Deferred wisp", "--ephemeral", "--defer", "+8760h")

		// undefer must SUCCEED on the wisp (the bare guard rejected it before).
		if out, err := bdProxiedRun(t, bd, p.dir, "undefer", wisp.ID); err != nil {
			t.Fatalf("REGRESSION (beads-kpwl9): proxied `bd undefer` on WISP %s failed (defer + the direct path support wisps): %v\n%s", wisp.ID, err, out)
		}

		// Status must be OPEN in the WISP table — proves the resolve reached the
		// wisp and the (already wisp-aware) ApplyUpdate cleared its deferred state.
		db := openProxiedDB(t, p)
		var status string
		if err := db.QueryRowContext(context.Background(),
			"SELECT status FROM wisps WHERE id = ?", wisp.ID).Scan(&status); err != nil {
			t.Fatalf("read wisp status: %v", err)
		}
		if types.Status(status) != types.StatusOpen {
			t.Errorf("REGRESSION (beads-kpwl9): undefer on WISP %s did not clear deferred state (wisp status=%q, want open)", wisp.ID, status)
		}
	})
}
