//go:build cgo

package main

import (
	"testing"
)

// beads-wy9jc: enumeration residual in the 8jy7e NoArgs SUBCOMMAND-axis sweep.
// 8jy7e added cobra.NoArgs to federationListPeersCmd (list-peers) but missed the
// two sibling no-arg leaves in the same file: federation sync and status. The
// "[--peer name]" in their Use string is a FLAG hint, not a positional — both
// RunEs read no args, so a stray positional (bd federation sync bogus) was
// silently ignored with rc=0. add-peer/remove-peer correctly use ExactArgs.
//
// The federation subcommands are gated behind //go:build cgo (federation.go vs
// federation_nocgo.go, which registers only the bare parent), so these teeth are
// cgo-tagged to match — in a pure-Go build the leaves do not exist and would
// resolve to the "federation" parent.
func TestNoArgsSweep_FederationSyncStatusRejectPositional(t *testing.T) {
	commands := [][]string{
		{"federation", "sync"},
		{"federation", "status"},
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
