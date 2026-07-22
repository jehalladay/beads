//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestSuggestedDuplicateCommands_ulq23 pins the copy-paste merge hint to the
// canonical atomic form `bd duplicate <src> --of <target>` (beads-ulq23).
//
// The prior hint printed `bd close <src> && bd dep add <src> <target> --type
// related`, which (1) teaches the non-atomic 2-step form that bypasses the
// LinkAndClose guards + molecule auto-close + on_close hook, and (2) mints a
// `related` edge — divergent from what `--auto-merge` now executes post-chf1w
// (a proper duplicates edge) and invisible to the beads-8nugc reopen guard.
//
// Teeth: asserts the exact canonical command AND that the divergent legacy
// fragments are gone. Reverting suggestedDuplicateCommands to the old string
// fails both the positive and the negative assertions.
func TestSuggestedDuplicateCommands_ulq23(t *testing.T) {
	t.Run("single source", func(t *testing.T) {
		got := suggestedDuplicateCommands([]string{"bd-abc"}, "bd-xyz")
		want := "bd duplicate bd-abc --of bd-xyz"
		if got != want {
			t.Fatalf("single-source hint = %q, want %q", got, want)
		}
	})

	t.Run("multiple sources join with &&", func(t *testing.T) {
		got := suggestedDuplicateCommands([]string{"bd-a", "bd-b"}, "bd-t")
		want := "bd duplicate bd-a --of bd-t && bd duplicate bd-b --of bd-t"
		if got != want {
			t.Fatalf("multi-source hint = %q, want %q", got, want)
		}
	})

	t.Run("no legacy related/dep-add/close form", func(t *testing.T) {
		got := suggestedDuplicateCommands([]string{"bd-a", "bd-b"}, "bd-t")
		for _, bad := range []string{"--type related", "bd dep add", "bd close"} {
			if strings.Contains(got, bad) {
				t.Fatalf("hint %q still contains divergent legacy fragment %q", got, bad)
			}
		}
		if !strings.Contains(got, "bd duplicate ") || !strings.Contains(got, "--of ") {
			t.Fatalf("hint %q missing canonical `bd duplicate ... --of` form", got)
		}
	})
}
