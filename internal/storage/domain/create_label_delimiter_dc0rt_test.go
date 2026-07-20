package domain

import (
	"context"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestCreateRejectsDelimiterLabel pins beads-dc0rt: the domain create() path
// (used by proxied-server `bd create --label` and markdown/jsonl import through
// the use-case) must reject a comma/newline label BEFORE persisting it, matching
// the direct-mode PersistLabels guard (beads-f3y1) and the AddLabel guard
// (beads-pqzx). Previously create() looped params.Labels straight into
// labelRepo.Insert (raw INSERT, empty-check only), so a comma/newline label
// persisted and round-tripped through the markdown "### Labels" parser (splits
// on ','/'\n') as MULTIPLE labels — the same identity corruption on a third,
// uncovered create path.
func TestCreateRejectsDelimiterLabel(t *testing.T) {
	ctx := context.Background()
	cfg := func() *fakeConfigRepo {
		return &fakeConfigRepo{
			config:      map[string]string{"issue_prefix": "bd-", "issue_id_mode": "hash"},
			adaptiveCfg: DefaultAdaptiveConfig(),
		}
	}

	for _, tc := range []struct {
		name  string
		label string
	}{
		{"comma", "frontend,backend"},
		{"newline", "frontend\nbackend"},
		{"carriage-return", "frontend\rbackend"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lbl := &fakeLabelRepoIUC{}
			uc := newTestIssueUC(&fakeIssueRepo{}, nil, lbl, nil, cfg())
			_, err := uc.CreateIssue(ctx, CreateIssueParams{
				Issue:      &types.Issue{Title: "t", IssueType: types.TypeTask},
				ExplicitID: "bd-1",
				Labels:     []string{tc.label},
			}, "actor")
			if err == nil {
				t.Fatalf("expected create to reject delimiter label %q, got nil error", tc.label)
			}
			if !strings.Contains(err.Error(), "comma or newline") {
				t.Errorf("want delimiter-rejection error, got %q", err.Error())
			}
			if len(lbl.inserted) != 0 {
				t.Errorf("delimiter label must be rejected BEFORE Insert; got inserted=%v", lbl.inserted)
			}
		})
	}

	t.Run("clean label still persists", func(t *testing.T) {
		lbl := &fakeLabelRepoIUC{}
		uc := newTestIssueUC(&fakeIssueRepo{}, nil, lbl, nil, cfg())
		_, err := uc.CreateIssue(ctx, CreateIssueParams{
			Issue:      &types.Issue{Title: "t", IssueType: types.TypeTask},
			ExplicitID: "bd-2",
			Labels:     []string{"frontend", "back end"}, // spaces stay legal (beads-ehw7)
		}, "actor")
		if err != nil {
			t.Fatalf("clean labels must persist: %v", err)
		}
		if len(lbl.inserted) != 2 {
			t.Errorf("want 2 clean labels inserted, got %v", lbl.inserted)
		}
	})
}
