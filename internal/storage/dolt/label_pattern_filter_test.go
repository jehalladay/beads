package dolt

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestLabelPatternAndRegexFilterE2E is the end-to-end teeth for beads-v5i7:
// --label-pattern (glob) and --label-regex flow into IssueFilter but were
// consumed by no query code, so SearchIssues returned ALL issues unfiltered.
// This drives a real embedded store to prove (a) the filters actually narrow
// the result set and (b) SQL REGEXP is supported by the engine (it is used
// nowhere else in the codebase, so this guards against an unsupported-op error).
func TestLabelPatternAndRegexFilterE2E(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	mk := func(title, label string) {
		iss := &types.Issue{Title: title, Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := store.CreateIssue(ctx, iss, "tester"); err != nil {
			t.Fatalf("create %q: %v", title, err)
		}
		if err := store.AddLabel(ctx, iss.ID, label, "tester"); err != nil {
			t.Fatalf("label %q: %v", label, err)
		}
	}
	mk("A", "tech-debt")
	mk("B", "tech-legacy")
	mk("C", "frontend")

	t.Run("label-pattern glob narrows to the tech-* set", func(t *testing.T) {
		got, err := store.SearchIssues(ctx, "", types.IssueFilter{LabelPattern: "tech-*"})
		if err != nil {
			t.Fatalf("SearchIssues(LabelPattern): %v", err)
		}
		if len(got) != 2 {
			t.Errorf("label-pattern 'tech-*' matched %d issues, want 2 (was the filter silently ignored?)", len(got))
		}
	})

	t.Run("label-regex narrows via REGEXP (engine must support it)", func(t *testing.T) {
		got, err := store.SearchIssues(ctx, "", types.IssueFilter{LabelRegex: "tech-(debt|legacy)"})
		if err != nil {
			t.Fatalf("SearchIssues(LabelRegex) errored — REGEXP may be unsupported: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("label-regex matched %d issues, want 2", len(got))
		}
	})

	t.Run("label-pattern excludes non-matching labels", func(t *testing.T) {
		got, err := store.SearchIssues(ctx, "", types.IssueFilter{LabelPattern: "frontend"})
		if err != nil {
			t.Fatalf("SearchIssues: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("label-pattern 'frontend' matched %d, want 1", len(got))
		}
	})
}
