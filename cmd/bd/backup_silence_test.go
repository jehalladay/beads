package main

import "testing"

// TestBackupSilenceFlags proves the `bd backup` subcommands (init/sync/remove/
// restore/status) set SilenceUsage + SilenceErrors on their command literals
// (beads-myu1, jbwv/pk3y silence class). Without them, cobra dumps a full usage
// block AND prints the returned exitError as "Error: exit code 1" to stderr on
// every error path — e.g. `bd backup sync` with no destination configured, or
// `bd backup restore /nonexistent` — all genuine runtime errors unrelated to
// arg/flag syntax, yet dump usage. Matches depAddCmd and the landed relate/
// unrelate (jbwv), ado + duplicate/supersede (pk3y/b1jv) fixes.
func TestBackupSilenceFlags(t *testing.T) {
	for _, name := range []string{"init", "sync", "remove", "restore", "status"} {
		cmd, _, err := rootCmd.Find([]string{"backup", name})
		if err != nil {
			t.Fatalf("rootCmd.Find([backup %s]): %v", name, err)
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
