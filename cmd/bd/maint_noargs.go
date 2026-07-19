package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// maintNoArgs rejects positional arguments for flag-only maintenance commands
// (flatten/gc/prune). These commands operate on the whole database and read no
// args[], so historically a stray positional was silently ignored — most
// dangerously "bd flatten mybead --force", which discards the "mybead"
// positional and irreversibly squashes ALL Dolt history (beads-ib1u). Mirror
// the bd list / bd count rejection so the mistake is loud instead of
// destructive, and name the command + point at --help.
func maintNoArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	msg := fmt.Sprintf("bd %s does not accept positional arguments; use flags instead (see bd %s --help)", cmd.Name(), cmd.Name())
	// beads-t1lx: honor the --json contract. cobra runs Args-validators after
	// flag-parse but before the global jsonOutput is set, so read --json off the
	// command directly. Under --json, emit a parseable JSON error object on
	// stdout and return an *exitError — main consumes that via exitCodeFromError
	// before its SilenceErrors plaintext-stderr print, so no plaintext leaks.
	if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
		jsonStdoutError(msg, "")
		return &exitError{Code: 1}
	}
	return fmt.Errorf("%s", msg)
}
