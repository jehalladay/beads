package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// beads-94pz: hermetic tests for the close-reason helper functions in close.go
// (verified 0% + no test references). Pure slice/index logic + a cobra-flag
// reader (validateCloseReasons is config-gated glue, left to integration).

func TestNonEmptyCloseReasons(t *testing.T) {
	got := nonEmptyCloseReasons([]string{"a", "", "b", "", "c"})
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Errorf("nonEmptyCloseReasons dropped/kept wrong entries: %v", got)
	}
	if len(nonEmptyCloseReasons(nil)) != 0 {
		t.Error("nil → empty")
	}
	if len(nonEmptyCloseReasons([]string{"", ""})) != 0 {
		t.Error("all-empty → empty")
	}
	// beads-in93a: whitespace-only entries are dropped like empties, but a
	// genuine reason is kept VERBATIM (untrimmed) to preserve formatting.
	got = nonEmptyCloseReasons([]string{"  ", "\t\n", "  real  ", ""})
	if len(got) != 1 {
		t.Fatalf("whitespace-only entries not dropped: %v", got)
	}
	if got[0] != "  real  " {
		t.Errorf("surviving reason must be verbatim (untrimmed), got %q", got[0])
	}
	if len(nonEmptyCloseReasons([]string{"   ", "\t"})) != 0 {
		t.Error("all-whitespace → empty (beads-in93a)")
	}
}

func TestReasonForCloseIndex(t *testing.T) {
	// A single reason applies to every index (broadcast).
	single := []string{"only"}
	for _, i := range []int{0, 1, 5} {
		if got := reasonForCloseIndex(single, i); got != "only" {
			t.Errorf("single reason at index %d = %q, want only", i, got)
		}
	}
	// Multiple reasons index positionally.
	multi := []string{"r0", "r1", "r2"}
	for i, want := range multi {
		if got := reasonForCloseIndex(multi, i); got != want {
			t.Errorf("multi reason at index %d = %q, want %q", i, got, want)
		}
	}
}

// buildCloseCmd builds a minimal command carrying the same reason flags close
// registers, so collectCloseReasonFlags can be driven without the full command.
func buildCloseCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "close"}
	registerCloseReasonFlag(cmd)
	cmd.Flags().String("resolution", "", "")
	cmd.Flags().StringP("message", "m", "", "")
	cmd.Flags().String("comment", "", "")
	return cmd
}

func TestCollectCloseReasonFlags(t *testing.T) {
	t.Run("no flags → nil", func(t *testing.T) {
		reasons, err := collectCloseReasonFlags(buildCloseCmd())
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if reasons != nil {
			t.Errorf("expected nil, got %v", reasons)
		}
	})

	t.Run("--reason (repeatable) wins and drops empties", func(t *testing.T) {
		cmd := buildCloseCmd()
		_ = cmd.Flags().Set("reason", "first")
		_ = cmd.Flags().Set("reason", "second")
		reasons, err := collectCloseReasonFlags(cmd)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(reasons) != 2 || reasons[0] != "first" || reasons[1] != "second" {
			t.Errorf("expected [first second], got %v", reasons)
		}
	})

	t.Run("falls back to --resolution alias", func(t *testing.T) {
		cmd := buildCloseCmd()
		_ = cmd.Flags().Set("resolution", "done via jira alias")
		reasons, err := collectCloseReasonFlags(cmd)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(reasons) != 1 || reasons[0] != "done via jira alias" {
			t.Errorf("expected resolution alias, got %v", reasons)
		}
	})

	t.Run("--message alias is honored", func(t *testing.T) {
		cmd := buildCloseCmd()
		_ = cmd.Flags().Set("message", "git-style message")
		reasons, err := collectCloseReasonFlags(cmd)
		if err != nil || len(reasons) != 1 || reasons[0] != "git-style message" {
			t.Fatalf("expected message alias, got %v (err %v)", reasons, err)
		}
	})

	// beads-in93a: a whitespace-only --reason must be dropped so the caller
	// falls through to the default "Closed", not stored as a blank reason.
	t.Run("whitespace-only --reason is dropped", func(t *testing.T) {
		cmd := buildCloseCmd()
		_ = cmd.Flags().Set("reason", "   ")
		reasons, err := collectCloseReasonFlags(cmd)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if reasons != nil {
			t.Errorf("whitespace-only --reason must yield nil (default Closed), got %v", reasons)
		}
	})

	// beads-in93a: same for the resolution/message/comment alias loop.
	t.Run("whitespace-only --comment alias is dropped", func(t *testing.T) {
		cmd := buildCloseCmd()
		_ = cmd.Flags().Set("comment", "\t\n ")
		reasons, err := collectCloseReasonFlags(cmd)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if reasons != nil {
			t.Errorf("whitespace-only --comment must yield nil (default Closed), got %v", reasons)
		}
	})
}
