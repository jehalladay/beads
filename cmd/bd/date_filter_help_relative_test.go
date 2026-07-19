package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestDateFilterHelpDocumentsRelative is the teeth for beads-w66p3: the
// created/updated/closed × after/before date-filter flags on list/count/search
// ALL accept relative time expressions (+6h, tomorrow, yesterday) — they route
// through timeparsing.ParseRelativeTime by construction — but their help said
// only "(YYYY-MM-DD or RFC3339)", stale vs the defer-*/due-* siblings which
// already document relative support. This asserts every one of the 18 flags now
// mentions "relative" in its usage, so the doc-staleness can't silently return.
func TestDateFilterHelpDocumentsRelative(t *testing.T) {
	cmds := map[string]*cobra.Command{
		"list":   listCmd,
		"count":  countCmd,
		"search": searchCmd,
	}
	dateFlags := []string{
		"created-after", "created-before",
		"updated-after", "updated-before",
		"closed-after", "closed-before",
	}
	for cmdName, cmd := range cmds {
		for _, fn := range dateFlags {
			flag := cmd.Flags().Lookup(fn)
			if flag == nil {
				t.Errorf("%s: --%s flag not registered", cmdName, fn)
				continue
			}
			if !strings.Contains(strings.ToLower(flag.Usage), "relative") {
				t.Errorf("%s --%s help must document relative-time support (beads-w66p3), got %q", cmdName, fn, flag.Usage)
			}
			// The absolute formats must still be documented (we augmented, not
			// replaced, the format guidance).
			if !strings.Contains(flag.Usage, "YYYY-MM-DD") {
				t.Errorf("%s --%s help dropped the YYYY-MM-DD format hint, got %q", cmdName, fn, flag.Usage)
			}
		}
	}
}
