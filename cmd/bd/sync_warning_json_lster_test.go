package main

import (
	"bytes"
	"strings"
	"testing"
)

// beads-lster: emitSyncWarningStderr is the shared, json-guarded stderr
// side-effect for the jira/linear/notion sync engine.OnWarning callbacks. The
// warning already travels in result.Warnings (emitted under --json), so the raw
// "Warning: ..." stderr echo must be suppressed under --json (else it
// double-reports as non-JSON noise) but preserved in human mode. Mirrors the
// ado fix (beads-mfmcf).
//
// MUTATION-VERIFIED: dropping the `if jsonOutput { return }` guard in
// emitSyncWarningStderr → TestSyncWarning_SuppressedInJSONMode_lster goes RED
// (the raw "Warning:" line leaks to the writer under --json).
func TestSyncWarning_SuppressedInJSONMode_lster(t *testing.T) {
	prev := jsonOutput
	t.Cleanup(func() { jsonOutput = prev })

	jsonOutput = true
	var buf bytes.Buffer
	emitSyncWarningStderr(&buf, "bootstrap match ambiguous for JIRA-4242")

	if got := buf.String(); strings.Contains(got, "Warning:") {
		t.Errorf("sync warning leaked under --json (beads-lster): %q — must rely on the result.Warnings envelope, not raw stderr text", got)
	}
	if got := strings.TrimSpace(buf.String()); got != "" {
		t.Errorf("expected empty output under --json, got %q", got)
	}
}

func TestSyncWarning_EmittedInHumanMode_lster(t *testing.T) {
	prev := jsonOutput
	t.Cleanup(func() { jsonOutput = prev })

	jsonOutput = false
	const detail = "bootstrap match ambiguous for JIRA-4242"
	var buf bytes.Buffer
	emitSyncWarningStderr(&buf, detail)

	// Human mode: the raw "Warning:" line MUST still be printed so interactive
	// users see the warning (the fix must not silence the human path).
	if got := buf.String(); !strings.Contains(got, "Warning: "+detail) {
		t.Errorf("human-mode sync warning not printed: got %q, want a line containing %q", got, "Warning: "+detail)
	}
}
