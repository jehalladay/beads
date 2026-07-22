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

// TestCreateIssuesFromMarkdown_DryRun verifies beads-9rb6: `bd create --file
// <f> --dry-run` previews the batch (titles/types/priorities/deps) and creates
// NOTHING — previously it was hard-rejected ("--dry-run is not supported with
// --file"), exactly the case where a preview is most useful. Mirrors the
// single-create and --graph --dry-run behavior.
func TestCreateIssuesFromMarkdown_DryRun(t *testing.T) {
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.Mkdir(beadsDir, 0755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(beadsDir, "dolt")

	oldStore, oldActor, oldCtx := store, actor, rootCtx
	s := newTestStoreIsolatedDB(t, dbPath, "drun")
	store = s
	actor = "drun"
	rootCtx = context.Background()
	t.Cleanup(func() { store, actor, rootCtx = oldStore, oldActor, oldCtx })

	md := "## First Task\n\nBody one.\n\n### Priority\n\n1\n\n" +
		"## Second Task\n\nBody two.\n\n### Priority\n\n3\n"
	mdPath := filepath.Join(tmpDir, "batch.md")
	if err := os.WriteFile(mdPath, []byte(md), 0644); err != nil {
		t.Fatal(err)
	}

	// Baseline: no issues before.
	before, err := s.SearchIssues(rootCtx, "", types.IssueFilter{})
	if err != nil {
		t.Fatalf("baseline SearchIssues: %v", err)
	}

	out := captureStdout(t, func() error {
		return createIssuesFromMarkdown(nil, mdPath, true, false)
	})

	// Must preview, not reject.
	if strings.Contains(out, "not supported") {
		t.Fatalf("dry-run was rejected instead of previewed (beads-9rb6 regression): %q", out)
	}
	if !strings.Contains(out, "DRY RUN") {
		t.Errorf("expected a DRY RUN banner, got: %q", out)
	}
	if !strings.Contains(out, "First Task") || !strings.Contains(out, "Second Task") {
		t.Errorf("preview must list both batch titles, got: %q", out)
	}

	// Must create NOTHING.
	after, err := s.SearchIssues(rootCtx, "", types.IssueFilter{})
	if err != nil {
		t.Fatalf("post SearchIssues: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("dry-run created %d issue(s); must create none", len(after)-len(before))
	}
}
