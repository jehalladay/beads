package main

import (
	"fmt"
	"io"
)

// beads-lster: shared, json-guarded stderr side-effect for tracker-sync
// engine.OnWarning callbacks.
//
// engine.warn() (internal/tracker/engine.go) appends every warning to
// e.warnings, which is folded into SyncResult.Warnings INDEPENDENTLY of the
// OnWarning callback. So for the sync verbs whose --json path emits the full
// result (jira/linear via outputJSON(result), notion via writeNotionJSON) the
// warning already reaches the machine-readable warnings[] envelope. The
// callback's raw "Warning: ..." stderr line is therefore a pure display
// side-effect — and under --json it DOUBLE-reports (raw text on stderr plus the
// same string in warnings[]), interleaving non-JSON noise into a --json
// consumer's captured stderr, unlike every sibling warning site (all
// !jsonOutput-guarded) and matching the ado fix (beads-mfmcf).
//
// Suppressing it under --json is safe ONLY because the warning is envelope-
// backed; github/gitlab sync (which emit no json result) are deliberately NOT
// routed here — there stderr is the sole channel.
func emitSyncWarningStderr(w io.Writer, msg string) {
	if jsonOutput {
		return
	}
	_, _ = fmt.Fprintf(w, "Warning: %s\n", msg)
}
