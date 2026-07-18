//go:build cgo

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// beads-57nt: `bd relate` on an already-related pair must report "already
// related, no change" rather than a misleading "✓ Linked" (AddDependency is
// idempotent). The relate-side sibling of the unrelate fix (beads-piud) and the
// bwla dep-add re-add case.
func TestRelateAlreadyRelatedReportsNoChange(t *testing.T) {
	tmpDir := t.TempDir()
	testDB := filepath.Join(tmpDir, ".beads", "beads.db")
	s := newTestStore(t, testDB)
	ctx := context.Background()

	for _, id := range []string{"test-rc-1", "test-rc-2"} {
		if err := s.CreateIssue(ctx, &types.Issue{
			ID: id, Title: id, Status: types.StatusOpen, Priority: 2,
			IssueType: types.TypeTask, CreatedAt: time.Now(),
		}, "test"); err != nil {
			t.Fatalf("CreateIssue %s: %v", id, err)
		}
	}

	prevStore, prevCtx, prevJSON := store, rootCtx, jsonOutput
	store = s
	rootCtx = ctx
	jsonOutput = false
	t.Cleanup(func() { store, rootCtx, jsonOutput = prevStore, prevCtx, prevJSON })

	// First relate: a real link.
	first := captureStdout(t, func() error {
		return runRelate(relateCmd, []string{"test-rc-1", "test-rc-2"})
	})
	if !strings.Contains(first, "Linked") {
		t.Fatalf("expected 'Linked' on first relate: %s", first)
	}

	// Second relate of the same pair: idempotent no-op — must NOT claim it linked.
	second := captureStdout(t, func() error {
		return runRelate(relateCmd, []string{"test-rc-1", "test-rc-2"})
	})
	if strings.Contains(second, "✓ Linked") {
		t.Errorf("false success: re-relating an existing pair printed 'Linked': %s", second)
	}
	if !strings.Contains(second, "no change") {
		t.Errorf("expected 'already related, no change' on re-relate, got: %s", second)
	}
}
