package issueops

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestPrepareIssueForInsert_TrimsTitle_cm94s guards the shared create+import
// write seam against storing a padded title (beads-cm94s). `bd create` trims
// the title at the cmd RunE (cmd/bd/create.go), but create paths that bypass
// the cmd layer — import (importIssuesCore → CreateIssuesInTxWithResult) and
// the domain/proxied create — reach PrepareIssueForInsert instead, which never
// trimmed. A padded title is unsearchable by exact match and breaks the
// markdown "### Title" round-trip. Same shared-write-path normalizer-parity
// class as dc0rt (label) / u4rks (metadata) / 82pv3 (timestamps).
func TestPrepareIssueForInsert_TrimsTitle_cm94s(t *testing.T) {
	issue := &types.Issue{
		ID:        "bd-1",
		Title:     "   padded title   ",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
	}
	if err := PrepareIssueForInsert(issue, nil, nil); err != nil {
		t.Fatalf("PrepareIssueForInsert should accept a trimmable title, got: %v", err)
	}
	if issue.Title != "padded title" {
		t.Errorf("title should be trimmed to %q, got %q", "padded title", issue.Title)
	}
}

// TestPrepareIssueForInsert_TrimEmptyTitleRejects_cm94s confirms an
// all-whitespace title trims to empty and is then rejected by the len==0
// guard in ValidateWithCustom — matching `bd create`, which trims then guards
// empty (create.go). The trim must not let a whitespace-only title through.
func TestPrepareIssueForInsert_TrimEmptyTitleRejects_cm94s(t *testing.T) {
	issue := &types.Issue{
		ID:        "bd-2",
		Title:     "     ",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
	}
	if err := PrepareIssueForInsert(issue, nil, nil); err == nil {
		t.Fatal("expected PrepareIssueForInsert to reject a whitespace-only title")
	}
}
