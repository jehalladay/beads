package main

import "testing"

// TestDuplicateSupersedeSilenceFlags proves `bd duplicate` and `bd supersede`
// set SilenceUsage + SilenceErrors on their command literals (beads-pk3y,
// jbwv sibling class). Without them, cobra dumps a full usage block AND prints
// the returned exitError as "Error: exit code 1" to stderr on every error path
// — e.g. `bd duplicate` (missing arg) or `bd supersede foo` (missing --with)
// both dump usage. Matches depAddCmd (dep.go) and the landed relate/unrelate
// (beads-jbwv) + ado (beads-pk3y ado slice) fixes.
func TestDuplicateSupersedeSilenceFlags(t *testing.T) {
	for _, name := range []string{"duplicate", "supersede"} {
		cmd, _, err := rootCmd.Find([]string{name})
		if err != nil {
			t.Fatalf("rootCmd.Find([%s]): %v", name, err)
		}
		if cmd.Name() != name {
			t.Fatalf("resolved %q, want %q", cmd.Name(), name)
		}
		if !cmd.SilenceUsage {
			t.Errorf("%s: SilenceUsage=false; want true so an error path does not dump the full cobra usage block", cmd.CommandPath())
		}
		if !cmd.SilenceErrors {
			t.Errorf("%s: SilenceErrors=false; want true so cobra does not print a bogus 'Error: exit code 1' to stderr", cmd.CommandPath())
		}
	}
}
