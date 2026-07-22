//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/internal/types"
)

// TestEmbeddedGraphCreateSourceRepoInherit is the beads-comt0 teeth: single
// `bd create --deps discovered-from:<parent>` inherits the parent's source_repo
// onto the child (create.go / domain u.create DiscoveredFromParent block), but
// `bd create --graph` mints every node TOP-LEVEL (Pass 1, empty source_repo)
// then links edges in a post-pass — so the single-create inheritance never ran
// for a graph child linked to its source parent by a discovered-from EDGE, and
// the child kept an empty source_repo (metadata-provenance gap). Same
// top-level-then-link create-input-parity seam as the l8qsn label inheritance
// and t39ph closed-parent guard.
//
// The fix adds a source_repo-from-discovered-from post-pass to BOTH graph link
// paths (embedded executeGraphApply + domain applyGraph). This test drives the
// embedded path end-to-end via the real `bd create --graph` subprocess and
// verifies the child's source_repo via raw SQL. Without the post-pass the
// assertion fails (child source_repo empty). Mirrors the single-create
// reference TestEmbeddedCreate/discovered_from_inherits_source_repo.
func TestEmbeddedGraphCreateSourceRepoInherit(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	writePlan := func(t *testing.T, dir, name, body string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write plan %s: %v", name, err)
		}
		return p
	}

	// childSourceRepo reads the stored source_repo for a bead id via raw SQL
	// against the embedded dolt db (the JSON output may omit source_repo).
	childSourceRepo := func(t *testing.T, beadsDir, database, id string) string {
		t.Helper()
		dataDir := filepath.Join(beadsDir, "embeddeddolt")
		db, cleanup, err := embeddeddolt.OpenSQL(t.Context(), dataDir, database, "main")
		if err != nil {
			t.Fatalf("OpenSQL: %v", err)
		}
		defer cleanup()
		var sourceRepo string
		if err := db.QueryRowContext(t.Context(),
			"SELECT COALESCE(source_repo, '') FROM issues WHERE id = ?", id).Scan(&sourceRepo); err != nil {
			t.Fatalf("query source_repo for %s: %v", id, err)
		}
		return sourceRepo
	}

	// (1) A graph child linked to a source parent by a top-level discovered-from
	//     EDGE inherits the parent's source_repo — the gap comt0 fixes.
	t.Run("graph_child_inherits_source_repo_via_discovered_from_edge", func(t *testing.T) {
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "sri")

		// Create the source parent with source_repo set via the store API
		// (mirrors the single-create reference test setup).
		store := openStore(t, beadsDir, "sri")
		parent := &types.Issue{
			Title:      "Parent with source repo",
			Priority:   1,
			Status:     types.StatusOpen,
			IssueType:  types.TypeTask,
			SourceRepo: "/path/to/repo",
		}
		if err := store.CreateIssue(t.Context(), parent, "test"); err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if err := store.Commit(t.Context(), "create parent"); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		store.Close()

		plan := `{"nodes":[{"key":"c","title":"graph discovered child","type":"bug"}],` +
			`"edges":[{"from_key":"c","to_id":"` + parent.ID + `","type":"discovered-from"}]}`
		res := bdCreateGraph(t, bd, dir, writePlan(t, dir, "sri.json", plan))
		childID := res.IDs["c"]
		if childID == "" {
			t.Fatalf("graph result missing child ID: %v", res.IDs)
		}

		if got := childSourceRepo(t, beadsDir, "sri", childID); got != "/path/to/repo" {
			t.Errorf("child source_repo: got %q, want %q (discovered-from parent inheritance on graph path)", got, "/path/to/repo")
		}
	})

	// (2) Negative control: no discovered-from edge → child keeps its own empty
	//     source_repo (the post-pass must not fabricate provenance).
	t.Run("graph_child_without_discovered_from_keeps_empty_source_repo", func(t *testing.T) {
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "srn")

		store := openStore(t, beadsDir, "srn")
		parent := &types.Issue{
			Title:      "Unrelated parent with source repo",
			Priority:   1,
			Status:     types.StatusOpen,
			IssueType:  types.TypeTask,
			SourceRepo: "/path/to/repo",
		}
		if err := store.CreateIssue(t.Context(), parent, "test"); err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if err := store.Commit(t.Context(), "create parent"); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		store.Close()

		// A blocks edge (not discovered-from) must NOT trigger source_repo inherit.
		plan := `{"nodes":[{"key":"c","title":"graph blocks child","type":"bug"}],` +
			`"edges":[{"from_key":"c","to_id":"` + parent.ID + `","type":"blocks"}]}`
		res := bdCreateGraph(t, bd, dir, writePlan(t, dir, "srn.json", plan))
		childID := res.IDs["c"]
		if childID == "" {
			t.Fatalf("graph result missing child ID: %v", res.IDs)
		}

		if got := childSourceRepo(t, beadsDir, "srn", childID); got != "" {
			t.Errorf("child source_repo: got %q, want empty (no discovered-from edge, no inheritance)", got)
		}
	})
}
