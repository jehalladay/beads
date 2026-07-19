package main

import "testing"

// TestRelateUnrelateSilenceFlags is teeth for beads-jbwv: relateCmd and
// unrelateCmd defined RunE without SilenceUsage/SilenceErrors, so every error
// path dumped the full usage block AND printed "Error: <msg>" to stderr — the
// SilenceUsage/SilenceErrors usage-dump class (ref: depAddCmd dep.go:129-130).
// A pure command-tree check: no DB needed. RED before the flags are added.
func TestRelateUnrelateSilenceFlags(t *testing.T) {
	for _, path := range [][]string{
		{"dep", "relate"},
		{"dep", "unrelate"},
	} {
		cmd, _, err := rootCmd.Find(path)
		if err != nil {
			t.Fatalf("Find(%v): %v", path, err)
		}
		if !cmd.SilenceUsage {
			t.Errorf("%q: SilenceUsage=false; error paths dump the usage block (beads-jbwv)", path)
		}
		if !cmd.SilenceErrors {
			t.Errorf("%q: SilenceErrors=false; error paths print 'Error: ...' to stderr (beads-jbwv)", path)
		}
	}
}
