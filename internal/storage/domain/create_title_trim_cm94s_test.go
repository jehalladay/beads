package domain

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestCreateTrimsTitle_cm94s pins beads-cm94s on the domain create() path (used
// by the proxied-server `bd create`). `bd create` trims the title at the cmd
// RunE (cmd/bd/create.go), but the domain path does its own inline validation
// instead of PrepareIssueForInsert, so it must mirror the seam trim (as it
// mirrors label/metadata validation for dc0rt/u4rks). A padded title stored
// verbatim is unsearchable by exact match and breaks the markdown title
// round-trip. The trim runs before mint, so a padded title hashes to the same
// ID as its trimmed form.
func TestCreateTrimsTitle_cm94s(t *testing.T) {
	ctx := context.Background()
	cfg := func() *fakeConfigRepo {
		return &fakeConfigRepo{
			config:      map[string]string{"issue_prefix": "bd-", "issue_id_mode": "hash"},
			adaptiveCfg: DefaultAdaptiveConfig(),
		}
	}

	t.Run("trims a padded title before insert", func(t *testing.T) {
		r := &fakeIssueRepo{}
		uc := newTestIssueUC(r, nil, &fakeLabelRepoIUC{}, nil, cfg())
		res, err := uc.CreateIssue(ctx, CreateIssueParams{
			Issue: &types.Issue{
				Title:     "   padded title   ",
				IssueType: types.TypeTask,
			},
			ExplicitID: "bd-1",
		}, "actor")
		if err != nil {
			t.Fatalf("create should accept a trimmable title, got: %v", err)
		}
		if len(r.inserted) != 1 {
			t.Fatalf("expected 1 inserted issue, got %d", len(r.inserted))
		}
		if got := r.inserted[0].Title; got != "padded title" {
			t.Errorf("inserted title should be trimmed to %q, got %q", "padded title", got)
		}
		if got := res.Issue.Title; got != "padded title" {
			t.Errorf("result title should be trimmed to %q, got %q", "padded title", got)
		}
	})

	t.Run("rejects a whitespace-only title after trim", func(t *testing.T) {
		r := &fakeIssueRepo{}
		uc := newTestIssueUC(r, nil, &fakeLabelRepoIUC{}, nil, cfg())
		_, err := uc.CreateIssue(ctx, CreateIssueParams{
			Issue: &types.Issue{
				Title:     "     ",
				IssueType: types.TypeTask,
			},
			ExplicitID: "bd-2",
		}, "actor")
		if err == nil {
			t.Fatal("expected create to reject a whitespace-only title")
		}
		if len(r.inserted) != 0 {
			t.Error("whitespace-only title must be rejected BEFORE insert")
		}
	})
}
