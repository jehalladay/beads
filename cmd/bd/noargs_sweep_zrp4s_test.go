package main

import (
	"strings"
	"testing"
)

// beads-zrp4s: residual zero-arg leaf subcommands the 8jy7e/s14ti/x91i6 sweeps
// missed. Each reads no positionals (its RunE never touches args) but had no
// Args validator, so a stray positional (bd metrics on extra, bd wisp gc junk,
// bd rules compact foo, bd formula list bar) was silently ignored with rc=0.
//
// The intra-file inconsistency is the tell: metricsExampleCmd (same file) was
// already guarded by 8jy7e — see noargs_sweep_test.go's
// TestNoArgsSweep_RemainingSubcommandsRejectPositional which pins
// {"metrics","example"} — but the sibling on/off leaves right beside it were
// never enumerated. formulaListCmd's sibling formulaShowCmd uses ExactArgs(1),
// the same inconsistency.
func TestNoArgsSweep_ZRP4S_ResidualLeavesRejectPositional(t *testing.T) {
	commands := [][]string{
		{"metrics", "on"},
		{"metrics", "off"},
		{"mol", "wisp", "gc"},
		{"rules", "compact"},
		{"formula", "list"},
	}

	for _, path := range commands {
		name := path[len(path)-1]
		t.Run(strings.Join(path, "_"), func(t *testing.T) {
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
			// No positionals must be accepted.
			if err := cmd.Args(cmd, nil); err != nil {
				t.Errorf("%v Args validator rejected the no-arg case: %v", path, err)
			}
		})
	}
}
