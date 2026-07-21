package main

import (
	"errors"
	"strings"
	"testing"
)

// beads-cxq3c: maybeAutoBackup runs from PersistentPostRun after EVERY command,
// including --json ones. Its Info skip-notice and its "auto-backup failed"
// warning are !isQuiet() && !jsonOutput-guarded, but the three "auto-backup
// skipped: %v" sites (backupDir / loadBackupState / GetCurrentCommit failures)
// wrote to stderr UNCONDITIONALLY — so on a --json invocation where auto-backup
// setup failed, the raw "Warning:" line polluted a --json consumer's captured
// stderr (the same leak class as beads-mfmcf's ado-sync warning). The extracted
// emitAutoBackupSkipWarning routes them through the sibling guard.
//
// MUTATION-VERIFIED: drop the `if !isQuiet() && !jsonOutput` guard in
// emitAutoBackupSkipWarning → TestAutoBackupSkipWarning_SuppressedInJSONMode_cxq3c
// goes RED (the raw "Warning:" line leaks under --json).
func TestAutoBackupSkipWarning_SuppressedInJSONMode_cxq3c(t *testing.T) {
	prevJSON, prevQuiet := jsonOutput, quietFlag
	t.Cleanup(func() { jsonOutput, quietFlag = prevJSON, prevQuiet })

	// --json mode: no auto-backup diagnostic may reach stderr — a --json
	// consumer capturing stderr must not see the post-command side-effect noise.
	jsonOutput = true
	quietFlag = false
	got := captureStderr(t, func() {
		emitAutoBackupSkipWarning(errors.New("failed to create directory /nope"))
	})
	if strings.Contains(got, "Warning:") {
		t.Errorf("auto-backup skip warning leaked to stderr under --json (beads-cxq3c): %q — must be suppressed like the sibling !jsonOutput-guarded warning", got)
	}
	if strings.TrimSpace(got) != "" {
		t.Errorf("expected empty stderr under --json, got %q", got)
	}
}

// TestAutoBackupSkipWarning_SuppressedWhenQuiet_cxq3c: --quiet also suppresses
// the diagnostic (mirrors the sibling guard's !isQuiet() leg).
func TestAutoBackupSkipWarning_SuppressedWhenQuiet_cxq3c(t *testing.T) {
	prevJSON, prevQuiet := jsonOutput, quietFlag
	t.Cleanup(func() { jsonOutput, quietFlag = prevJSON, prevQuiet })

	jsonOutput = false
	quietFlag = true
	got := captureStderr(t, func() {
		emitAutoBackupSkipWarning(errors.New("state unreadable"))
	})
	if strings.TrimSpace(got) != "" {
		t.Errorf("auto-backup skip warning printed under --quiet (beads-cxq3c): %q", got)
	}
}

// TestAutoBackupSkipWarning_EmittedInHumanMode_cxq3c: an interactive human run
// (no --json, no --quiet) MUST still see the diagnostic — the fix must not
// silence the operator-facing path.
func TestAutoBackupSkipWarning_EmittedInHumanMode_cxq3c(t *testing.T) {
	prevJSON, prevQuiet := jsonOutput, quietFlag
	t.Cleanup(func() { jsonOutput, quietFlag = prevJSON, prevQuiet })

	jsonOutput = false
	quietFlag = false
	const detail = "failed to create directory /nope"
	got := captureStderr(t, func() {
		emitAutoBackupSkipWarning(errors.New(detail))
	})
	if !strings.Contains(got, "Warning: auto-backup skipped: "+detail) {
		t.Errorf("human-mode auto-backup skip warning not printed: got %q, want a line containing %q", got, "Warning: auto-backup skipped: "+detail)
	}
}
