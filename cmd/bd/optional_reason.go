package main

import "strings"

// normalizeOptionalReason collapses a whitespace-only --reason to "" ("not
// provided") for commands where --reason is optional and has no default, so a
// value like "   " falls through to the no-reason path instead of storing a
// dangling blank suffix (e.g. "Dismissed:    " / "Promoted...:    ") or leaking
// a blank reason into output. A genuine reason is returned VERBATIM (no trim)
// to preserve its formatting.
//
// This is the shared form of the beads-in93a stored-blank-reason fix already
// applied inline in reopen (beads-5rix3, normalizeReopenReason) and the gate /
// set-state family (beads-57f51). Used by bd promote and bd human dismiss
// (beads-tg1js). Commands whose --reason has a DEFAULT (todo done, close) or is
// REQUIRED validate empties differently and do not use this helper.
func normalizeOptionalReason(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return ""
	}
	return reason
}
