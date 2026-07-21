package main

import (
	"strings"
	"testing"
)

// beads-80evy: maybeAutoBackup is a best-effort PersistentPostRun side-effect.
// Its backupDir/loadBackupState/GetCurrentCommit failure legs emitted
// "Warning: auto-backup skipped: ..." on stderr UNCONDITIONALLY, unlike the
// sibling notices in the same function (the one-time Info at the
// remote-filesystem skip, and the "auto-backup failed" warning), which both
// guard with !isQuiet() && !jsonOutput. So a --json consumer that captured
// stderr got non-JSON warning noise on any backup-dir/state/commit error —
// the intra-function-inconsistent twin of mfmcf (ado sync stderr leak).
//
// emitAutoBackupSkipWarning is the extracted, json-guarded stderr side-effect.
//
// MUTATION-VERIFIED: dropping the `!jsonOutput` guard in
// emitAutoBackupSkipWarning → TestAutoBackupSkipWarning_SuppressedInJSONMode_80evy
// goes RED (stderr leaks the raw "Warning: auto-backup skipped:" line).
func TestAutoBackupSkipWarning_SuppressedInJSONMode_80evy(t *testing.T) {
	prev := jsonOutput
	t.Cleanup(func() { jsonOutput = prev })

	// --json mode: no raw "Warning:" text may hit stderr — a --json consumer
	// must get clean stdout/stderr, not interleaved human warning noise.
	jsonOutput = true
	got := captureStderr(t, func() {
		emitAutoBackupSkipWarning("resolve backup dir: permission denied")
	})
	if strings.Contains(got, "Warning:") {
		t.Errorf("auto-backup skip warning leaked to stderr under --json (beads-80evy): %q", got)
	}
	if strings.TrimSpace(got) != "" {
		t.Errorf("expected empty stderr under --json, got %q", got)
	}
}

func TestAutoBackupSkipWarning_EmittedInHumanMode_80evy(t *testing.T) {
	prev := jsonOutput
	t.Cleanup(func() { jsonOutput = prev })

	// Human mode: the warning MUST still print so interactive operators see
	// that auto-backup was skipped (the fix must not silence the human path).
	jsonOutput = false
	const detail = "failed to get current commit: dial tcp: connection refused"
	got := captureStderr(t, func() {
		emitAutoBackupSkipWarning(detail)
	})
	if !strings.Contains(got, "Warning: auto-backup skipped: "+detail) {
		t.Errorf("human-mode auto-backup skip warning not printed: got %q, want a line containing %q", got, "Warning: auto-backup skipped: "+detail)
	}
}
