package main

import "testing"

// TestADOSilenceFlags proves the `bd ado` subcommands (sync/status/projects)
// set SilenceUsage + SilenceErrors on their command literals (beads-pk3y,
// jbwv sibling class). Without them, cobra dumps a full usage block AND prints
// the returned exitError as "Error: exit code 1" to stderr on every error path
// — e.g. `bd ado sync` with an unconfigured PAT ("ado.pat not configured...")
// is a genuine runtime error, unrelated to arg/flag syntax, yet dumps usage.
// Matches depAddCmd (dep.go) and the landed relate/unrelate fix (beads-jbwv).
func TestADOSilenceFlags(t *testing.T) {
	for _, path := range [][]string{
		{"ado", "sync"},
		{"ado", "status"},
		{"ado", "projects"},
	} {
		cmd, _, err := rootCmd.Find(path)
		if err != nil {
			t.Fatalf("rootCmd.Find(%v): %v", path, err)
		}
		if cmd.Name() != path[len(path)-1] {
			t.Fatalf("resolved %q, want %q", cmd.Name(), path[len(path)-1])
		}
		if !cmd.SilenceUsage {
			t.Errorf("%s: SilenceUsage=false; want true so an error path does not dump the full cobra usage block", cmd.CommandPath())
		}
		if !cmd.SilenceErrors {
			t.Errorf("%s: SilenceErrors=false; want true so cobra does not print a bogus 'Error: exit code 1' to stderr", cmd.CommandPath())
		}
	}
}
