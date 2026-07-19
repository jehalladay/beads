package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// beads-51fl LEG 1 (lav0 MARSHAL-variant): `bd backup status --json` built its
// payload with json.MarshalIndent + fmt.Println, bypassing
// outputJSON→wrapWithSchemaVersion, so the object OMITTED the top-level
// schema_version that every --json verb carries (docs/JSON_SCHEMA.md contract)
// and IGNORED BD_JSON_ENVELOPE=1. The fix routes the payload (backupStatusJSON)
// through outputJSON. backupStatusJSON is store-free, so the contract can be
// exercised directly. RED before the fix (no schema_version key).
func TestBackupStatusJSONCarriesSchemaVersion(t *testing.T) {
	oldJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = oldJSON })

	state := &backupState{LastDoltCommit: "abc123"}

	out := captureStdout(t, func() error {
		return emitBackupStatusJSON(state)
	})

	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		t.Fatalf("expected JSON on stdout, got empty")
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
		t.Fatalf("stdout is not a JSON object: %v\nstdout:\n%s", err, out)
	}
	if _, ok := m["schema_version"]; !ok {
		t.Fatalf("backup status --json missing top-level schema_version (beads-51fl contract violation): %v", m)
	}
	// The payload content must survive the wrapper.
	if _, ok := m["backup"]; !ok {
		t.Errorf("backup status --json lost the 'backup' key after wrapping: %v", m)
	}
}

// TestBackupStatusJSONEnvelopeMode is the envelope-contract half: with
// BD_JSON_ENVELOPE=1 the payload must be wrapped {schema_version, data:...}.
// The old raw-MarshalIndent site ignored the env var and emitted a bare object,
// so a .data consumer broke. RED before the fix.
func TestBackupStatusJSONEnvelopeMode(t *testing.T) {
	oldJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = oldJSON })
	t.Setenv("BD_JSON_ENVELOPE", "1")

	state := &backupState{LastDoltCommit: "abc123"}

	out := captureStdout(t, func() error {
		return emitBackupStatusJSON(state)
	})

	var m map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("stdout is not a JSON object: %v\nstdout:\n%s", err, out)
	}
	if _, ok := m["schema_version"]; !ok {
		t.Fatalf("envelope mode missing schema_version (beads-51fl): %v", m)
	}
	if _, ok := m["data"]; !ok {
		t.Fatalf("envelope mode must wrap payload under 'data' (beads-51fl): %v", m)
	}
}
