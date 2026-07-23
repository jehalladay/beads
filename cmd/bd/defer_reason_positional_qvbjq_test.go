package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// beads-qvbjq: hermetic teeth for defer's positional --reason helpers.
//
// Before the fix `bd defer` read a single GetString("reason") — cobra
// last-wins — so `bd defer A B --reason r1 --reason r2` silently dropped r1 and
// applied r2 to BOTH IDs (batch data loss, zero signal). `bd close`/`bd done`
// already mapped repeated --reason POSITIONALLY via closeReasonFlagValue and is
// the documented, mature convention, so defer is brought to parity. These
// tests pin the pure slice/index logic; end-to-end mapping is covered by the
// embedded integration test.
//
// MUTATION-VERIFY: change reasonForDeferIndex's `default` arm to `return
// reasons[0]` (revert to broadcast) and reason_maps_positionally goes RED;
// remove the `len(reasons)==0 → ""` arm and no_reason_is_empty panics/RED.

func TestReasonForDeferIndex_qvbjq(t *testing.T) {
	t.Run("no_reason_is_empty", func(t *testing.T) {
		// The defer-specific case close never has: zero reasons → "" at every
		// index (defer's common path appends no notes).
		for _, i := range []int{0, 1, 7} {
			if got := reasonForDeferIndex(nil, i); got != "" {
				t.Errorf("reasonForDeferIndex(nil, %d) = %q, want empty", i, got)
			}
		}
	})

	t.Run("single_reason_broadcasts", func(t *testing.T) {
		single := []string{"shared"}
		for _, i := range []int{0, 1, 5} {
			if got := reasonForDeferIndex(single, i); got != "shared" {
				t.Errorf("single reason at index %d = %q, want shared", i, got)
			}
		}
	})

	t.Run("reason_maps_positionally", func(t *testing.T) {
		multi := []string{"r0", "r1", "r2"}
		for i, want := range multi {
			if got := reasonForDeferIndex(multi, i); got != want {
				t.Errorf("REGRESSION (beads-qvbjq): multi reason at index %d = %q, want %q (defer must map --reason positionally like close, not broadcast last-wins)", i, got, want)
			}
		}
	})
}

// buildDeferCmd builds a minimal command carrying the same repeatable --reason
// flag defer registers, so collectDeferReasons can be driven in isolation.
func buildDeferCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "defer"}
	cmd.Flags().VarP(&closeReasonFlagValue{}, "reason", "r", "")
	return cmd
}

func TestCollectDeferReasons_qvbjq(t *testing.T) {
	t.Run("none_given", func(t *testing.T) {
		cmd := buildDeferCmd()
		if got := collectDeferReasons(cmd); len(got) != 0 {
			t.Errorf("no --reason → empty, got %v", got)
		}
	})

	t.Run("repeated_preserved_in_order", func(t *testing.T) {
		cmd := buildDeferCmd()
		if err := cmd.ParseFlags([]string{"-r", "first", "-r", "second"}); err != nil {
			t.Fatalf("parse: %v", err)
		}
		got := collectDeferReasons(cmd)
		if len(got) != 2 || got[0] != "first" || got[1] != "second" {
			t.Errorf("REGRESSION (beads-qvbjq): repeated --reason collected as %v, want [first second] (last-wins would drop 'first')", got)
		}
	})

	t.Run("empty_reason_kept_for_validation", func(t *testing.T) {
		// Unlike collectCloseReasonFlags, defer keeps an explicit empty so the
		// RunE can reject it with the beads-v02z JSON error rather than silently
		// treating "--reason=''" as "no reason".
		cmd := buildDeferCmd()
		if err := cmd.ParseFlags([]string{"--reason="}); err != nil {
			t.Fatalf("parse: %v", err)
		}
		got := collectDeferReasons(cmd)
		if len(got) != 1 || got[0] != "" {
			t.Errorf("explicit --reason='' must be kept (len1, empty) for validation, got %v", got)
		}
	})
}
