//go:build cgo

package embeddeddolt_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/internal/storage/schema"
)

// advanceSchemaAhead inserts a schema_migrations row one past the binary's
// LatestVersion, simulating a DB written by a newer binary (forward drift).
func advanceSchemaAhead(t *testing.T, ctx context.Context, beadsDir, database string) {
	t.Helper()
	dataDir := filepath.Join(beadsDir, "embeddeddolt")
	db, cleanup, err := embeddeddolt.OpenSQL(ctx, dataDir, database, "main")
	if err != nil {
		t.Fatalf("OpenSQL: %v", err)
	}
	defer func() { _ = cleanup() }()
	if _, err := db.ExecContext(ctx,
		"INSERT INTO schema_migrations (version) VALUES (?)", schema.LatestVersion()+1); err != nil {
		t.Fatalf("insert future migration: %v", err)
	}
}

// TestEmbeddedWritableOpen_ForwardDrift_ReturnsSchemaSkewError is the beads-yd1g
// guard (embedded twin of beads-0j3h): a WRITABLE Open against a DB ahead of the
// binary must fail with a *schema.SchemaSkewError, matching OpenReadOnly. Before
// the fix the writable path only ran initSchema (no forward-drift check) and
// opened silently.
func TestEmbeddedWritableOpen_ForwardDrift_ReturnsSchemaSkewError(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	// Strip an inherited BD_IGNORE_SCHEMA_SKEW so this exercises the refuse path
	// even from a town seat that sets it (the 0j3h hermeticity lesson).
	t.Setenv("BD_IGNORE_SCHEMA_SKEW", "")

	ctx := t.Context()
	beadsDir := filepath.Join(t.TempDir(), ".beads")

	// Create + migrate the DB with a normal writable open, then close it so the
	// cache entry is released before the re-open.
	store, err := embeddeddolt.Open(ctx, beadsDir, "db", "main")
	if err != nil {
		t.Fatalf("Open (create): %v", err)
	}
	if err := store.Commit(ctx, "bd init"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	store.Close()

	advanceSchemaAhead(t, ctx, beadsDir, "db")

	// Re-open WRITABLE against the ahead DB: must now detect forward drift.
	reopened, rErr := embeddeddolt.Open(ctx, beadsDir, "db", "main")
	if rErr == nil {
		reopened.Close()
		t.Fatal("Open (writable) = nil, want error for forward schema drift on the write path")
	}
	if !schema.IsSchemaSkewError(rErr) {
		t.Fatalf("error = %T (%v), want error wrapping *schema.SchemaSkewError", rErr, rErr)
	}
}

// TestEmbeddedWritableOpen_ForwardDrift_EscapeHatch_Succeeds verifies the
// town-wide interim: BD_IGNORE_SCHEMA_SKEW=1 downgrades the write-path guard to a
// warning so the writable open still succeeds.
func TestEmbeddedWritableOpen_ForwardDrift_EscapeHatch_Succeeds(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	t.Setenv("BD_IGNORE_SCHEMA_SKEW", "1")

	ctx := t.Context()
	beadsDir := filepath.Join(t.TempDir(), ".beads")

	store, err := embeddeddolt.Open(ctx, beadsDir, "db", "main")
	if err != nil {
		t.Fatalf("Open (create): %v", err)
	}
	if err := store.Commit(ctx, "bd init"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	store.Close()

	advanceSchemaAhead(t, ctx, beadsDir, "db")

	reopened, rErr := embeddeddolt.Open(ctx, beadsDir, "db", "main")
	if rErr != nil {
		t.Fatalf("Open (writable, escape hatch) = %v, want nil", rErr)
	}
	reopened.Close()
}
