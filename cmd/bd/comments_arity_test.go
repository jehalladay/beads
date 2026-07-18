package main

import "testing"

// TestCommentsShowArity proves the `comments` SHOW parent takes EXACTLY one
// positional ID (beads-uwis): its RunE reads only args[0], so before the fix it
// declared cobra.MinimumNArgs(1) and `bd comments a b c` silently showed only
// a's comments with rc=0, dropping b/c (a glob/typo read as success). It must
// reject extra IDs like its sibling single-target read commands children and
// history. `comments add` (id + optional text) intentionally stays MinimumNArgs.
func TestCommentsShowArity(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"comments"})
	if err != nil {
		t.Fatalf("rootCmd.Find([comments]): %v", err)
	}
	if cmd.Name() != "comments" {
		t.Fatalf("resolved %q, want %q", cmd.Name(), "comments")
	}
	if cmd.Args == nil {
		t.Fatal("comments has no Args validator; extra positional IDs would be silently ignored")
	}
	// Exactly one positional is accepted.
	if err := cmd.Args(cmd, []string{"bd-1"}); err != nil {
		t.Errorf("comments rejected a single positional %q: %v", "bd-1", err)
	}
	// Two positionals must be rejected (the RED before the fix).
	if err := cmd.Args(cmd, []string{"bd-1", "bd-2"}); err == nil {
		t.Error("comments accepted extra positional IDs; want ExactArgs(1) rejection so a glob/typo errors loudly instead of silently dropping IDs")
	}
	// Zero positionals must be rejected too.
	if err := cmd.Args(cmd, nil); err == nil {
		t.Error("comments accepted the no-arg case; an issue ID is required")
	}
}

// TestCommentsAddArityUnchanged guards that the `comments add` subcommand keeps
// its permissive MinimumNArgs(1) (id + optional inline text) — the fix must not
// tighten it.
func TestCommentsAddArityUnchanged(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"comments", "add"})
	if err != nil {
		t.Fatalf("rootCmd.Find([comments add]): %v", err)
	}
	if cmd.Name() != "add" {
		t.Fatalf("resolved %q, want %q", cmd.Name(), "add")
	}
	if cmd.Args == nil {
		t.Fatal("comments add has no Args validator")
	}
	// id + inline text (2 positionals) must still be accepted.
	if err := cmd.Args(cmd, []string{"bd-1", "some text"}); err != nil {
		t.Errorf("comments add rejected id+text: %v", err)
	}
}
