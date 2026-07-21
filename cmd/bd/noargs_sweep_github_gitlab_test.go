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

// beads-kwghg: enumeration residual after euyjs — the NoArgs SUBCOMMAND-axis
// sweep fixed github/gitlab (euyjs) and 21 other leaves (kg678) but OMITTED the
// other three sync-provider families entirely: notion, linear, jira. These nine
// flag-only/no-arg leaves read no positionals in their RunE but had no Args
// validator, so a stray positional (bd notion status foo, bd jira sync bogus)
// was silently ignored with rc=0.
//
// EXCLUDED (would carry their own validator): none today — none of the nine
// consume positionals (no [positional] in Use, RunE args==0). If notion/linear/
// jira later gain a pull/push arg-verb (as github/gitlab did in sync_push_pull.go),
// that verb keeps its own validator — same exclusion rule as euyjs.
//
// Lives in this file (not noargs_sweep_test.go) to stay off the shared table
// region that the sibling 8jy7e-slice MRs append to.
func TestNoArgsSweep_NotionLinearJiraProvidersRejectPositional(t *testing.T) {
	commands := [][]string{
		{"notion", "status"},
		{"notion", "init"},
		{"notion", "connect"},
		{"notion", "sync"},
		{"linear", "sync"},
		{"linear", "status"},
		{"linear", "teams"},
		{"jira", "sync"},
		{"jira", "status"},
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
