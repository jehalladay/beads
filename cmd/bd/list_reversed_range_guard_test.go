package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// beads-wnm6g (DISCOVERY.md BUG-36/BUG-37): a reversed range on bd list is
// silently wrong. --priority-min 4 --priority-max 0 builds
// "priority >= 4 AND priority <= 0" (always false) and --created-after 2099
// --created-before 2020 builds "created_at >= 2099 AND created_at <= 2020"
// (always false), so both return an empty result with NO error. The fix
// rejects the reversed range in gatherListInput. These teeth call
// gatherListInput directly (hermetic, no server) — the guard is pure input
// validation returning an error via HandleErrorRespectJSON (no os.Exit).

// newListRangeGuardCmd registers just the range flags gatherListInput
// validates. Unread flags return zero-values via the `, _` accessors, so this
// minimal set is safe.
func newListRangeGuardCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "list"}
	cmd.Flags().String("priority-min", "", "")
	cmd.Flags().String("priority-max", "", "")
	// beads-yqh4j: register every date axis bd list exposes so the guard tests
	// can exercise all of them (wnm6g only registered created).
	for _, axis := range []string{"created", "updated", "closed", "defer", "due"} {
		cmd.Flags().String(axis+"-after", "", "")
		cmd.Flags().String(axis+"-before", "", "")
	}
	return cmd
}

func TestGatherListInput_PriorityRangeReversedErrors(t *testing.T) {
	cmd := newListRangeGuardCmd()
	if err := cmd.Flags().Set("priority-min", "4"); err != nil {
		t.Fatalf("set --priority-min: %v", err)
	}
	if err := cmd.Flags().Set("priority-max", "0"); err != nil {
		t.Fatalf("set --priority-max: %v", err)
	}
	if _, err := gatherListInput(cmd); err == nil {
		t.Fatal("expected an error for --priority-min 4 --priority-max 0 (reversed range silently returns empty)")
	}
}

func TestGatherListInput_PriorityRangeNormalOK(t *testing.T) {
	// min <= max (including equal) must remain valid.
	for _, pair := range [][2]string{{"0", "4"}, {"2", "2"}} {
		cmd := newListRangeGuardCmd()
		if err := cmd.Flags().Set("priority-min", pair[0]); err != nil {
			t.Fatalf("set --priority-min: %v", err)
		}
		if err := cmd.Flags().Set("priority-max", pair[1]); err != nil {
			t.Fatalf("set --priority-max: %v", err)
		}
		if _, err := gatherListInput(cmd); err != nil {
			t.Errorf("--priority-min %s --priority-max %s should be valid, got: %v", pair[0], pair[1], err)
		}
	}
}

func TestGatherListInput_DateRangeReversedErrors(t *testing.T) {
	cmd := newListRangeGuardCmd()
	if err := cmd.Flags().Set("created-after", "2099-12-31"); err != nil {
		t.Fatalf("set --created-after: %v", err)
	}
	if err := cmd.Flags().Set("created-before", "2020-01-01"); err != nil {
		t.Fatalf("set --created-before: %v", err)
	}
	if _, err := gatherListInput(cmd); err == nil {
		t.Fatal("expected an error for --created-after 2099 --created-before 2020 (reversed date range silently returns empty)")
	}
}

func TestGatherListInput_DateRangeNormalOK(t *testing.T) {
	cmd := newListRangeGuardCmd()
	if err := cmd.Flags().Set("created-after", "2020-01-01"); err != nil {
		t.Fatalf("set --created-after: %v", err)
	}
	if err := cmd.Flags().Set("created-before", "2099-12-31"); err != nil {
		t.Fatalf("set --created-before: %v", err)
	}
	if _, err := gatherListInput(cmd); err != nil {
		t.Errorf("normal date range should be valid, got: %v", err)
	}
}

// TestGatherListInput_AllDateAxesReversedErrors is the beads-yqh4j completeness
// teeth: wnm6g guarded only --created, but bd list exposes updated/closed/
// defer/due date ranges too, each of which builds an always-false WHERE when
// reversed. Every axis must reject a reversed range; equal/ordered bounds stay
// valid. RED-verified by running before the guard loop (each new axis returned
// nil error with an empty result).
func TestGatherListInput_AllDateAxesReversedErrors(t *testing.T) {
	for _, axis := range []string{"created", "updated", "closed", "defer", "due"} {
		t.Run(axis+"_reversed_rejected", func(t *testing.T) {
			cmd := newListRangeGuardCmd()
			if err := cmd.Flags().Set(axis+"-after", "2099-12-31"); err != nil {
				t.Fatalf("set --%s-after: %v", axis, err)
			}
			if err := cmd.Flags().Set(axis+"-before", "2020-01-01"); err != nil {
				t.Fatalf("set --%s-before: %v", axis, err)
			}
			if _, err := gatherListInput(cmd); err == nil {
				t.Fatalf("expected an error for reversed --%s-after/--%s-before (silently returns empty)", axis, axis)
			}
		})
		t.Run(axis+"_ordered_ok", func(t *testing.T) {
			cmd := newListRangeGuardCmd()
			if err := cmd.Flags().Set(axis+"-after", "2020-01-01"); err != nil {
				t.Fatalf("set --%s-after: %v", axis, err)
			}
			if err := cmd.Flags().Set(axis+"-before", "2099-12-31"); err != nil {
				t.Fatalf("set --%s-before: %v", axis, err)
			}
			if _, err := gatherListInput(cmd); err != nil {
				t.Errorf("ordered --%s range should be valid, got: %v", axis, err)
			}
		})
	}
}
