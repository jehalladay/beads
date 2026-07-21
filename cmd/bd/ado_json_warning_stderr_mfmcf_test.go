package main

import (
	"strings"
	"testing"
)

// beads-mfmcf: `bd ado sync` collects engine/pull/push warnings into the
// adoSyncResult.Warnings envelope (emitted under --json) AND, in human mode,
// echoes each as a raw "Warning: ..." line on stderr. Before this fix the
// OnWarning closure wrote that stderr line UNCONDITIONALLY — so under --json
// every warning was double-reported (raw text on stderr + the same string in
// the warnings[] array), interleaving non-JSON noise with a --json consumer's
// captured stderr, unlike every sibling warning site in runADOSync (all
// !jsonOutput-guarded) and unlike OnMessage.
//
// emitADOSyncWarningStderr is the extracted stderr side-effect; the collection
// (warnings = append(...)) stays in the closure so the warning still reaches
// the JSON envelope regardless of mode.
//
// MUTATION-VERIFIED: dropping the `if !jsonOutput` guard in
// emitADOSyncWarningStderr → TestADOSyncWarning_SuppressedInJSONMode_mfmcf goes
// RED (stderr leaks the raw "Warning:" line under --json).
func TestADOSyncWarning_SuppressedInJSONMode_mfmcf(t *testing.T) {
	prev := jsonOutput
	t.Cleanup(func() { jsonOutput = prev })

	// --json mode: no raw "Warning:" text may hit stderr (it lives in the
	// warnings[] envelope instead).
	jsonOutput = true
	got := captureStderr(t, func() {
		emitADOSyncWarningStderr("bootstrap match ambiguous for ADO #4242")
	})
	if strings.Contains(got, "Warning:") {
		t.Errorf("ado sync warning leaked to stderr under --json (beads-mfmcf): %q — must rely on the warnings[] envelope, not raw stderr text", got)
	}
	if strings.TrimSpace(got) != "" {
		t.Errorf("expected empty stderr under --json, got %q", got)
	}
}

func TestADOSyncWarning_EmittedInHumanMode_mfmcf(t *testing.T) {
	prev := jsonOutput
	t.Cleanup(func() { jsonOutput = prev })

	// Human mode: the raw "Warning:" line MUST still be printed so interactive
	// users see the warning (the fix must not silence the human path).
	jsonOutput = false
	const detail = "bootstrap match ambiguous for ADO #4242"
	got := captureStderr(t, func() {
		emitADOSyncWarningStderr(detail)
	})
	if !strings.Contains(got, "Warning: "+detail) {
		t.Errorf("human-mode ado sync warning not printed to stderr: got %q, want a line containing %q", got, "Warning: "+detail)
	}
}
