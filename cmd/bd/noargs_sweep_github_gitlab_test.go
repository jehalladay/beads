package main

import (
	"testing"
)

// beads-euyjs: the 8jy7e SUBCOMMAND-axis NoArgs sweep enumerated the ado and
// federation sync-provider families but OMITTED the github/gitlab providers
// entirely. These flag-only/no-arg leaves (status/repos/projects/sync) read no
// positionals in their RunE but had no Args validator, so a stray positional
// (bd github status foo, bd gitlab sync bogus) was silently ignored with rc=0.
//
// EXCLUDED (arg-consuming, correctly declare positionals in Use): the pull/push
// leaves in sync_push_pull.go ("push [bead-ids...]", "pull [refs...]") — same
// exclusion rule as 8jy7e's sync push/pull.
//
// Lives in its own file (not noargs_sweep_test.go) to stay off the shared table
// region that the sibling 8jy7e-slice MRs append to.
func TestNoArgsSweep_GitHubGitLabProvidersRejectPositional(t *testing.T) {
	commands := [][]string{
		{"github", "status"},
		{"github", "repos"},
		{"github", "sync"},
		{"gitlab", "status"},
		{"gitlab", "projects"},
		{"gitlab", "sync"},
	}

	for _, path := range commands {
		path := path
		name := path[len(path)-1]
		t.Run(path[0]+"_"+name, func(t *testing.T) {
			cmd, _, err := rootCmd.Find(path)
			if err != nil {
				t.Fatalf("rootCmd.Find(%v): %v", path, err)
			}
			// Confirm we resolved the leaf, not a parent (Find returns the
			// deepest match; a mistyped path could resolve to an ancestor).
			if cmd.Name() != name {
				t.Fatalf("resolved %q, want %q — path %v did not reach the leaf", cmd.Name(), name, path)
			}
			if cmd.Args == nil {
				t.Fatalf("%v has no Args validator; a stray positional would be silently ignored", path)
			}
			// A positional must be rejected.
			if err := cmd.Args(cmd, []string{"stray"}); err == nil {
				t.Errorf("%v Args validator accepted a stray positional %q, want rejection", path, "stray")
			}
			// The no-arg case must be accepted.
			if err := cmd.Args(cmd, nil); err != nil {
				t.Errorf("%v Args validator rejected the no-arg case: %v", path, err)
			}
		})
	}
}
