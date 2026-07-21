package main

import (
	"strings"
	"testing"
)

// TestNoArgsSweep_GXHS4_ResidualSiblingsRejectPositional covers the two zero-arg
// leaves the zrp4s sweep left behind: `rules audit` and `mol wisp list`. zrp4s
// added cobra.NoArgs to their same-file siblings (rules compact, mol wisp gc) but
// not to these — the exact intra-file inconsistency that signals the gap. Each is
// flag-driven (its RunE never reads the positional args slice), so without NoArgs
// a stray positional (e.g. `bd rules audit junk`, `bd mol wisp list junk`) is
// silently swallowed with exit 0 (silent false-success).
func TestNoArgsSweep_GXHS4_ResidualSiblingsRejectPositional(t *testing.T) {
	commands := [][]string{
		{"rules", "audit"},
		{"mol", "wisp", "list"},
	}
	for _, path := range commands {
		path := path
		name := path[len(path)-1]
		t.Run(strings.Join(path, "_"), func(t *testing.T) {
			cmd, _, err := rootCmd.Find(path)
			if err != nil {
				t.Fatalf("rootCmd.Find(%v): %v", path, err)
			}
			// Ensure we resolved the leaf, not a parent that swallowed the tail.
			if cmd.Name() != name {
				t.Fatalf("resolved %q, want leaf %q (parent swallowed the tail?)", cmd.Name(), name)
			}
			if cmd.Args == nil {
				t.Fatalf("%v: Args validator is nil (missing cobra.NoArgs)", path)
			}
			if err := cmd.Args(cmd, []string{"stray"}); err == nil {
				t.Errorf("%v: accepted a stray positional (want rejection)", path)
			}
			if err := cmd.Args(cmd, nil); err != nil {
				t.Errorf("%v: rejected the no-arg invocation (%v)", path, err)
			}
		})
	}
}
