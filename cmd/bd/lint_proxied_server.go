package main

import (
	"context"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/uow"
	"github.com/steveyegge/beads/internal/types"
)

// lintBackend abstracts the config-source + issue/dep reads for `bd lint` so the
// shared linting logic in lint.go works in both direct mode (global `store`) and
// proxiedServerMode (a UOW, where `store` is nil) — beads-hquo8. Before this,
// lint hard-failed "database not initialized" for hub (proxied-server) crew
// because it gated every read on the nil global store, despite being a
// documented CI gate (`bd lint $IDS || fail`). Read-divergence sibling of
// beads-6pjl6 (info), beads-ktlo (orphans), and the count/show precedents; all
// the reads lint needs (SearchIssues, GetIssue, config, parent-child dependents)
// already exist on the domain use cases, so this is a pure routing fix — no
// behavior change for direct crew.
type lintBackend struct {
	uw uow.UnitOfWork // non-nil only in proxied mode
}

// newLintBackend opens a proxied read UOW when in proxiedServerMode, else returns
// a direct backend over the global store. Caller must defer close().
func newLintBackend(ctx context.Context) *lintBackend {
	if usesProxiedServer() {
		return &lintBackend{uw: proxiedOpenReadUOW(ctx)}
	}
	return &lintBackend{}
}

func (b *lintBackend) close() {
	if b.uw != nil {
		b.uw.Close(context.Background())
	}
}

func (b *lintBackend) loadFilterConfig(ctx context.Context) (listFilterConfig, error) {
	if b.uw != nil {
		return loadProxiedListFilterConfig(ctx, b.uw)
	}
	return loadDirectListFilterConfig(ctx, store)
}

func (b *lintBackend) getIssue(ctx context.Context, id string) (*types.Issue, error) {
	if b.uw != nil {
		return b.uw.IssueUseCase().GetIssue(ctx, id)
	}
	return store.GetIssue(ctx, id)
}

func (b *lintBackend) searchIssues(ctx context.Context, filter types.IssueFilter) ([]*types.Issue, error) {
	if b.uw != nil {
		page, err := b.uw.IssueUseCase().SearchIssues(ctx, "", filter)
		if err != nil {
			return nil, err
		}
		return page.Items, nil
	}
	return store.SearchIssues(ctx, "", filter)
}

// openChildIDsOfEpic returns the IDs of an epic's open (non-closed) parent-child
// children — the backend-routed sibling of the package-level helper of the same
// name in lint.go. Best-effort (fail-open, no children on error), matching the
// direct helper and countEpicOpenChildren's posture. The proxied leg uses the
// same DepParentChild/DepDirectionIn listing as `bd show --children` (a child
// depends ON its parent, so children are the epic's dependents).
func (b *lintBackend) openChildIDsOfEpic(ctx context.Context, epicID string) []string {
	if b.uw != nil {
		deps, err := proxiedListDeps(ctx, b.uw, epicID, false, domain.DepListFilter{
			Types:     []types.DependencyType{types.DepParentChild},
			Direction: domain.DepDirectionIn,
		})
		if err != nil {
			return nil
		}
		// beads-97gmg: a done-category child is complete, matching the close
		// guard (countEpicOpenChildren) and the direct lint helper — otherwise
		// an epic legitimately closed with all-done-category children would be
		// falsely flagged as a closed-epic-with-open-child inconsistency.
		done := map[string]bool{}
		if custom, cerr := b.uw.ConfigUseCase().GetCustomStatuses(ctx); cerr == nil {
			for _, cs := range custom {
				if cs.Category == types.CategoryDone {
					done[cs.Name] = true
				}
			}
		}
		var open []string
		for _, dep := range deps {
			if dep.Issue.Status != types.StatusClosed && !done[string(dep.Issue.Status)] {
				open = append(open, dep.Issue.ID)
			}
		}
		return open
	}
	return openChildIDsOfEpic(ctx, store, epicID)
}
