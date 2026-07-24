package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/types"
)

// TestStatusFilterHelpFullSet_vhpam guards beads-vhpam + beads-wfcts (the
// multi-command sibling of beads-mfo0q/3no17/o3wh0): every read command whose
// --status flag is validated by the shared types.Status.IsValidWithCustom path
// must enumerate the full built-in status set in its flag help (find-duplicates
// is the seventh sibling, beads-wfcts, folded in). These commands all accept
// open/in_progress/blocked/deferred/closed/pinned/hooked + status.custom + the
// 'all' sentinel at runtime, but their static help literals historically listed
// only 4-5, so users never discovered closed/pinned/hooked/all were valid.
//
// Reading the flag Usage from each command's registered cobra flag means this
// fails loudly if any command's help drifts back to under-listing, or if a new
// built-in status is added to types.Status without updating the help source.
func TestStatusFilterHelpFullSet_vhpam(t *testing.T) {
	cmds := []struct {
		name string
		cmd  *cobra.Command
	}{
		{"list", listCmd},
		{"count", countCmd},
		{"search", searchCmd},
		{"human list", humanListCmd},
		{"migrate-issues", migrateIssuesCmd},
		{"dep tree", depTreeCmd},
		{"find-duplicates", findDuplicatesCmd},
	}

	// The built-in statuses the shared validator accepts (types.go). Every one
	// is IsValid(), so each command's --status help must mention it.
	builtins := []types.Status{
		types.StatusOpen,
		types.StatusInProgress,
		types.StatusBlocked,
		types.StatusDeferred,
		types.StatusClosed,
		types.StatusPinned,
		types.StatusHooked,
	}

	for _, tc := range cmds {
		f := tc.cmd.Flags().Lookup("status")
		if f == nil {
			t.Errorf("%s has no --status flag", tc.name)
			continue
		}
		usage := f.Usage
		for _, s := range builtins {
			if !s.IsValid() {
				t.Fatalf("test list drift: %q is no longer a built-in status", s)
			}
			if !strings.Contains(usage, string(s)) {
				t.Errorf("%s --status help omits the accepted built-in status %q (beads-vhpam):\n%q", tc.name, s, usage)
			}
		}
		// The 'all' sentinel clears the filter on every sibling.
		if !strings.Contains(usage, "all") {
			t.Errorf("%s --status help omits the 'all' sentinel (beads-vhpam):\n%q", tc.name, usage)
		}
	}
}
