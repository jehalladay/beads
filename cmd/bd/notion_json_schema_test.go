package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// beads-lav0 (missed site notion.go:691): writeNotionJSON used a raw
// json.NewEncoder(cmd.OutOrStdout()), bypassing outputJSON→wrapWithSchemaVersion,
// so every `bd notion {status,init,connect,sync}` --json object omitted the
// top-level schema_version and ignored BD_JSON_ENVELOPE=1. The lav0 discovery
// grep matched json.NewEncoder(os.Stdout) but missed the cmd.OutOrStdout()
// variant (flagged by beads_sr_pm). All 5 callers pass an object, so the fix
// (route through outputJSON) adds schema_version in BOTH plain and envelope
// mode. RED before the fix (bare object, no schema_version key).

func TestNotionJSONCarriesSchemaVersion_lav0(t *testing.T) {
	oldJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = oldJSON })

	payload := map[string]any{"configured": false, "data_source_id": "ds-1"}

	out := captureStdout(t, func() error {
		return writeNotionJSON(&cobra.Command{}, payload)
	})

	trimmed := strings.TrimSpace(out)
	var m map[string]any
	if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
		t.Fatalf("notion --json is not a JSON object: %v\nstdout:\n%s", err, trimmed)
	}
	if _, ok := m["schema_version"]; !ok {
		t.Fatalf("notion --json (plain mode) missing schema_version (beads-lav0 missed site): %v", m)
	}
	// The original object fields must survive alongside schema_version.
	if _, ok := m["configured"]; !ok {
		t.Fatalf("notion --json dropped the payload fields: %v", m)
	}
}

func TestNotionJSONEnvelopeMode_lav0(t *testing.T) {
	oldJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = oldJSON })
	t.Setenv("BD_JSON_ENVELOPE", "1")

	payload := map[string]any{"configured": true, "data_source_id": "ds-1"}

	out := captureStdout(t, func() error {
		return writeNotionJSON(&cobra.Command{}, payload)
	})

	trimmed := strings.TrimSpace(out)
	var m map[string]any
	if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
		t.Fatalf("notion --json (envelope) is not a JSON object: %v\nstdout:\n%s", err, trimmed)
	}
	if _, ok := m["schema_version"]; !ok {
		t.Fatalf("notion --json (envelope) missing schema_version (beads-lav0): %v", m)
	}
	if _, ok := m["data"]; !ok {
		t.Fatalf("notion --json (envelope) must wrap the payload under 'data' (beads-lav0): %v", m)
	}
}
