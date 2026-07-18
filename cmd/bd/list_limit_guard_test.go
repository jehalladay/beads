package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// beads-uh4i: bd list --limit accepts a negative value silently and coerces it
// to "no limit" (returns ALL rows), where the sibling --offset<0 hard-errors.
// The SQL builders guard `if filter.Limit > 0`, so a negative Limit emits no
// LIMIT clause and the whole table comes back — a validation-parity gap on
// numeric flags in the list path (same family as pbl7, distinct flag+command).
//
// The fix mirrors the existing --offset guard in gatherListInput: a changed
// --limit < 0 must error, not silently unbound the result set. These teeth call
// gatherListInput directly (hermetic, no server) — the guard is pure input
// validation and returns an error via HandleErrorRespectJSON (no os.Exit).

// newListGuardCmd registers just the numeric flags gatherListInput validates,
// so the input parser can be exercised without the full list command. Unread
// flags return zero-values via the `, _` accessors, so this minimal set is safe.
func newListGuardCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "list"}
	cmd.Flags().IntP("limit", "n", 50, "")
	cmd.Flags().Int("offset", 0, "")
	return cmd
}

func TestGatherListInput_NegativeLimitErrors(t *testing.T) {
	cmd := newListGuardCmd()
	if err := cmd.Flags().Set("limit", "-5"); err != nil {
		t.Fatalf("set --limit: %v", err)
	}
	_, err := gatherListInput(cmd)
	if err == nil {
		t.Fatal("expected an error for --limit -5, got nil (negative limit was silently accepted → returns ALL rows)")
	}
}

func TestGatherListInput_NegativeLimitParityWithOffset(t *testing.T) {
	// --offset<0 already errors; --limit<0 must error the same way.
	cmd := newListGuardCmd()
	if err := cmd.Flags().Set("offset", "-1"); err != nil {
		t.Fatalf("set --offset: %v", err)
	}
	if _, err := gatherListInput(cmd); err == nil {
		t.Fatal("sanity: --offset -1 should error (reference guard)")
	}

	cmd2 := newListGuardCmd()
	if err := cmd2.Flags().Set("limit", "-1"); err != nil {
		t.Fatalf("set --limit: %v", err)
	}
	if _, err := gatherListInput(cmd2); err == nil {
		t.Error("--limit -1 should error for parity with --offset -1")
	}
}

func TestGatherListInput_ZeroAndPositiveLimitOK(t *testing.T) {
	// --limit 0 is the documented "unlimited" sentinel and must remain valid;
	// a positive limit is obviously valid. Only negative is invalid.
	for _, v := range []string{"0", "10"} {
		cmd := newListGuardCmd()
		if err := cmd.Flags().Set("limit", v); err != nil {
			t.Fatalf("set --limit %s: %v", v, err)
		}
		if _, err := gatherListInput(cmd); err != nil {
			t.Errorf("--limit %s should be valid, got error: %v", v, err)
		}
	}
}
