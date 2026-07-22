package issueops

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestValidateRef(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ref     string
		wantErr bool
	}{
		{name: "simple branch", ref: "main", wantErr: false},
		{name: "commit-like hash", ref: "a1b2c3d4", wantErr: false},
		{name: "path-style ref with allowed punctuation", ref: "refs/heads/feature-1.2_3", wantErr: false},
		{name: "empty rejected", ref: "", wantErr: true},
		{name: "too long (>128) rejected", ref: strings.Repeat("a", 129), wantErr: true},
		{name: "at max length (128) allowed", ref: strings.Repeat("a", 128), wantErr: false},
		{name: "space rejected", ref: "a b", wantErr: true},
		{name: "semicolon rejected (injection guard)", ref: "main;DROP", wantErr: true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateRef(tt.ref)
			if tt.wantErr && err == nil {
				t.Fatalf("ValidateRef(%q) = nil, want error", tt.ref)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ValidateRef(%q) = %v, want nil", tt.ref, err)
			}
		})
	}
}

func TestCreateBlockedRecomputeIDs(t *testing.T) {
	t.Parallel()

	t.Run("splits issue and wisp buckets and dedups", func(t *testing.T) {
		t.Parallel()
		issues := []*types.Issue{
			{ID: "bd-1"},                  // regular
			{ID: "bd-1"},                  // duplicate regular
			{ID: "bd-w", Ephemeral: true}, // wisp
			nil,                           // skipped
		}
		issueIDs, wispIDs := createBlockedRecomputeIDs(issues)
		if len(issueIDs) != 1 || issueIDs[0] != "bd-1" {
			t.Fatalf("issueIDs = %v, want [bd-1]", issueIDs)
		}
		if len(wispIDs) != 1 || wispIDs[0] != "bd-w" {
			t.Fatalf("wispIDs = %v, want [bd-w]", wispIDs)
		}
	})

	t.Run("dependency sources are included, empty dep.IssueID falls back to issue.ID", func(t *testing.T) {
		t.Parallel()
		issues := []*types.Issue{
			{
				ID: "bd-parent",
				Dependencies: []*types.Dependency{
					{IssueID: "bd-dep"}, // explicit source
					{IssueID: ""},       // falls back to bd-parent (already added)
					nil,                 // skipped
				},
			},
		}
		issueIDs, wispIDs := createBlockedRecomputeIDs(issues)
		if len(wispIDs) != 0 {
			t.Fatalf("wispIDs = %v, want empty", wispIDs)
		}
		// Expect bd-parent (from issue.ID) and bd-dep (from the explicit dep),
		// with the empty-source dep deduped against bd-parent.
		got := map[string]bool{}
		for _, id := range issueIDs {
			got[id] = true
		}
		if !got["bd-parent"] || !got["bd-dep"] || len(issueIDs) != 2 {
			t.Fatalf("issueIDs = %v, want exactly [bd-parent bd-dep]", issueIDs)
		}
	})

	t.Run("empty and nil-only inputs yield empty buckets", func(t *testing.T) {
		t.Parallel()
		issueIDs, wispIDs := createBlockedRecomputeIDs([]*types.Issue{nil, {ID: ""}})
		if len(issueIDs) != 0 || len(wispIDs) != 0 {
			t.Fatalf("expected empty buckets, got issueIDs=%v wispIDs=%v", issueIDs, wispIDs)
		}
	})
}

func TestIssueUpsertAssignments(t *testing.T) {
	t.Parallel()

	t.Run("plain form uses VALUES() without stale guard", func(t *testing.T) {
		t.Parallel()
		got := issueUpsertAssignments(false)
		if got == "" {
			t.Fatal("expected non-empty assignment fragment")
		}
		if !strings.Contains(got, "= VALUES(") {
			t.Errorf("plain form missing VALUES() assignment: %q", got)
		}
		if strings.Contains(got, "IF(VALUES(updated_at)") {
			t.Errorf("plain form must not contain the stale-reject IF guard: %q", got)
		}
	})

	t.Run("stale-reject form guards on updated_at", func(t *testing.T) {
		t.Parallel()
		got := issueUpsertAssignments(true)
		if !strings.Contains(got, "IF(VALUES(updated_at) > updated_at") {
			t.Errorf("stale-reject form missing updated_at guard: %q", got)
		}
	})

	t.Run("both forms cover every upsert column", func(t *testing.T) {
		t.Parallel()
		plain := issueUpsertAssignments(false)
		guarded := issueUpsertAssignments(true)
		for _, col := range issueUpsertColumns {
			if !strings.Contains(plain, col) {
				t.Errorf("plain form missing column %q", col)
			}
			if !strings.Contains(guarded, col) {
				t.Errorf("guarded form missing column %q", col)
			}
		}
	})
}

// insertIssueColumns is the column list written by the fresh INSERT in
// insertIssueIntoTable (helpers.go). It is duplicated here on purpose: the
// contract test below asserts every INSERT column is either rewritten on
// re-import (issueUpsertColumns) or explicitly blessed as re-import-immutable
// (upsertImmutableColumns). If a new column is added to the INSERT but to
// NEITHER set, this test fails — forcing an explicit round-trip decision so a
// user-mutable field can never again be silently dropped on
// export→edit→re-import (beads-kalv / beads-lbez).
var insertIssueColumns = []string{
	"id", "content_hash", "title", "description", "design", "acceptance_criteria", "notes",
	"status", "priority", "issue_type", "assignee", "estimated_minutes",
	"created_at", "created_by", "owner", "updated_at", "started_at", "closed_at", "external_ref", "spec_id",
	"compaction_level", "compacted_at", "compacted_at_commit", "original_size",
	"sender", "ephemeral", "no_history", "wisp_type", "pinned", "is_template",
	"mol_type", "work_type", "source_system", "source_repo", "close_reason", "closed_by_session",
	"event_kind", "actor", "target", "payload",
	"await_type", "await_id", "timeout_ns", "waiters", "bonded_from",
	"due_at", "defer_until", "metadata",
}

// upsertImmutableColumns are INSERT columns deliberately NOT rewritten on
// re-import, each with a ground-truth reason (see the block comment on
// issueUpsertColumns in helpers.go). They are the complement of
// issueUpsertColumns within insertIssueColumns.
var upsertImmutableColumns = map[string]string{
	"id":                  "identity / primary key",
	"created_at":          "immutable creation timestamp",
	"created_by":          "immutable creation actor",
	"ephemeral":           "governs issues-vs-wisps table routing, not an in-place column update",
	"no_history":          "governs issues-vs-wisps table routing, not an in-place column update",
	"is_template":         "governs template routing, not an in-place column update",
	"compaction_level":    "compaction-manager owned, not user round-trip data",
	"compacted_at":        "compaction-manager owned, not user round-trip data",
	"compacted_at_commit": "compaction-manager owned, not user round-trip data",
	"original_size":       "compaction-manager owned, not user round-trip data",
}

// TestInsertColumnsAreUpsertedOrBlessed is the round-trip fidelity contract:
// every column the fresh INSERT writes must be either re-imported (in
// issueUpsertColumns) or explicitly blessed immutable — never silently absent.
func TestInsertColumnsAreUpsertedOrBlessed(t *testing.T) {
	t.Parallel()

	upsertSet := make(map[string]bool, len(issueUpsertColumns))
	for _, col := range issueUpsertColumns {
		upsertSet[col] = true
	}

	// (1) Every INSERT column is accounted for: upserted or blessed-immutable,
	//     never both, never neither.
	for _, col := range insertIssueColumns {
		_, blessed := upsertImmutableColumns[col]
		upserted := upsertSet[col]
		switch {
		case upserted && blessed:
			t.Errorf("column %q is BOTH upserted and blessed-immutable — pick one", col)
		case !upserted && !blessed:
			t.Errorf("INSERT column %q is neither re-imported (issueUpsertColumns) nor blessed "+
				"immutable (upsertImmutableColumns): a bd export→edit→re-import would silently "+
				"drop edits to it — add it to one set with a rationale (beads-lbez)", col)
		}
	}

	// (2) The upsert set must not reference a column the INSERT never writes
	//     (a rewrite of a non-existent column is a bug / typo).
	insertSet := make(map[string]bool, len(insertIssueColumns))
	for _, col := range insertIssueColumns {
		insertSet[col] = true
	}
	for _, col := range issueUpsertColumns {
		if !insertSet[col] {
			t.Errorf("issueUpsertColumns rewrites %q which the INSERT never writes", col)
		}
	}

	// (3) The blessed-immutable set must be a subset of INSERT columns too.
	for col := range upsertImmutableColumns {
		if !insertSet[col] {
			t.Errorf("upsertImmutableColumns lists %q which the INSERT never writes", col)
		}
	}

	// (4) Regression teeth for the specific fields beads-kalv + beads-lbez
	//     restored: they MUST be re-imported.
	for _, col := range []string{
		"owner", "pinned", "mol_type", "work_type", // kalv
		"spec_id", "due_at", "defer_until", "await_type", "await_id", // lbez tier-A
		"timeout_ns", "waiters", "wisp_type", "sender", "source_system",
		"event_kind", "actor", "target", "payload",
		"closed_by_session", // xapi2: ni2ph made it JSON-exported (READ) — the write set had to follow
	} {
		if !upsertSet[col] {
			t.Errorf("mutable field %q dropped from issueUpsertColumns — re-import silently loses edits", col)
		}
	}
}

func TestExpandAndAbsPath(t *testing.T) {
	t.Parallel()

	t.Run("relative path becomes absolute", func(t *testing.T) {
		t.Parallel()
		got := expandAndAbsPath("some/rel/path")
		if !filepath.IsAbs(got) {
			t.Fatalf("expandAndAbsPath(rel) = %q, want absolute", got)
		}
		if !strings.HasSuffix(got, filepath.Join("some", "rel", "path")) {
			t.Fatalf("expandAndAbsPath(rel) = %q, want suffix some/rel/path", got)
		}
	})

	t.Run("already-absolute path is preserved", func(t *testing.T) {
		t.Parallel()
		abs := filepath.Join(t.TempDir(), "x")
		got := expandAndAbsPath(abs)
		if got != abs {
			t.Fatalf("expandAndAbsPath(%q) = %q, want unchanged", abs, got)
		}
	})

	t.Run("bare tilde expands to an absolute home path", func(t *testing.T) {
		t.Parallel()
		got := expandAndAbsPath("~")
		if !filepath.IsAbs(got) {
			t.Fatalf("expandAndAbsPath(~) = %q, want absolute", got)
		}
		if strings.HasPrefix(got, "~") {
			t.Fatalf("expandAndAbsPath(~) = %q, tilde not expanded", got)
		}
	})

	t.Run("tilde-prefixed path expands under home", func(t *testing.T) {
		t.Parallel()
		got := expandAndAbsPath("~/sub/dir")
		if strings.HasPrefix(got, "~") {
			t.Fatalf("expandAndAbsPath(~/sub/dir) = %q, tilde not expanded", got)
		}
		if !strings.HasSuffix(got, filepath.Join("sub", "dir")) {
			t.Fatalf("expandAndAbsPath(~/sub/dir) = %q, want suffix sub/dir", got)
		}
	})
}
