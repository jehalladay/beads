package main

import (
	"os"
	"path/filepath"
	"testing"
)

// beads-2zkv: hermetic tests for hasBeadsPrimeHooks (settings.json prime-hook
// detection) and syncConflictCondition, both verified 0% + no test refs.

func writeSettings(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	return p
}

func TestHasBeadsPrimeHooks(t *testing.T) {
	t.Run("SessionStart with 'bd prime' → true", func(t *testing.T) {
		p := writeSettings(t, `{
		  "hooks": {
		    "SessionStart": [
		      {"hooks": [{"command": "bd prime"}]}
		    ]
		  }
		}`)
		if !hasBeadsPrimeHooks(p) {
			t.Error("expected true for a SessionStart bd-prime hook")
		}
	})

	t.Run("PreCompact with 'bd prime --stealth' → true", func(t *testing.T) {
		p := writeSettings(t, `{
		  "hooks": {
		    "PreCompact": [
		      {"hooks": [{"command": "bd prime --stealth"}]}
		    ]
		  }
		}`)
		if !hasBeadsPrimeHooks(p) {
			t.Error("expected true for a PreCompact bd-prime --stealth hook")
		}
	})

	t.Run("hooks present but different command → false", func(t *testing.T) {
		p := writeSettings(t, `{
		  "hooks": {"SessionStart": [{"hooks": [{"command": "echo hi"}]}]}
		}`)
		if hasBeadsPrimeHooks(p) {
			t.Error("expected false when no bd-prime command present")
		}
	})

	t.Run("no hooks key → false", func(t *testing.T) {
		if hasBeadsPrimeHooks(writeSettings(t, `{"other": 1}`)) {
			t.Error("expected false when there is no hooks key")
		}
	})

	t.Run("malformed JSON → false", func(t *testing.T) {
		if hasBeadsPrimeHooks(writeSettings(t, `{not json`)) {
			t.Error("expected false for unparseable settings")
		}
	})

	t.Run("missing file → false", func(t *testing.T) {
		if hasBeadsPrimeHooks(filepath.Join(t.TempDir(), "nope.json")) {
			t.Error("expected false for a missing settings file")
		}
	})
}

func TestSyncConflictCondition(t *testing.T) {
	// Currently a constant false (multi-rig routing removed); assert the contract.
	if syncConflictCondition() {
		t.Error("syncConflictCondition should be false")
	}
}
