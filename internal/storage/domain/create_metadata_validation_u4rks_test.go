package domain

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/types"
)

// TestCreateValidatesMetadataSchema_u4rks pins beads-u4rks: the domain create()
// path (used by proxied-server `bd create`) must enforce the metadata SCHEMA
// validation the direct/embedded path applies via PrepareIssueForInsert ->
// ValidateMetadataIfConfigured. beads-lsbu added this to the domain UPDATE path
// but was scoped to update, leaving create able to persist schema-invalid
// metadata when a strict schema is configured — a mode-asymmetric
// boundary-validation bypass. When metadata_validation=error, create must reject
// metadata that violates the schema and accept valid metadata; when no schema is
// configured (the default), it must be a no-op.
func TestCreateValidatesMetadataSchema_u4rks(t *testing.T) {
	ctx := context.Background()
	cfg := func() *fakeConfigRepo {
		return &fakeConfigRepo{
			config:      map[string]string{"issue_prefix": "bd-", "issue_id_mode": "hash"},
			adaptiveCfg: DefaultAdaptiveConfig(),
		}
	}

	// Configure a strict schema: severity is a required enum(low|high).
	_ = config.Initialize()
	config.Set("validation.metadata.mode", "error")
	config.Set("validation.metadata.fields", map[string]interface{}{
		"severity": map[string]interface{}{
			"type": "enum", "required": true, "values": []interface{}{"low", "high"},
		},
	})
	t.Cleanup(func() {
		config.Set("validation.metadata.mode", "none")
		config.Set("validation.metadata.fields", nil)
	})

	t.Run("rejects schema-invalid metadata", func(t *testing.T) {
		lbl := &fakeLabelRepoIUC{}
		r := &fakeIssueRepo{}
		uc := newTestIssueUC(r, nil, lbl, nil, cfg())
		_, err := uc.CreateIssue(ctx, CreateIssueParams{
			Issue: &types.Issue{
				Title:     "bad meta",
				IssueType: types.TypeTask,
				Metadata:  json.RawMessage(`{"severity":"catastrophic"}`), // not in enum
			},
			ExplicitID: "bd-1",
		}, "actor")
		if err == nil {
			t.Fatal("expected create to reject schema-invalid metadata, got nil error")
		}
		if !strings.Contains(err.Error(), "metadata schema violation") {
			t.Errorf("want metadata-schema-violation error, got %q", err.Error())
		}
		if len(r.inserted) != 0 {
			t.Error("schema-invalid metadata must be rejected BEFORE insert")
		}
	})

	t.Run("rejects missing required field", func(t *testing.T) {
		uc := newTestIssueUC(&fakeIssueRepo{}, nil, nil, nil, cfg())
		_, err := uc.CreateIssue(ctx, CreateIssueParams{
			Issue: &types.Issue{
				Title:     "missing severity",
				IssueType: types.TypeTask,
				Metadata:  json.RawMessage(`{"other":"x"}`),
			},
			ExplicitID: "bd-2",
		}, "actor")
		if err == nil {
			t.Fatal("expected create to reject metadata missing required 'severity', got nil")
		}
	})

	t.Run("accepts schema-valid metadata", func(t *testing.T) {
		uc := newTestIssueUC(&fakeIssueRepo{}, nil, nil, nil, cfg())
		_, err := uc.CreateIssue(ctx, CreateIssueParams{
			Issue: &types.Issue{
				Title:     "good meta",
				IssueType: types.TypeTask,
				Metadata:  json.RawMessage(`{"severity":"high"}`),
			},
			ExplicitID: "bd-3",
		}, "actor")
		if err != nil {
			t.Fatalf("schema-valid metadata must be accepted: %v", err)
		}
	})
}
