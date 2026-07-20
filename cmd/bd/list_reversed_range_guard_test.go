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
	cmd.Flags().String("created-after", "", "")
	cmd.Flags().String("created-before", "", "")
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
