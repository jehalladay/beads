//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestEmbeddedShipDoneCategory6yt1m is the beads-6yt1m teeth. `bd ship` gated on
// a literal `issue.Status != types.StatusClosed` (ship.go + ship_proxied_server.go),
// so a custom done-category status (e.g. "verified:done") — a terminal "work
// complete" outcome that beads-97gmg/x463g/ulsg4 already treat as complete
// elsewhere, and that bd ready/count/list already exclude — was refused with
// "is not closed (status: verified)" and only shippable with --force.
//
// The fix widens the gate at both sites to accept literal-closed OR a
// done-category status (reusing doneCategoryStatusNames / the proxied
// doneCategoryStatusSetProxied resolver). Degraded-safe: empty done-set →
// literal-'closed'. Frozen-category excluded (parked != done).
//
// Mutation: revert the ship.go gate back to `issue.Status != types.StatusClosed
// && !force` → done_category_status_ships_without_force goes RED (ship refuses).
func TestEmbeddedShipDoneCategory6yt1m(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "shd")

	// Configure a custom done-category status and a frozen (parked) one.
	bdConfig(t, bd, dir, "set", "status.custom", "verified:done,parked:frozen")

	// labelAdd attaches an export: label to an issue.
	labelAdd := func(t *testing.T, id, label string) {
		t.Helper()
		cmd := exec.Command(bd, "label", "add", id, label)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("label add %s %s failed: %v\n%s", id, label, err, out)
		}
	}

	// (1) THE FIX: a done-category (complete) issue ships WITHOUT --force.
	t.Run("done_category_status_ships_without_force", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "verified feature", "--type", "feature")
		labelAdd(t, issue.ID, "export:vcap")
		bdUpdate(t, bd, dir, issue.ID, "--status", "verified") // done-category, not literally closed
		out := bdShip(t, bd, dir, "vcap")
		if !strings.Contains(out, "Shipped") || !strings.Contains(out, "provides:vcap") {
			t.Errorf("expected done-category issue to ship without --force, got:\n%s", out)
		}
	})

	// (2) NEGATIVE (regression): a literally-CLOSED issue still ships (byte-
	//     identical to the pre-fix accepted case).
	t.Run("closed_status_still_ships", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "closed feature", "--type", "feature")
		labelAdd(t, issue.ID, "export:ccap")
		cmd := exec.Command(bd, "close", issue.ID)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("close failed: %v\n%s", err, out)
		}
		out := bdShip(t, bd, dir, "ccap")
		if !strings.Contains(out, "Shipped") {
			t.Errorf("expected closed issue to ship, got:\n%s", out)
		}
	})

	// (3) NEGATIVE (regression): a literally-OPEN issue is still REFUSED without
	//     --force — the widen must not open the gate for genuinely-incomplete work.
	t.Run("open_status_still_refuses", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "open feature", "--type", "feature")
		labelAdd(t, issue.ID, "export:ocap")
		out := bdShipFail(t, bd, dir, "ocap")
		if !strings.Contains(out, "is not closed") {
			t.Errorf("expected open issue to be refused, got:\n%s", out)
		}
	})

	// (4) NEGATIVE (scope): a FROZEN-category status is parked, not done — it must
	//     still be REFUSED (matches 97gmg/x463g/ulsg4 Done-only semantics).
	t.Run("frozen_category_status_still_refuses", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "parked feature", "--type", "feature")
		labelAdd(t, issue.ID, "export:pcap")
		bdUpdate(t, bd, dir, issue.ID, "--status", "parked") // frozen-category, NOT done
		out := bdShipFail(t, bd, dir, "pcap")
		if !strings.Contains(out, "is not closed") {
			t.Errorf("expected frozen (parked) issue to be refused, got:\n%s", out)
		}
	})

	// (5) --force still overrides for a genuinely-open issue (escape hatch intact).
	t.Run("open_status_force_ships", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "force feature", "--type", "feature")
		labelAdd(t, issue.ID, "export:fcap")
		out := bdShip(t, bd, dir, "fcap", "--force")
		if !strings.Contains(out, "Shipped") {
			t.Errorf("expected --force to ship open issue, got:\n%s", out)
		}
	})
}
