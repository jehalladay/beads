//go:build cgo

package embeddeddolt_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestMetadataFieldsRejectsScalarMetadata is the regression guard for beads-kkqu.
//
// The fnp6 per-key path (UpdateMetadataFields -> JSON_SET(COALESCE(metadata,
// '{}'), ...)) silently NO-OPPED when the existing metadata was a NON-OBJECT
// scalar (e.g. `42`): Dolt's JSON_SET on a scalar document returns the scalar
// unchanged, so the UPDATE succeeded but wrote nothing. Such rows are reachable
// via bd import's verbatim upsert (od9b) and pre-ef2k legacy rows. The old
// client-side read-modify-write ERRORED loudly on them; fnp6 turned that into a
// silent-write-loss. The fix reads the current value in-tx and rejects a
// non-null non-object.
//
// TEETH: revert the type-guard in ApplyMetadataKeyEditsInTx and this test goes
// RED — the set returns nil with metadata still == 42 (the silent no-op).
func TestMetadataFieldsRejectsScalarMetadata(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt tests")
	}
	ctx := context.Background()
	te := newTestEnv(t, "sp")

	const created = "sp-scalar1"
	issue := &types.Issue{
		ID:        created,
		Title:     "scalar-meta probe",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeBug,
	}
	if err := te.store.CreateIssue(ctx, issue, "tester"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	// Seed SCALAR metadata directly via raw SQL, bypassing the ef2k CLI gate
	// (mirrors bd import verbatim / a legacy row).
	te.exec(t, ctx, "UPDATE issues SET metadata = CAST('42' AS JSON) WHERE id = ?", created)

	// Per-key SET must ERROR (not silently no-op) on non-object metadata.
	err := te.store.UpdateMetadataFields(ctx, created,
		map[string]json.RawMessage{"team": json.RawMessage(`"platform"`)}, nil, "tester")
	if err == nil {
		var after string
		te.queryScalar(t, ctx, "SELECT metadata FROM issues WHERE id = ?", []any{created}, &after)
		t.Fatalf("expected an error setting a key on scalar metadata, got nil (silent no-op: metadata after = %s)", after)
	}
	if !strings.Contains(err.Error(), "not a JSON object") {
		t.Errorf("error should explain non-object metadata; got %v", err)
	}

	// Per-key UNSET must likewise error rather than silently no-op.
	if uerr := te.store.UpdateMetadataFields(ctx, created, nil, []string{"team"}, "tester"); uerr == nil {
		t.Errorf("expected an error unsetting a key on scalar metadata, got nil")
	}

	// The scalar must be untouched (the guard aborts before any write).
	var after string
	te.queryScalar(t, ctx, "SELECT metadata FROM issues WHERE id = ?", []any{created}, &after)
	if after != "42" {
		t.Errorf("metadata should be untouched by the rejected edits; got %s", after)
	}
}

// TestMetadataFieldsOnObjectAndNullMetadata confirms the guard does NOT regress
// the normal paths: a valid object row and a NULL-metadata row both accept a
// per-key set.
func TestMetadataFieldsOnObjectAndNullMetadata(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt tests")
	}
	ctx := context.Background()
	te := newTestEnv(t, "so")

	// (1) NULL metadata -> COALESCE default '{}' -> set works.
	nullRow := &types.Issue{ID: "so-null", Title: "null meta", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeBug}
	if err := te.store.CreateIssue(ctx, nullRow, "tester"); err != nil {
		t.Fatalf("CreateIssue null: %v", err)
	}
	if err := te.store.UpdateMetadataFields(ctx, "so-null",
		map[string]json.RawMessage{"a": json.RawMessage(`1`)}, nil, "tester"); err != nil {
		t.Fatalf("set on null-metadata row should succeed: %v", err)
	}
	var afterNull string
	te.queryScalar(t, ctx, "SELECT metadata FROM issues WHERE id = ?", []any{"so-null"}, &afterNull)
	if !strings.Contains(afterNull, `"a"`) {
		t.Errorf("expected key a set on null-metadata row; got %s", afterNull)
	}

	// (2) Existing object metadata -> set merges, preserving siblings.
	objRow := &types.Issue{ID: "so-obj", Title: "obj meta", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeBug}
	if err := te.store.CreateIssue(ctx, objRow, "tester"); err != nil {
		t.Fatalf("CreateIssue obj: %v", err)
	}
	te.exec(t, ctx, `UPDATE issues SET metadata = CAST('{"keep":"me"}' AS JSON) WHERE id = ?`, "so-obj")
	if err := te.store.UpdateMetadataFields(ctx, "so-obj",
		map[string]json.RawMessage{"add": json.RawMessage(`"new"`)}, nil, "tester"); err != nil {
		t.Fatalf("set on object-metadata row should succeed: %v", err)
	}
	var afterObj string
	te.queryScalar(t, ctx, "SELECT metadata FROM issues WHERE id = ?", []any{"so-obj"}, &afterObj)
	if !strings.Contains(afterObj, `"keep"`) || !strings.Contains(afterObj, `"add"`) {
		t.Errorf("expected both keep and add on object-metadata row; got %s", afterObj)
	}
}
