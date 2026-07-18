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

// TestUnrelateNoOpHonest_piud is the regression teeth for beads-piud: bd unrelate
// on a pair that has NO relates-to link must fail loud (rc!=0 + honest error),
// not report a false "✓ Unlinked" / unrelated:true. RemoveDependency is
// idempotent (returns nil on a no-op), so without the edge-exists pre-check the
// CLI verb reported success on a removal that did nothing — the w2tk/yaux
// false-success class. A real relates-to link, by contrast, must still remove
// cleanly and report success.
func TestUnrelateNoOpHonest_piud(t *testing.T) {
	tmpDir := t.TempDir()
	testDB := filepath.Join(tmpDir, ".beads", "beads.db")
	s := newTestStore(t, testDB)
	ctx := context.Background()

	mk := func(id string) *types.Issue {
		return &types.Issue{
			ID:        id,
			Title:     id,
			Status:    types.StatusOpen,
			Priority:  1,
			IssueType: types.TypeTask,
			CreatedAt: time.Now(),
		}
	}
	for _, id := range []string{"piud-a", "piud-b", "piud-c"} {
		if err := s.CreateIssue(ctx, mk(id), "test"); err != nil {
			t.Fatalf("CreateIssue %s failed: %v", id, err)
		}
	}

	// Point the package globals runUnrelate reads at the test store.
	prevStore, prevCtx, prevJSON := store, rootCtx, jsonOutput
	store = s
	rootCtx = ctx
	jsonOutput = false
	t.Cleanup(func() { store, rootCtx, jsonOutput = prevStore, prevCtx, prevJSON })

	// No-op: a and c were never related → must be a loud error, not a false success.
	t.Run("unrelate_never_related_fails_loud", func(t *testing.T) {
		out, err := captureStdoutExpectErr(t, func() error {
			return runUnrelate(unrelateCmd, []string{"piud-a", "piud-c"})
		})
		if err == nil {
			t.Fatalf("expected a non-nil error unrelating a never-related pair, got nil (stdout=%q)", out)
		}
		if strings.Contains(out, "Unlinked") {
			t.Errorf("no-op unrelate must NOT print a false '✓ Unlinked' success; stdout=%q", out)
		}
	})

	// A genuine link must still unrelate cleanly (guards against over-tightening).
	t.Run("unrelate_real_link_succeeds", func(t *testing.T) {
		for _, d := range []*types.Dependency{
			{IssueID: "piud-a", DependsOnID: "piud-b", Type: types.DepRelatesTo},
			{IssueID: "piud-b", DependsOnID: "piud-a", Type: types.DepRelatesTo},
		} {
			if err := s.AddDependency(ctx, d, "test"); err != nil {
				t.Fatalf("AddDependency %s->%s failed: %v", d.IssueID, d.DependsOnID, err)
			}
		}
		// captureStdout fails the test itself on a non-nil error, so a successful
		// return here is the assertion that a real link unrelates cleanly.
		out := captureStdout(t, func() error {
			return runUnrelate(unrelateCmd, []string{"piud-a", "piud-b"})
		})
		if !strings.Contains(out, "Unlinked") {
			t.Errorf("unrelate on a real relates-to link should report '✓ Unlinked'; stdout=%q", out)
		}
		// Second unrelate of the now-removed link must be a loud no-op.
		if _, err := captureStdoutExpectErr(t, func() error {
			return runUnrelate(unrelateCmd, []string{"piud-a", "piud-b"})
		}); err == nil {
			t.Errorf("re-unrelating an already-removed link must fail loud (idempotent no-op is a false success)")
		}
	})
}
