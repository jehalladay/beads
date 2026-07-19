package main

import (
	"context"

	"github.com/steveyegge/beads/internal/storage/uow"
	"github.com/steveyegge/beads/internal/types"
)

// countBackend abstracts the config-source + count queries for `bd count` so
// the shared filter-building in count.go works in both direct mode (global
// `store`) and proxiedServerMode (a UOW, where `store` is nil) — beads-2om1.
// The proxied path uses the CountIssues/CountIssuesByGroup methods added to the
// domain IssueUseCase (interface-extension leg) plus the already-proxied
// ConfigUseCase-backed filter-config loader (loadProxiedListFilterConfig).
type countBackend struct {
	uw uow.UnitOfWork // non-nil only in proxied mode
}

// newCountBackend opens a proxied read UOW when in proxiedServerMode, else
// returns a direct backend over the global store. Caller must defer close().
func newCountBackend(ctx context.Context) *countBackend {
	if usesProxiedServer() {
		return &countBackend{uw: proxiedOpenReadUOW(ctx)}
	}
	return &countBackend{}
}

func (b *countBackend) close() {
	if b.uw != nil {
		b.uw.Close(context.Background())
	}
}

func (b *countBackend) loadFilterConfig(ctx context.Context) (listFilterConfig, error) {
	if b.uw != nil {
		return loadProxiedListFilterConfig(ctx, b.uw)
	}
	return loadDirectListFilterConfig(ctx, store)
}

func (b *countBackend) countIssues(ctx context.Context, filter types.IssueFilter) (int64, error) {
	if b.uw != nil {
		return b.uw.IssueUseCase().CountIssues(ctx, "", filter)
	}
	return store.CountIssues(ctx, "", filter)
}

func (b *countBackend) countByGroup(ctx context.Context, filter types.IssueFilter, groupBy string) (map[string]int, error) {
	if b.uw != nil {
		return b.uw.IssueUseCase().CountIssuesByGroup(ctx, filter, groupBy)
	}
	return store.CountIssuesByGroup(ctx, filter, groupBy)
}
