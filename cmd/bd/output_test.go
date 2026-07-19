package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWrapWithSchemaVersion_Legacy_Object(t *testing.T) {
	input := map[string]string{"id": "beads-123", "title": "Test"}
	result := wrapWithSchemaVersion(input)

	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map[string]interface{}, got %T", result)
	}
	if m["schema_version"] != JSONSchemaVersion {
		t.Errorf("schema_version = %v, want %d", m["schema_version"], JSONSchemaVersion)
	}
	if m["id"] != "beads-123" {
		t.Errorf("id = %v, want beads-123", m["id"])
	}
}

func TestWrapWithSchemaVersion_Legacy_Slice(t *testing.T) {
	input := []string{"a", "b", "c"}
	result := wrapWithSchemaVersion(input)

	arr, ok := result.([]string)
	if !ok {
		t.Fatalf("expected []string (passthrough), got %T", result)
	}
	if len(arr) != 3 {
		t.Errorf("slice length = %d, want 3", len(arr))
	}
}

func TestWrapWithSchemaVersion_Legacy_Nil(t *testing.T) {
	result := wrapWithSchemaVersion(nil)
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map[string]interface{}, got %T", result)
	}
	if m["schema_version"] != JSONSchemaVersion {
		t.Errorf("schema_version = %v, want %d", m["schema_version"], JSONSchemaVersion)
	}
}

func TestWrapWithSchemaVersion_Envelope_Object(t *testing.T) {
	t.Setenv("BD_JSON_ENVELOPE", "1")

	input := map[string]string{"id": "beads-123", "title": "Test"}
	result := wrapWithSchemaVersion(input)

	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map[string]interface{}, got %T", result)
	}
	if m["schema_version"] != JSONSchemaVersion {
		t.Errorf("schema_version = %v, want %d", m["schema_version"], JSONSchemaVersion)
	}
	data, ok := m["data"]
	if !ok {
		t.Fatal("missing 'data' key in envelope")
	}
	inner, ok := data.(map[string]string)
	if !ok {
		t.Fatalf("data type = %T, want map[string]string", data)
	}
	if inner["id"] != "beads-123" {
		t.Errorf("data.id = %v, want beads-123", inner["id"])
	}
}

func TestWrapWithSchemaVersion_Envelope_Slice(t *testing.T) {
	t.Setenv("BD_JSON_ENVELOPE", "1")

	input := []string{"a", "b", "c"}
	result := wrapWithSchemaVersion(input)

	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected envelope map, got %T", result)
	}
	if m["schema_version"] != JSONSchemaVersion {
		t.Errorf("schema_version = %v, want %d", m["schema_version"], JSONSchemaVersion)
	}
	data, ok := m["data"]
	if !ok {
		t.Fatal("missing 'data' key in envelope")
	}
	arr, ok := data.([]string)
	if !ok {
		t.Fatalf("data type = %T, want []string", data)
	}
	if len(arr) != 3 {
		t.Errorf("data length = %d, want 3", len(arr))
	}
}

func TestWrapWithSchemaVersion_Envelope_Nil(t *testing.T) {
	t.Setenv("BD_JSON_ENVELOPE", "1")

	result := wrapWithSchemaVersion(nil)
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected envelope map, got %T", result)
	}
	if m["schema_version"] != JSONSchemaVersion {
		t.Errorf("schema_version = %v, want %d", m["schema_version"], JSONSchemaVersion)
	}
	if m["data"] != nil {
		t.Errorf("data = %v, want nil", m["data"])
	}
}

func TestWrapWithSchemaVersion_Envelope_RoundTrip(t *testing.T) {
	t.Setenv("BD_JSON_ENVELOPE", "1")

	input := map[string]interface{}{"count": 42, "name": "test"}
	result := wrapWithSchemaVersion(input)

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if parsed["schema_version"] != float64(JSONSchemaVersion) {
		t.Errorf("schema_version = %v, want %v", parsed["schema_version"], float64(JSONSchemaVersion))
	}
	innerData, ok := parsed["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data type = %T, want map[string]interface{}", parsed["data"])
	}
	if innerData["count"] != float64(42) {
		t.Errorf("data.count = %v, want 42", innerData["count"])
	}
}

// TestWarnReservedUserMapKeys is the teeth for the beads-z0fe read-side leg
// (PM ruling: the write guard stops NEW colliding keys, but a key stored before
// the guard landed is still silently clobbered when `bd kv list --json` /
// `bd memories --json` emit their flat map; warn on read so the residual loss
// is visible). The warning must fire for a reserved key, name it + the singular
// get hint, and stay silent for a map with no colliding keys.
func TestWarnReservedUserMapKeys(t *testing.T) {
	t.Run("warns_on_reserved_key", func(t *testing.T) {
		out := captureStderr(t, func() {
			warnReservedUserMapKeys(map[string]string{
				"schema_version": "clobbered",
				"normal_key":     "ok",
			}, "kv get")
		})
		if !strings.Contains(out, "schema_version") {
			t.Errorf("expected the warning to name the colliding key schema_version, got:\n%s", out)
		}
		if !strings.Contains(out, "kv get schema_version") {
			t.Errorf("expected the warning to include the singular read hint 'kv get schema_version', got:\n%s", out)
		}
		if strings.Contains(out, "normal_key") {
			t.Errorf("the warning must not name the non-colliding key normal_key, got:\n%s", out)
		}
	})

	t.Run("warns_on_data_key", func(t *testing.T) {
		out := captureStderr(t, func() {
			warnReservedUserMapKeys(map[string]string{"data": "clobbered under envelope"}, "recall")
		})
		if !strings.Contains(out, `"data"`) || !strings.Contains(out, "recall data") {
			t.Errorf("expected a warning naming 'data' with the 'recall data' hint, got:\n%s", out)
		}
	})

	t.Run("silent_when_no_collision", func(t *testing.T) {
		out := captureStderr(t, func() {
			warnReservedUserMapKeys(map[string]string{
				"feature_flag": "true",
				"auth-note":    "jwt",
			}, "kv get")
		})
		if strings.TrimSpace(out) != "" {
			t.Errorf("expected no warning for a collision-free map, got:\n%s", out)
		}
	})

	t.Run("silent_on_empty_map", func(t *testing.T) {
		out := captureStderr(t, func() {
			warnReservedUserMapKeys(map[string]string{}, "kv get")
		})
		if strings.TrimSpace(out) != "" {
			t.Errorf("expected no warning for an empty map, got:\n%s", out)
		}
	})
}
