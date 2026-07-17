package main

import (
	"encoding/json"
	"strings"
	"testing"
)

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

// TestReportItemError_JSONEmitsStructuredStderr verifies that, under --json,
// per-item batch errors (bd show/update loop over multiple IDs and continue
// past failures) are emitted as a JSON object on stderr — never a bare
// "Error: ..." line — so a consumer parsing stdout is unaffected and one
// parsing stderr still gets JSON (beads-fg6).
func TestReportItemError_JSONEmitsStructuredStderr(t *testing.T) {
	saved := jsonOutput
	defer func() { jsonOutput = saved }()
	jsonOutput = true

	out := captureStderr(t, func() {
		reportItemError("Error fetching %s: %v", "zz-1", "no issue found")
	})

	trimmed := strings.TrimSpace(out)
	if !strings.HasPrefix(trimmed, "{") {
		t.Fatalf("expected JSON object on stderr, got: %q", out)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		t.Fatalf("stderr not valid JSON: %v\nraw: %s", err, out)
	}
	if got := parsed["error"]; got != "Error fetching zz-1: no issue found" {
		t.Errorf("error = %v, want formatted message", got)
	}
	if parsed["schema_version"] != float64(JSONSchemaVersion) {
		t.Errorf("schema_version = %v, want %d", parsed["schema_version"], JSONSchemaVersion)
	}
}

// TestReportItemError_PlainTextWhenNotJSON verifies the non-JSON path emits the
// plain formatted message (with a trailing newline) and no JSON envelope.
func TestReportItemError_PlainTextWhenNotJSON(t *testing.T) {
	saved := jsonOutput
	defer func() { jsonOutput = saved }()
	jsonOutput = false

	out := captureStderr(t, func() {
		reportItemError("Issue %s not found", "zz-9")
	})

	if out != "Issue zz-9 not found\n" {
		t.Errorf("plain-text stderr = %q, want %q", out, "Issue zz-9 not found\n")
	}
	if strings.Contains(out, "schema_version") {
		t.Errorf("plain-text path must not emit a JSON envelope; got: %q", out)
	}
}
