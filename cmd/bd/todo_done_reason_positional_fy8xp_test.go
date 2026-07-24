package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// beads-fy8xp: hermetic teeth for `bd todo done`'s positional --reason helpers.
//
// Before the fix `bd todo done` read a single GetString("reason") — cobra
// last-wins — so `bd todo done A B --reason r1 --reason r2` silently dropped r1
// and applied r2 to BOTH IDs (batch data loss, zero signal). This is the qvbjq
// sibling (qvbjq brought close+defer to a positional --reason model; reopen +
// todo done were the two unfixed siblings). These tests pin the pure
// slice/index logic; end-to-end mapping is covered by the embedded test.
//
// MUTATION-VERIFY: change reasonForTodoDoneIndex's `default` arm to `return
// reasons[0]` (revert to broadcast) and reason_maps_positionally goes RED;
// remove the `len(reasons)==0 → ""` arm and no_reason_is_empty panics/RED.

func TestReasonForTodoDoneIndex_fy8xp(t *testing.T) {
	t.Run("no_reason_is_empty", func(t *testing.T) {
		// todo done's --reason is OPTIONAL: zero reasons → "" at every index (the
		// caller wraps it in todoDoneReasonOrDefault for the "Completed" fallback,
		// so no empty-reason error is raised, per beads-07sko).
		for _, i := range []int{0, 1, 7} {
			if got := reasonForTodoDoneIndex(nil, i); got != "" {
				t.Errorf("reasonForTodoDoneIndex(nil, %d) = %q, want empty", i, got)
			}
		}
	})

	t.Run("single_reason_broadcasts", func(t *testing.T) {
		single := []string{"shared"}
		for _, i := range []int{0, 1, 5} {
			if got := reasonForTodoDoneIndex(single, i); got != "shared" {
				t.Errorf("single reason at index %d = %q, want shared", i, got)
			}
		}
	})

	t.Run("reason_maps_positionally", func(t *testing.T) {
		multi := []string{"r0", "r1", "r2"}
		for i, want := range multi {
			if got := reasonForTodoDoneIndex(multi, i); got != want {
				t.Errorf("REGRESSION (beads-fy8xp): multi reason at index %d = %q, want %q (todo done must map --reason positionally like close, not broadcast last-wins)", i, got, want)
			}
		}
	})
}

// buildTodoDoneCmd builds a minimal command carrying the same repeatable
// --reason flag `bd todo done` registers, so collectTodoDoneReasons can be
// driven in isolation.
func buildTodoDoneCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "done"}
	cmd.Flags().VarP(&closeReasonFlagValue{}, "reason", "r", "")
	return cmd
}

func TestCollectTodoDoneReasons_fy8xp(t *testing.T) {
	t.Run("none_given", func(t *testing.T) {
		cmd := buildTodoDoneCmd()
		if got := collectTodoDoneReasons(cmd); len(got) != 0 {
			t.Errorf("no --reason → empty, got %v", got)
		}
	})

	t.Run("repeated_preserved_in_order", func(t *testing.T) {
		cmd := buildTodoDoneCmd()
		if err := cmd.ParseFlags([]string{"-r", "first", "-r", "second"}); err != nil {
			t.Fatalf("parse: %v", err)
		}
		got := collectTodoDoneReasons(cmd)
		if len(got) != 2 || got[0] != "first" || got[1] != "second" {
			t.Errorf("REGRESSION (beads-fy8xp): repeated --reason collected as %v, want [first second] (last-wins would drop 'first')", got)
		}
	})

	t.Run("empty_reason_kept_for_count_guard", func(t *testing.T) {
		// Keeps an explicit empty so the count-guard sees the true positional
		// count; the per-index todoDoneReasonOrDefault collapses a whitespace-only
		// slot to the "Completed" default (07sko), so no empty-reason error.
		cmd := buildTodoDoneCmd()
		if err := cmd.ParseFlags([]string{"--reason="}); err != nil {
			t.Fatalf("parse: %v", err)
		}
		got := collectTodoDoneReasons(cmd)
		if len(got) != 1 || got[0] != "" {
			t.Errorf("explicit --reason='' must be kept (len1, empty) for the count-guard, got %v", got)
		}
	})
}
