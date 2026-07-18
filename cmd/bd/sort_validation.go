package main

import "github.com/spf13/cobra"

// sortFieldsHelp is the canonical, single-source list of valid --sort field
// names, shared across the read commands (list/search/query) so their flag help
// and validation can't drift apart.
const sortFieldsHelp = "priority, created, updated, closed, status, id, title, type, assignee"

// validSortFields is the set backing sortFieldsHelp.
var validSortFields = map[string]bool{
	"priority": true, "created": true, "updated": true, "closed": true,
	"status": true, "id": true, "title": true, "type": true, "assignee": true,
}

// isValidSortField reports whether field is an accepted --sort key. An empty
// field (no --sort given) is treated as valid (the command's default order).
func isValidSortField(field string) bool {
	return field == "" || validSortFields[field]
}

// validateSortField rejects an unrecognized --sort field. bd list already
// failed loud on an invalid sort field, but the sibling read commands built the
// SQL/Go sort from the raw flag and silently fell back to priority order on an
// unknown key (sqlbuild.OrderByForColumns → SortDefs[""], and the client
// compareIssuesBy default) — a misleading false-green where the user believes
// the output is sorted by their (typo'd) field. Routing every --sort consumer
// through this one helper keeps validation and the documented field set in sync
// (beads-a9rk; sibling of beads-y04n for bd search).
func validateSortField(field string) error {
	if !isValidSortField(field) {
		return HandleErrorRespectJSON("invalid sort field %q (valid: %s)", field, sortFieldsHelp)
	}
	return nil
}

// validateSortFromCmd is the convenience form for a *cobra.Command: it reads the
// --sort flag and validates it in one call.
func validateSortFromCmd(cmd *cobra.Command) error {
	sortBy, _ := cmd.Flags().GetString("sort")
	return validateSortField(sortBy)
}
