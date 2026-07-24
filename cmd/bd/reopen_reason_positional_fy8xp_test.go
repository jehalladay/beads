package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// beads-fy8xp: hermetic teeth for reopen's positional --reason helpers.
//
// Before the fix `bd reopen` read a single GetString("reason") — cobra
// last-wins — so `bd reopen A B --reason r1 --reason r2` silently dropped r1
// and applied r2 to BOTH IDs (batch data loss, zero signal). This is the qvbjq
// sibling: qvbjq brought `bd close`/`bd defer` to a positional --reason model
// via closeReasonFlagValue; reopen + todo done were the two unfixed siblings.
// These tests pin the pure slice/index logic; end-to-end mapping is covered by
// the embedded integration test.
//
// MUTATION-VERIFY: change reasonForReopenIndex's `default` arm to `return
// reasons[0]` (revert to broadcast) and reason_maps_positionally goes RED;
// remove the `len(reasons)==0 → ""` arm and no_reason_is_empty panics/RED.

func TestReasonForReopenIndex_fy8xp(t *testing.T) {
	t.Run("no_reason_is_empty", func(t *testing.T) {
		// reopen's --reason is OPTIONAL: zero reasons → "" at every index (the
		// per-index normalizeReopenReason then keeps it no-reason, unlike
		// defer's v02z empty-reason error).
		for _, i := range []int{0, 1, 7} {
			if got := reasonForReopenIndex(nil, i); got != "" {
				t.Errorf("reasonForReopenIndex(nil, %d) = %q, want empty", i, got)
			}
		}
	})

	t.Run("single_reason_broadcasts", func(t *testing.T) {
		single := []string{"shared"}
		for _, i := range []int{0, 1, 5} {
			if got := reasonForReopenIndex(single, i); got != "shared" {
				t.Errorf("single reason at index %d = %q, want shared", i, got)
			}
		}
	})

	t.Run("reason_maps_positionally", func(t *testing.T) {
		multi := []string{"r0", "r1", "r2"}
		for i, want := range multi {
			if got := reasonForReopenIndex(multi, i); got != want {
				t.Errorf("REGRESSION (beads-fy8xp): multi reason at index %d = %q, want %q (reopen must map --reason positionally like close, not broadcast last-wins)", i, got, want)
			}
		}
	})
}

// buildReopenCmd builds a minimal command carrying the same repeatable --reason
// flag reopen registers, so collectReopenReasons can be driven in isolation.
func buildReopenCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "reopen"}
	cmd.Flags().VarP(&closeReasonFlagValue{}, "reason", "r", "")
	return cmd
}

func TestCollectReopenReasons_fy8xp(t *testing.T) {
	t.Run("none_given", func(t *testing.T) {
		cmd := buildReopenCmd()
		if got := collectReopenReasons(cmd); len(got) != 0 {
			t.Errorf("no --reason → empty, got %v", got)
		}
	})

	t.Run("repeated_preserved_in_order", func(t *testing.T) {
		cmd := buildReopenCmd()
		if err := cmd.ParseFlags([]string{"-r", "first", "-r", "second"}); err != nil {
			t.Fatalf("parse: %v", err)
		}
		got := collectReopenReasons(cmd)
		if len(got) != 2 || got[0] != "first" || got[1] != "second" {
			t.Errorf("REGRESSION (beads-fy8xp): repeated --reason collected as %v, want [first second] (last-wins would drop 'first')", got)
		}
	})

	t.Run("empty_reason_kept_for_count_guard", func(t *testing.T) {
		// Unlike collectCloseReasonFlags, reopen keeps an explicit empty so the
		// count-guard sees the true positional count (a `--reason ""` slot still
		// occupies a position); the per-index normalizeReopenReason collapses it
		// to no-reason (reopen's reason is optional, so no empty-reason error).
		cmd := buildReopenCmd()
		if err := cmd.ParseFlags([]string{"--reason="}); err != nil {
			t.Fatalf("parse: %v", err)
		}
		got := collectReopenReasons(cmd)
		if len(got) != 1 || got[0] != "" {
			t.Errorf("explicit --reason='' must be kept (len1, empty) for the count-guard, got %v", got)
		}
	})
}
