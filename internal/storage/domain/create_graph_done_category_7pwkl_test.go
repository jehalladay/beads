package domain

import (
	"context"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestDomainClosedParentGuard_DoneCategory_7pwkl pins the beads-7pwkl fix: the
// three domain-layer closed-parent-with-open-child guards in issue.go —
// create() (proxied --deps parent-child / markdown seam) and applyGraph()
// (edge-loop + node.ParentID Pass 4) — must treat a parent in a custom
// done-category status (CategoryDone) as terminal, exactly like the cmd-side
// create seams (ei6vq) and the embedded graph_apply guard (99lgm). Before the
// fix these keyed on the literal parent.Status == types.StatusClosed, so a
// done-category parent bypassed the guard and a freshly minted OPEN child
// silently recreated the forbidden terminal-parent-with-open-child state.
//
// The done-set is resolved via u.doneCategoryStatusNames (cfgRepo custom
// statuses). Empty done-set is degraded-safe: byte-identical to literal-closed.
//
// Mutation check: revert any of the three sites to `== types.StatusClosed` and
// the matching subtest goes GREEN->RED (the done-category child is admitted).
func TestDomainClosedParentGuard_DoneCategory_7pwkl(t *testing.T) {
	ctx := context.Background()

	// "verified" is a custom status in the DONE category — terminal, not literally closed.
	doneCfg := func() *fakeConfigRepo {
		return &fakeConfigRepo{
			config:      map[string]string{"issue_prefix": "bd-", "issue_id_mode": "hash"},
			adaptiveCfg: DefaultAdaptiveConfig(),
			statuses:    []types.CustomStatus{{Name: "verified", Category: types.CategoryDone}},
		}
	}

	doneParent := func() *types.Issue {
		return &types.Issue{ID: "bd-parent", IssueType: types.TypeEpic, Status: types.Status("verified")}
	}

	t.Run("create --deps parent-child rejects open child under done-category parent", func(t *testing.T) {
		r := &fakeIssueRepo{getIssues: map[string]*types.Issue{"bd-parent": doneParent()}}
		uc := newTestIssueUC(r, &fakeDepRepoIUC{}, nil, nil, doneCfg())
		_, err := uc.CreateIssue(ctx, CreateIssueParams{
			Issue: &types.Issue{Title: "c", IssueType: types.TypeTask},
			Dependencies: []DependencySpec{
				{TargetID: "bd-parent", Type: types.DepParentChild},
			},
		}, "actor")
		if err == nil || !strings.Contains(err.Error(), "closed parent bd-parent") {
			t.Fatalf("want closed-parent rejection for done-category parent, got %v", err)
		}
	})

	t.Run("create --deps parent-child --force overrides done-category parent", func(t *testing.T) {
		r := &fakeIssueRepo{getIssues: map[string]*types.Issue{"bd-parent": doneParent()}}
		uc := newTestIssueUC(r, &fakeDepRepoIUC{}, nil, nil, doneCfg())
		if _, err := uc.CreateIssue(ctx, CreateIssueParams{
			Issue: &types.Issue{Title: "c", IssueType: types.TypeTask},
			Force: true,
			Dependencies: []DependencySpec{
				{TargetID: "bd-parent", Type: types.DepParentChild},
			},
		}, "actor"); err != nil {
			t.Fatalf("--force must override done-category guard, got %v", err)
		}
	})

	t.Run("applyGraph edge parent-child rejects open child under existing done-category parent", func(t *testing.T) {
		r := &fakeIssueRepo{
			existsResults: map[string]bool{"bd-parent": true},
			getIssues:     map[string]*types.Issue{"bd-parent": doneParent()},
		}
		uc := newTestIssueUC(r, &fakeDepRepoIUC{}, nil, nil, doneCfg())
		// child node is minted OPEN; edge links it under the existing done-category parent.
		plan := GraphPlan{
			Nodes: []GraphNode{
				{Key: "child", Issue: &types.Issue{Title: "c", IssueType: types.TypeTask}},
			},
			Edges: []GraphEdge{{FromKey: "child", ToID: "bd-parent", Type: types.DepParentChild}},
		}
		if _, err := uc.ApplyIssueGraph(ctx, plan, "actor"); err == nil || !strings.Contains(err.Error(), "closed parent bd-parent") {
			t.Fatalf("want edge-loop closed-parent rejection for done-category parent, got %v", err)
		}
	})

	t.Run("applyGraph node.ParentID rejects open child under existing done-category parent", func(t *testing.T) {
		r := &fakeIssueRepo{
			existsResults: map[string]bool{"bd-parent": true},
			getIssues:     map[string]*types.Issue{"bd-parent": doneParent()},
		}
		uc := newTestIssueUC(r, &fakeDepRepoIUC{}, nil, nil, doneCfg())
		plan := GraphPlan{
			Nodes: []GraphNode{
				{Key: "child", Issue: &types.Issue{Title: "c", IssueType: types.TypeTask}, ParentID: "bd-parent"},
			},
		}
		if _, err := uc.ApplyIssueGraph(ctx, plan, "actor"); err == nil || !strings.Contains(err.Error(), "closed parent bd-parent") {
			t.Fatalf("want Pass-4 closed-parent rejection for done-category parent, got %v", err)
		}
	})

	// Negative control: an empty done-set (no custom statuses) must leave a plain
	// OPEN parent unaffected — degraded-safe, byte-identical to literal-closed.
	t.Run("open parent with empty done-set is admitted (control)", func(t *testing.T) {
		openParent := &types.Issue{ID: "bd-parent", IssueType: types.TypeEpic, Status: types.StatusOpen}
		r := &fakeIssueRepo{
			existsResults: map[string]bool{"bd-parent": true},
			getIssues:     map[string]*types.Issue{"bd-parent": openParent},
		}
		cfg := &fakeConfigRepo{
			config:      map[string]string{"issue_prefix": "bd-", "issue_id_mode": "hash"},
			adaptiveCfg: DefaultAdaptiveConfig(),
		}
		uc := newTestIssueUC(r, &fakeDepRepoIUC{}, nil, nil, cfg)
		plan := GraphPlan{
			Nodes: []GraphNode{
				{Key: "child", Issue: &types.Issue{Title: "c", IssueType: types.TypeTask}, ParentID: "bd-parent"},
			},
		}
		if _, err := uc.ApplyIssueGraph(ctx, plan, "actor"); err != nil {
			t.Fatalf("open parent must be admitted, got %v", err)
		}
	})
}
