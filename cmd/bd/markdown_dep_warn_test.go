//go:build cgo

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestCreateIssuesFromMarkdown_SkippedDepWarns verifies beads-r2lq: markdown
// import must surface skipped dependencies instead of dropping them silently.
// Two independent drop points are covered:
//  1. a dependency whose target does not exist -> OnSkippedDependency fires a
//     warning (previously the skip was recorded to a nil callback = invisible);
//  2. a typed dependency with a blank target ("blocks:") -> empty DependsOnID
//     resolves to "target not found" and hits the same OnSkippedDependency path.
//
// In every case the issue itself must still be created (skip-and-warn, matching
// the JSONL/create-deps precedent — not fail-hard).
func TestCreateIssuesFromMarkdown_SkippedDepWarns(t *testing.T) {
	setup := func(t *testing.T, md string) (dir string) {
		t.Helper()
		tmpDir := t.TempDir()
		beadsDir := filepath.Join(tmpDir, ".beads")
		if err := os.Mkdir(beadsDir, 0755); err != nil {
			t.Fatal(err)
		}
		dbPath := filepath.Join(beadsDir, "dolt")

		oldStore, oldActor, oldCtx := store, actor, rootCtx
		s := newTestStoreIsolatedDB(t, dbPath, "r2lq")
		store = s
		actor = "r2lq"
		rootCtx = context.Background()
		t.Cleanup(func() { store, actor, rootCtx = oldStore, oldActor, oldCtx })

		mdPath := filepath.Join(tmpDir, "issues.md")
		if err := os.WriteFile(mdPath, []byte(md), 0644); err != nil {
			t.Fatal(err)
		}
		return mdPath
	}

	t.Run("nonexistent dependency target warns and still creates the issue", func(t *testing.T) {
		// The target MUST share the store's prefix ("r2lq"). beads-77i6 makes a
		// CROSS-prefix target external (ClassifyDepTarget→DepTargetExternal), which
		// skips the local existence check entirely, so a cross-prefix "nonexistent"
		// target is accepted as a valid external ref and never triggers the
		// "target not found" skip-and-warn. Using a same-prefix nonexistent target
		// exercises the local existence-check path this test is asserting (beads-2nrc).
		md := "## Issue With Bad Dep\n\n" +
			"Body text.\n\n" +
			"### Dependencies\n\n" +
			"r2lq-nonexistent-99999\n"
		mdPath := setup(t, md)

		out := captureStderr(t, func() {
			if err := createIssuesFromMarkdown(nil, mdPath, false); err != nil {
				t.Fatalf("createIssuesFromMarkdown returned error (expected skip-and-warn, not fail): %v", err)
			}
		})

		if !strings.Contains(out, "skipped dependency") {
			t.Errorf("expected a 'skipped dependency' warning for the nonexistent target, got: %q", out)
		}

		// The issue must still exist (skip the edge, keep the issue).
		n, err := store.CountIssues(rootCtx, "", types.IssueFilter{})
		if err != nil {
			t.Fatalf("CountIssues failed: %v", err)
		}
		if n != 1 {
			t.Fatalf("expected the issue to be created despite the bad dep, got %d issues", n)
		}
	})

	t.Run("empty dependency target (typed, blank after colon) warns and still creates", func(t *testing.T) {
		// "blocks:" survives parseStringList (non-empty token) but its target is
		// empty after the ":" split -> an empty DependsOnID that PersistDependencies
		// cannot resolve, hitting the same OnSkippedDependency path. (A bare empty
		// token like ", " is dropped by parseStringList before it reaches the
		// dep loop, so the reachable "empty" case is this typed-blank-target form.)
		md := "## Issue With Empty Target\n\n" +
			"Body text.\n\n" +
			"### Dependencies\n\n" +
			"blocks:\n"
		mdPath := setup(t, md)

		out := captureStderr(t, func() {
			if err := createIssuesFromMarkdown(nil, mdPath, false); err != nil {
				t.Fatalf("createIssuesFromMarkdown returned error (expected skip-and-warn): %v", err)
			}
		})

		if !strings.Contains(out, "skipped dependency") {
			t.Errorf("expected a 'skipped dependency' warning for the empty target, got: %q", out)
		}

		n, err := store.CountIssues(rootCtx, "", types.IssueFilter{})
		if err != nil {
			t.Fatalf("CountIssues failed: %v", err)
		}
		if n != 1 {
			t.Fatalf("expected the issue to be created despite the empty target, got %d issues", n)
		}
	})
}
