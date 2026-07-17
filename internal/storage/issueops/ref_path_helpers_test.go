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
