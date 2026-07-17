package dolt

import (
	"context"
	"fmt"
	"testing"

	"github.com/steveyegge/beads/internal/storage/schema"
)

// TestDoltNew_Writable_ForwardDrift_ReturnsSchemaSkewError is the beads-0j3h
// guard: opening a WRITABLE store against a DB that is ahead of the binary must
// fail with a *schema.SchemaSkewError, just like the read-only path. Before the
// fix the write path only ran initSchema (which no-ops on an ahead DB — nothing
// pending) and opened silently, letting a v53-class binary write a v54 DB and
// risk a silently lost concurrent write.
func TestDoltNew_Writable_ForwardDrift_ReturnsSchemaSkewError(t *testing.T) {
	skipIfNoDolt(t)
	// Strip an inherited BD_IGNORE_SCHEMA_SKEW (town seats set it during the live
	// v54/v53 skew) so this test exercises the non-escape-hatch refuse path.
	t.Setenv("BD_IGNORE_SCHEMA_SKEW", "")

	ctx, cancel := testContext(t)
	defer cancel()

	tmpDir := t.TempDir()
	dbName := uniqueTestDBName(t)

	// Create + fully migrate the DB with a normal writable open.
	store, err := New(ctx, &Config{
		Path:            tmpDir,
		CommitterName:   "test",
		CommitterEmail:  "test@example.com",
		Database:        dbName,
		CreateIfMissing: true,
	})
	if err != nil {
		t.Fatalf("New (create): %v", err)
	}

	// Advance schema_migrations by one to simulate a DB from a newer binary,
	// then close so the next open is a clean re-open of the ahead DB.
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO schema_migrations (version) VALUES (?)",
		schema.LatestVersion()+1,
	); err != nil {
		t.Fatalf("insert future migration: %v", err)
	}
	defer func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 5*testTimeout)
		defer dropCancel()
		_, _ = store.db.ExecContext(dropCtx, fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", dbName))
		store.Close()
	}()

	// Re-open WRITABLE (ReadOnly:false, no CreateIfMissing) against the ahead DB.
	// This must now detect the forward drift and refuse, mirroring the read path.
	wStore, wErr := New(ctx, &Config{
		Path:           tmpDir,
		CommitterName:  "test",
		CommitterEmail: "test@example.com",
		Database:       dbName,
	})
	if wErr == nil {
		wStore.Close()
		t.Fatal("New (writable) = nil, want error for forward schema drift on the write path")
	}
	if !schema.IsSchemaSkewError(wErr) {
		t.Fatalf("error = %T (%v), want error wrapping *schema.SchemaSkewError", wErr, wErr)
	}
}

// TestDoltNew_Writable_ForwardDrift_EscapeHatch_Succeeds verifies the town-wide
// interim: BD_IGNORE_SCHEMA_SKEW=1 downgrades the write-path forward-drift guard
// to a warning so a writable open against an ahead DB still succeeds.
func TestDoltNew_Writable_ForwardDrift_EscapeHatch_Succeeds(t *testing.T) {
	skipIfNoDolt(t)
	t.Setenv("BD_IGNORE_SCHEMA_SKEW", "1")

	ctx, cancel := testContext(t)
	defer cancel()

	tmpDir := t.TempDir()
	dbName := uniqueTestDBName(t)

	store, err := New(ctx, &Config{
		Path:            tmpDir,
		CommitterName:   "test",
		CommitterEmail:  "test@example.com",
		Database:        dbName,
		CreateIfMissing: true,
	})
	if err != nil {
		t.Fatalf("New (create): %v", err)
	}
	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO schema_migrations (version) VALUES (?)",
		schema.LatestVersion()+1,
	); err != nil {
		t.Fatalf("insert future migration: %v", err)
	}
	defer func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 5*testTimeout)
		defer dropCancel()
		_, _ = store.db.ExecContext(dropCtx, fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", dbName))
		store.Close()
	}()

	wStore, wErr := New(ctx, &Config{
		Path:           tmpDir,
		CommitterName:  "test",
		CommitterEmail: "test@example.com",
		Database:       dbName,
	})
	if wErr != nil {
		t.Fatalf("New (writable with escape hatch) = %v, want nil", wErr)
	}
	wStore.Close()
}
