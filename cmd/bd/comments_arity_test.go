package main

import "testing"

// TestCommentsArity_ExactlyOne pins beads-uwis: the `comments <issue-id>` show
// command reads only args[0], so before this fix its MinimumNArgs(1) validator
// silently ignored extra positionals (a typo/glob) and ran with rc=0. It now
// uses ExactArgs(1) — matching the sibling single-target read commands
// (children, history) — so 0 or 2+ ids are rejected loudly while exactly one
// is accepted. The `comments add` subcommand keeps MinimumNArgs(1) (id + text)
// and is exercised separately.
func TestCommentsArity_ExactlyOne(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"comments"})
	if err != nil {
		t.Fatalf("rootCmd.Find([comments]): %v", err)
	}
	if cmd.Name() != "comments" {
		t.Fatalf("resolved %q, want %q", cmd.Name(), "comments")
	}
	if cmd.Args == nil {
		t.Fatal("comments has no Args validator")
	}

	// Exactly one id is accepted.
	if err := cmd.Args(cmd, []string{"bd-1"}); err != nil {
		t.Errorf("comments rejected the single-id case: %v", err)
	}
	// Zero ids must be rejected (an id is required — there is no "comments list").
	if err := cmd.Args(cmd, nil); err == nil {
		t.Error("comments accepted zero ids, want rejection")
	}
	// Two+ ids must be rejected rather than silently ignoring the extras.
	if err := cmd.Args(cmd, []string{"bd-1", "bd-2"}); err == nil {
		t.Error("comments accepted extra ids, want rejection (extras were silently dropped before beads-uwis)")
	}

	// The `comments add` subcommand still accepts >=1 (id [+ optional text]).
	addCmd, _, err := rootCmd.Find([]string{"comments", "add"})
	if err != nil {
		t.Fatalf("rootCmd.Find([comments add]): %v", err)
	}
	if addCmd.Name() != "add" {
		t.Fatalf("resolved %q, want %q", addCmd.Name(), "add")
	}
	if err := addCmd.Args(addCmd, []string{"bd-1", "some", "text"}); err != nil {
		t.Errorf("comments add rejected id+text: %v", err)
	}
}
