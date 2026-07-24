package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestStaleStatusHelpFullSet_o3wh0 guards beads-o3wh0 (sibling of
// beads-mfo0q/beads-3no17): the `bd stale --status` flag help must enumerate
// every built-in status it actually accepts. stale validates via
// types.Status.IsValidWithCustom (cmd/bd/stale.go) against the full built-in
// set (open, in_progress, blocked, deferred, closed, pinned, hooked) plus any
// status.custom values plus the 'all' sentinel (which clears the filter). The
// old help listed only 4 of the 7 built-ins, so a user never discovered that
// --status closed / pinned / hooked / all are valid (all accepted rc=0 at
// runtime). This test fails loudly if the help drifts back to under-listing.
func TestStaleStatusHelpFullSet_o3wh0(t *testing.T) {
	f := staleCmd.Flags().Lookup("status")
	if f == nil {
		t.Fatal("stale has no --status flag")
	}
	usage := f.Usage

	// The built-in statuses stale accepts (types.go). Anything IsValid() is a
	// legal --status value, so the help must mention each one.
	builtins := []types.Status{
		types.StatusOpen,
		types.StatusInProgress,
		types.StatusBlocked,
		types.StatusDeferred,
		types.StatusClosed,
		types.StatusPinned,
		types.StatusHooked,
	}
	for _, s := range builtins {
		if !s.IsValid() {
			t.Fatalf("test list drift: %q is no longer a built-in status", s)
		}
		if !strings.Contains(usage, string(s)) {
			t.Errorf("stale --status help omits the accepted built-in status %q (beads-o3wh0):\n%q", s, usage)
		}
	}

	// stale maps the 'all' sentinel to no filter, so the help should mention it.
	if !strings.Contains(usage, "all") {
		t.Errorf("stale --status help omits the 'all' sentinel (clears the filter, beads-o3wh0):\n%q", usage)
	}
}
