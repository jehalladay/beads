package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/tracker"
)

// TestOutputSyncResultCarriesSchemaVersion is teeth for beads-lav0: the raw
// json.NewEncoder(os.Stdout) sites bypass outputJSON()/wrapWithSchemaVersion,
// so their --json output OMITS the top-level schema_version that every
// outputJSON verb carries (docs/JSON_SCHEMA.md contract). outputSyncResult
// (sync_push_pull.go) is one such site and is store-free, so it exercises the
// contract directly. Before the fix this asserts RED (no schema_version key).
func TestOutputSyncResultCarriesSchemaVersion(t *testing.T) {
	oldJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = oldJSON })

	result := &tracker.SyncResult{Success: true}

	out := captureStdout(t, func() error {
		outputSyncResult(result, false)
		return nil
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
		t.Fatalf("JSON output missing top-level schema_version (beads-lav0 contract violation): %v", m)
	}
}

// TestOutputSyncResultEnvelopeMode is the envelope-contract teeth: with
// BD_JSON_ENVELOPE=1 every outputJSON site must wrap the payload as
// {schema_version, data:...}. A raw-encoder site ignores the env var and emits
// a bare payload, so a .data consumer breaks. RED before the fix.
func TestOutputSyncResultEnvelopeMode(t *testing.T) {
	oldJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = oldJSON })
	t.Setenv("BD_JSON_ENVELOPE", "1")

	result := &tracker.SyncResult{Success: true}

	out := captureStdout(t, func() error {
		outputSyncResult(result, false)
		return nil
	})

	var m map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("stdout is not a JSON object: %v\nstdout:\n%s", err, out)
	}
	if _, ok := m["data"]; !ok {
		t.Fatalf("envelope mode: JSON output missing 'data' key (beads-lav0): %v", m)
	}
	if _, ok := m["schema_version"]; !ok {
		t.Fatalf("envelope mode: JSON output missing 'schema_version' key (beads-lav0): %v", m)
	}
}

// TestPreflightChecklistCarriesSchemaVersion is teeth for the beads-kvmg
// checklist site (writePreflightChecklist), a raw-encoder path landed after the
// initial lav0 sweep: its --json ChecklistResult was encoded without
// wrapWithSchemaVersion, so it omitted the top-level schema_version. The site
// writes to an io.Writer (not os.Stdout), so it is wrapped in-place rather than
// via outputJSON. RED before the wrap, GREEN after.
func TestPreflightChecklistCarriesSchemaVersion(t *testing.T) {
	var buf bytes.Buffer
	if err := writePreflightChecklist(&buf, true); err != nil {
		t.Fatalf("writePreflightChecklist(json): %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("checklist --json is not a JSON object: %v\noutput:\n%s", err, buf.String())
	}
	if _, ok := m["schema_version"]; !ok {
		t.Fatalf("preflight checklist --json missing top-level schema_version (beads-lav0): %v", m)
	}
}
