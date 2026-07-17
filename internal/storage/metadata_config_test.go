package storage

import (
	"encoding/json"
	"testing"
)

// TestValidateMetadataIfConfigured_NoneModeNoOp verifies the shared validator is
// a no-op when no metadata schema is configured (the default) — so wiring it
// into the domain/proxied write paths (beads-lsbu) never rejects a normal
// update on a rig without metadata_validation set.
func TestValidateMetadataIfConfigured_NoneModeNoOp(t *testing.T) {
	// No config initialized → LoadMetadataSchema returns mode "none".
	if got := LoadMetadataSchema(); got.Mode != "none" {
		t.Fatalf("LoadMetadataSchema mode = %q, want none (uninitialized config)", got.Mode)
	}
	if err := ValidateMetadataIfConfigured(json.RawMessage(`{"anything":"goes","n":123}`)); err != nil {
		t.Fatalf("ValidateMetadataIfConfigured in none-mode = %v, want nil", err)
	}
	// Malformed JSON is also tolerated in none-mode (no schema to check against);
	// well-formedness is enforced separately by NormalizeMetadataValue upstream.
	if err := ValidateMetadataIfConfigured(json.RawMessage(`not json`)); err != nil {
		t.Fatalf("ValidateMetadataIfConfigured(none-mode, malformed) = %v, want nil", err)
	}
}

// TestValidateMetadataSchema_ErrorModeReportsViolations exercises the underlying
// schema validation the shared helper delegates to, independent of config
// plumbing: an enum violation and an out-of-range int are reported.
func TestValidateMetadataSchema_ErrorModeReportsViolations(t *testing.T) {
	min := 0.0
	max := 10.0
	schema := MetadataSchemaConfig{
		Mode: "error",
		Fields: map[string]MetadataFieldSchema{
			"env":   {Type: MetadataFieldEnum, Values: []string{"dev", "prod"}, Required: true},
			"score": {Type: MetadataFieldInt, Min: &min, Max: &max},
		},
	}

	// Valid metadata → no errors.
	if errs := ValidateMetadataSchema(json.RawMessage(`{"env":"prod","score":5}`), schema); len(errs) != 0 {
		t.Fatalf("valid metadata reported %d errors: %v", len(errs), errs)
	}
	// Enum violation.
	if errs := ValidateMetadataSchema(json.RawMessage(`{"env":"staging","score":5}`), schema); len(errs) == 0 {
		t.Fatal("expected enum violation for env=staging")
	}
	// Out-of-range int.
	if errs := ValidateMetadataSchema(json.RawMessage(`{"env":"dev","score":99}`), schema); len(errs) == 0 {
		t.Fatal("expected range violation for score=99")
	}
	// Missing required field.
	if errs := ValidateMetadataSchema(json.RawMessage(`{"score":5}`), schema); len(errs) == 0 {
		t.Fatal("expected required violation for missing env")
	}
}
