package main

import "github.com/spf13/cobra"

// validateLimitFlag rejects a negative --limit value. --limit 0 is the
// documented "unlimited" sentinel, so only a negative value is invalid.
//
// This is the shared cmd-layer chokepoint every command that accepts --limit
// must route through. bd list guarded this inline (beads-uh4i), but the sibling
// read commands (ready/search/query/gate/find-duplicates/mol current) each read
// GetInt("limit") without a guard, so a negative --limit silently unbounded:
// the SQL builders emit a LIMIT clause only when filter.Limit > 0, so a negative
// Limit is FALSE → no LIMIT → the FULL result set, rc=0, no error — a misleading
// false-green where the user expected a bounded set or an error (beads-eqi4).
//
// Routing all --limit consumers through one helper means a new command can't
// reintroduce the gap by forgetting the guard. Callers pass the parsed limit and
// whether the flag was explicitly set (only a user-supplied negative is
// rejected; the unset default is left alone).
func validateLimitFlag(limit int, changed bool) error {
	if changed && limit < 0 {
		return HandleErrorRespectJSON("--limit must be >= 0")
	}
	return nil
}

// validateLimitFromCmd is the convenience form for a *cobra.Command: it reads
// the --limit flag and its changed state and validates in one call.
func validateLimitFromCmd(cmd *cobra.Command) error {
	limit, _ := cmd.Flags().GetInt("limit")
	return validateLimitFlag(limit, cmd.Flags().Changed("limit"))
}
