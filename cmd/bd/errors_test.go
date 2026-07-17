package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReportItemError_JSONMode(t *testing.T) {
	// Under --json, reportItemError must emit a structured JSON error object to
	// stderr (stdout is reserved for the parseable success payload) — never a
	// bare "Error ..." plain-text line (beads-fg6).
	prev := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = prev }()

	out := captureStderr(t, func() {
		reportItemError("Error fetching %s: %v", "beads-zzzzz", "no issue found")
	})

	trimmed := strings.TrimSpace(out)
	if !strings.HasPrefix(trimmed, "{") {
		t.Fatalf("expected JSON object on stderr, got non-JSON: %q", out)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("stderr is not valid JSON: %v\noutput: %q", err, out)
	}
	if parsed["error"] != "Error fetching beads-zzzzz: no issue found" {
		t.Errorf("error field = %v, want formatted message", parsed["error"])
	}
	if parsed["schema_version"] != float64(JSONSchemaVersion) {
		t.Errorf("schema_version = %v, want %d", parsed["schema_version"], JSONSchemaVersion)
	}
}

func TestReportItemError_JSONMode_StdoutStaysClean(t *testing.T) {
	// The parseable success payload lives on stdout; a per-item error under
	// --json must NOT leak anything onto stdout.
	prev := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = prev }()

	stdout := captureStdout(t, func() error {
		reportItemError("Issue %s not found", "beads-zzzzz")
		return nil
	})
	if stdout != "" {
		t.Errorf("stdout should be empty under --json, got %q", stdout)
	}
}

func TestReportItemError_PlainMode(t *testing.T) {
	// Without --json, reportItemError prints the plain-text line to stderr,
	// preserving the pre-existing human-readable behavior.
	prev := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = prev }()

	out := captureStderr(t, func() {
		reportItemError("Issue %s not found", "beads-zzzzz")
	})
	if out != "Issue beads-zzzzz not found\n" {
		t.Errorf("plain-text output = %q, want %q", out, "Issue beads-zzzzz not found\n")
	}
	if strings.Contains(out, "{") {
		t.Errorf("plain mode must not emit JSON, got %q", out)
	}
}

func TestJsonStderrError_StructuredOutput(t *testing.T) {
	tests := []struct {
		name    string
		message string
		hint    string
	}{
		{"message_only", "database not found", ""},
		{"message_with_hint", "database not found", "Run 'bd init' to create one"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := map[string]interface{}{
				"schema_version": JSONSchemaVersion,
				"error":          tt.message,
			}
			if tt.hint != "" {
				obj["hint"] = tt.hint
			}

			data, err := json.Marshal(obj)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			var parsed map[string]interface{}
			if err := json.Unmarshal(data, &parsed); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			if parsed["schema_version"] != float64(JSONSchemaVersion) {
				t.Errorf("schema_version = %v, want %d", parsed["schema_version"], JSONSchemaVersion)
			}
			if parsed["error"] != tt.message {
				t.Errorf("error = %v, want %s", parsed["error"], tt.message)
			}
			if tt.hint != "" {
				if parsed["hint"] != tt.hint {
					t.Errorf("hint = %v, want %s", parsed["hint"], tt.hint)
				}
			} else {
				if _, ok := parsed["hint"]; ok {
					t.Errorf("hint should not be present when empty")
				}
			}
		})
	}
}
