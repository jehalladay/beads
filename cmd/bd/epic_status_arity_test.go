package main

import "testing"

// TestEpicStatusArity proves `bd epic status` rejects stray positional args
// (beads-qvys): it lists ALL epics' closure-eligibility with no per-epic mode,
// but before the fix it had no Args constraint and silently ignored any arg —
// `epic status <id>` / a typo / garbage all exited 0 with the same whole-
// workspace listing, a false "filtered by id" read. Matches the sibling
// `epic close-eligible` (cobra.NoArgs).
func TestEpicStatusArity(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"epic", "status"})
	if err != nil {
		t.Fatalf("rootCmd.Find([epic status]): %v", err)
	}
	if cmd.Name() != "status" {
		t.Fatalf("resolved %q, want %q", cmd.Name(), "status")
	}
	if cmd.Args == nil {
		t.Fatal("epic status has no Args validator; a stray positional would be silently ignored")
	}
	// No positional is accepted.
	if err := cmd.Args(cmd, nil); err != nil {
		t.Errorf("epic status rejected the no-arg case: %v", err)
	}
	// A stray positional must be rejected (the RED before the fix).
	if err := cmd.Args(cmd, []string{"some-epic-id"}); err == nil {
		t.Error("epic status accepted a stray positional; want cobra.NoArgs rejection so a stray/typo arg errors loudly instead of silently listing all epics")
	}
}
