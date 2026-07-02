package main

import (
	"strings"
	"testing"
)

// TestParseAuditEntryFromStdin verifies the boundary validation on the audit
// record --stdin path: JSON must decode, "kind" is required (matching the flag
// branch), and an explicit actor overrides the decoded value (beads-r06.11).
func TestParseAuditEntryFromStdin(t *testing.T) {
	t.Run("ValidEntry", func(t *testing.T) {
		e, err := parseAuditEntryFromStdin([]byte(`{"kind":"tool_call","model":"opus"}`), "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if e.Kind != "tool_call" {
			t.Errorf("kind = %q, want %q", e.Kind, "tool_call")
		}
	})

	t.Run("MissingKindRejected", func(t *testing.T) {
		_, err := parseAuditEntryFromStdin([]byte(`{"model":"opus"}`), "")
		if err == nil {
			t.Fatal("expected error for entry missing kind, got nil")
		}
		if !strings.Contains(err.Error(), "kind") {
			t.Errorf("error should mention kind, got: %v", err)
		}
	})

	t.Run("BlankKindRejected", func(t *testing.T) {
		_, err := parseAuditEntryFromStdin([]byte(`{"kind":"   "}`), "")
		if err == nil {
			t.Fatal("expected error for blank kind, got nil")
		}
	})

	t.Run("InvalidJSONRejected", func(t *testing.T) {
		_, err := parseAuditEntryFromStdin([]byte(`{not json`), "")
		if err == nil {
			t.Fatal("expected error for invalid JSON, got nil")
		}
		if !strings.Contains(err.Error(), "invalid JSON") {
			t.Errorf("error should mention invalid JSON, got: %v", err)
		}
	})

	t.Run("ActorOverride", func(t *testing.T) {
		e, err := parseAuditEntryFromStdin([]byte(`{"kind":"tool_call","actor":"from_json"}`), "override_actor")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if e.Actor != "override_actor" {
			t.Errorf("actor = %q, want %q", e.Actor, "override_actor")
		}
	})
}
