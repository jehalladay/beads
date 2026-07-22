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

// TestLabelPatternLiteralMetacharsE2E is the teeth for beads-k3xye: globToLike
// backslash-escapes a literal '_'/'%'/'\' in a --label-pattern, but that only
// matches literally if the LIKE clause carries ESCAPE '\'. go-mysql-server has
// NO default LIKE escape char, so without the ESCAPE clause the '_' still acts
// as a single-char wildcard (and '\%'/'\_' match a literal backslash + char).
// This drives the real embedded store on BOTH the bd-list (SearchIssues) and
// bd-ready (GetReadyWork) paths — the two SQL-emitting globToLike consumers.
//
// MUTATION-VERIFY: drop ESCAPE '\\' from either label clause (labels.go:87 /
// ready.go:321) and "literal underscore does not wildcard-match" FAILS — with
// no escape char the pattern 'a_c' matches both "a_c" and "abc".
func TestLabelPatternLiteralMetacharsE2E(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	mk := func(id, label string) {
		iss := &types.Issue{ID: id, Title: id, Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := store.CreateIssue(ctx, iss, "tester"); err != nil {
			t.Fatalf("create %q: %v", id, err)
		}
		if err := store.AddLabel(ctx, iss.ID, label, "tester"); err != nil {
			t.Fatalf("label %q: %v", id, err)
		}
	}
	// A label with a literal underscore, and a same-shape decoy where the
	// underscore position holds a different char. A literal-'_' match must hit
	// only the first; a wildcard-'_' match would leak the decoy.
	mk("k3-under", "foo_bar")
	mk("k3-decoy", "fooXbar")
	// A label with a literal percent, and a decoy without it. A literal-'%'
	// match must hit only the first; a wildcard-'%' would match everything.
	mk("k3-pct", "rate%off")
	mk("k3-plain", "rateoff")

	t.Run("list: literal underscore does not wildcard-match", func(t *testing.T) {
		got, err := store.SearchIssues(ctx, "", types.IssueFilter{LabelPattern: "foo_bar"})
		if err != nil {
			t.Fatalf("SearchIssues(LabelPattern foo_bar): %v", err)
		}
		if len(got) != 1 || got[0].ID != "k3-under" {
			ids := make([]string, len(got))
			for i, g := range got {
				ids[i] = g.ID
			}
			t.Errorf("literal '_' pattern 'foo_bar' matched %v, want exactly [k3-under] (a '_' treated as a wildcard leaks k3-decoy)", ids)
		}
	})

	t.Run("list: literal percent does not wildcard-match", func(t *testing.T) {
		got, err := store.SearchIssues(ctx, "", types.IssueFilter{LabelPattern: "rate%off"})
		if err != nil {
			t.Fatalf("SearchIssues(LabelPattern rate%%off): %v", err)
		}
		if len(got) != 1 || got[0].ID != "k3-pct" {
			ids := make([]string, len(got))
			for i, g := range got {
				ids[i] = g.ID
			}
			t.Errorf("literal '%%' pattern 'rate%%off' matched %v, want exactly [k3-pct] (a '%%' treated as a wildcard leaks k3-plain)", ids)
		}
	})

	t.Run("list: glob '*' still wildcards after the escape fix", func(t *testing.T) {
		got, err := store.SearchIssues(ctx, "", types.IssueFilter{LabelPattern: "foo*bar"})
		if err != nil {
			t.Fatalf("SearchIssues(LabelPattern foo*bar): %v", err)
		}
		// 'foo*bar' -> 'foo%bar' must match both foo_bar and fooXbar.
		if len(got) != 2 {
			t.Errorf("glob 'foo*bar' matched %d, want 2 (foo_bar + fooXbar); escape fix must not break real globbing", len(got))
		}
	})

	t.Run("ready: literal underscore does not wildcard-match", func(t *testing.T) {
		got, err := store.GetReadyWork(ctx, types.WorkFilter{LabelPattern: "foo_bar"})
		if err != nil {
			t.Fatalf("GetReadyWork(LabelPattern foo_bar): %v", err)
		}
		if len(got) != 1 || got[0].ID != "k3-under" {
			ids := make([]string, len(got))
			for i, g := range got {
				ids[i] = g.ID
			}
			t.Errorf("ready literal '_' pattern 'foo_bar' matched %v, want exactly [k3-under] (a '_' wildcard leaks k3-decoy)", ids)
		}
	})
}
