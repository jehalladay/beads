//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"
)

// TestEmbeddedStaleLimitGuard covers beads-r9hj: `bd stale --limit <negative>`
// must be a hard error, not a silent unbounded full-set return.
//
// eqi4 (9faffed6e) added the shared validateLimitFromCmd chokepoint and routed
// every sibling read command's --limit through it, but MISSED bd stale.
// GetStaleIssuesInTx emits a LIMIT clause only when filter.Limit > 0 (stale.go
// storage path), so a negative --limit is false → no LIMIT → the FULL result
// set with rc=0: the same misleading false-green eqi4 fixed for the others.
// r9hj routes stale through validateLimitFromCmd so a negative --limit fails
// loud while --limit 0 (the documented "unlimited" sentinel) and positives are
// left alone.
func TestEmbeddedStaleLimitGuard(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "sl")

	// Seed several stale issues so an unbounded (buggy) result would return
	// more than a small positive --limit — makes the guard's teeth meaningful.
	var ids []string
	for _, title := range []string{"stale a", "stale b", "stale c", "stale d"} {
		ids = append(ids, bdCreate(t, bd, dir, title, "--type", "task").ID)
	}
	makeIssuesStale(t, beadsDir, "sl", ids)

	t.Run("negative_limit_rejected", func(t *testing.T) {
		// The bug: --limit -1 silently unbounds. Must now fail loud (rc!=0).
		out := bdStaleFail(t, bd, dir, "--limit", "-1")
		if !strings.Contains(out, "--limit must be >= 0") {
			t.Errorf("stale --limit -1: want a '--limit must be >= 0' error, got:\n%s", out)
		}
	})

	t.Run("zero_limit_unlimited", func(t *testing.T) {
		// --limit 0 is the documented "unlimited" sentinel: still succeeds and
		// returns all stale issues (regression guard — must not be rejected).
		entries := bdStaleJSON(t, bd, dir, "--limit", "0")
		if len(entries) < len(ids) {
			t.Errorf("stale --limit 0: want all %d stale issues (unlimited), got %d", len(ids), len(entries))
		}
	})

	t.Run("positive_limit_bounds", func(t *testing.T) {
		// A positive --limit still bounds the result (regression guard).
		entries := bdStaleJSON(t, bd, dir, "--limit", "2")
		if len(entries) != 2 {
			t.Errorf("stale --limit 2: want exactly 2, got %d", len(entries))
		}
	})
}
