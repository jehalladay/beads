//go:build cgo

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-nqwl1: `bd doctor --check=pollution --clean` backs up the flagged issues
// to JSONL, then deletes them, telling the user "To restore, run: bd init
// --from-jsonl <backup>". But the backup was asymmetrically lossy vs a real
// export: runPollutionCheck fetched issues via SearchIssues with a zero-value
// filter (IncludeDependencies=false) and the search path never hydrates comments
// at all, so the marshaled structs carried EMPTY Dependencies and EMPTY
// Comments. deleteIssue then removes the issue AND its dependency/comment rows,
// so the documented restore silently came back with dependency edges and comment
// history gone — permanent loss for any wrongly-flagged issue.
//
// The fix hoists a hydrateAndBackupPollutedIssues helper that bulk-loads
// GetDependencyRecordsForIssues + GetCommentsForIssues onto the flagged structs
// before marshaling, mirroring the real export path (export.go).
//
// MUTATION-VERIFY: strip the hydration block from hydrateAndBackupPollutedIssues
// (call backupPollutedIssues directly) and this test FAILS — the backup JSONL
// carries an empty dependencies array and an empty comments array.
func TestPollutionBackupHydratesDepsAndComments_nqwl1(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	if err := real.SetConfig(ctx, "issue_prefix", "bd"); err != nil {
		t.Fatalf("set prefix: %v", err)
	}

	// A flagged (pollution-shaped) issue that nonetheless carries a real
	// dependency edge and a real comment — exactly the wrongly-flagged item
	// whose relational history the lossy backup would drop on --clean.
	victim := &types.Issue{ID: "bd-victim", Title: "test-throwaway", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	target := &types.Issue{ID: "bd-target", Title: "real work the victim blocks on", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	for _, iss := range []*types.Issue{victim, target} {
		if err := real.CreateIssue(ctx, iss, "test"); err != nil {
			t.Fatalf("create %s: %v", iss.ID, err)
		}
	}
	if err := real.AddDependency(ctx, &types.Dependency{IssueID: "bd-victim", DependsOnID: "bd-target", Type: types.DepBlocks}, "test"); err != nil {
		t.Fatalf("add dependency: %v", err)
	}
	if _, err := real.AddIssueComment(ctx, "bd-victim", "test", "important history worth preserving"); err != nil {
		t.Fatalf("add comment: %v", err)
	}

	// Re-fetch through the same zero-value-filter search path runPollutionCheck
	// uses, so the struct entering the backup starts with EMPTY deps+comments —
	// the exact state the bug marshaled.
	issues, err := real.SearchIssues(ctx, "", types.IssueFilter{})
	if err != nil {
		t.Fatalf("search issues: %v", err)
	}
	var victimIssue *types.Issue
	for _, is := range issues {
		if is.ID == "bd-victim" {
			victimIssue = is
		}
	}
	if victimIssue == nil {
		t.Fatal("victim issue not found in search results")
	}
	if len(victimIssue.Dependencies) != 0 || len(victimIssue.Comments) != 0 {
		t.Fatalf("precondition: search path should NOT pre-hydrate deps/comments, got deps=%d comments=%d",
			len(victimIssue.Dependencies), len(victimIssue.Comments))
	}

	polluted := []pollutionResult{{issue: victimIssue, score: 1.0}}
	backupPath := filepath.Join(tmpDir, "pollution-backup.jsonl")
	if err := hydrateAndBackupPollutedIssues(ctx, real, polluted, backupPath); err != nil {
		t.Fatalf("hydrateAndBackupPollutedIssues: %v", err)
	}

	data, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 JSONL line, got %d:\n%s", len(lines), data)
	}
	var restored types.Issue
	if err := json.Unmarshal([]byte(lines[0]), &restored); err != nil {
		t.Fatalf("backup line not valid JSON: %v", err)
	}

	// The backup must round-trip the dependency edge — else `bd init --from-jsonl`
	// restores the issue with the edge gone.
	if len(restored.Dependencies) == 0 {
		t.Errorf("REGRESSION (nqwl1): pollution backup dropped Dependencies — restore via `bd init --from-jsonl` would silently lose the dependency edge [beads-nqwl1]\nbackup: %s", lines[0])
	} else {
		d := restored.Dependencies[0]
		if d.DependsOnID != "bd-target" {
			t.Errorf("nqwl1: backed-up dependency points at %q, want bd-target", d.DependsOnID)
		}
	}

	// ...and the comment history.
	if len(restored.Comments) == 0 {
		t.Errorf("REGRESSION (nqwl1): pollution backup dropped Comments — restore would silently lose comment history [beads-nqwl1]\nbackup: %s", lines[0])
	} else if !strings.Contains(restored.Comments[0].Text, "important history worth preserving") {
		t.Errorf("nqwl1: backed-up comment body = %q, want the seeded text", restored.Comments[0].Text)
	}
}
