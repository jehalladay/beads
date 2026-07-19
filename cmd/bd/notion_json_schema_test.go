package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/steveyegge/beads/internal/notion"
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

// TestNotionJSONWritesToCommandWriter_weyi guards the two defects the original
// lav0 fix introduced (beads-weyi):
//
//  1. Writer routing: writeNotionJSON's callers (and their tests) capture
//     cmd.OutOrStdout(), which is redirected to a buffer under test. The first
//     fix used outputJSON, which writes to os.Stdout unconditionally, leaving
//     the buffer EMPTY (decode fails "unexpected end of JSON input"). The
//     emitter must honor the command's writer.
//  2. Reserved-key collision: notion.StatusResponse declared its
//     Notion-database schema version under json:"schema_version" — the SAME key
//     the beads envelope injects (as an int). Routing through the envelope
//     clobbered that string field with 1, breaking StatusResponse decode. The
//     domain field is retagged to notion_schema_version so both coexist.
//
// Using a real StatusResponse (not a synthetic map) is what surfaces (2); the
// original teeth used a map and missed it.
func TestNotionJSONWritesToCommandWriter_weyi(t *testing.T) {
	oldJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = oldJSON })

	resp := &notion.StatusResponse{Configured: true, DataSourceID: "ds-1"}

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	if err := writeNotionJSON(cmd, resp); err != nil {
		t.Fatalf("writeNotionJSON: %v", err)
	}

	// Defect 1: the command's writer must receive the output.
	if buf.Len() == 0 {
		t.Fatal("writeNotionJSON wrote nothing to cmd.OutOrStdout() (routed to os.Stdout instead); beads-weyi")
	}

	// Defect 2: a typed StatusResponse must still decode — the injected int
	// envelope key must not collide with a domain string field.
	var decoded notion.StatusResponse
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("StatusResponse --json does not round-trip (reserved-key collision?): %v\n%s", err, buf.String())
	}
	if !decoded.Configured || decoded.DataSourceID != "ds-1" {
		t.Fatalf("payload fields lost: %+v", decoded)
	}

	// And the beads envelope key must still be present (the lav0 goal).
	var m map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("not a JSON object: %v", err)
	}
	if _, ok := m["schema_version"]; !ok {
		t.Fatalf("notion --json missing beads schema_version envelope key: %s", buf.String())
	}
}
