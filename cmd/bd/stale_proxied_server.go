package main

import (
	"context"

	"github.com/steveyegge/beads/internal/types"
)

// fetchStaleIssues returns stale issues for the given filter, routing to the
// proxied-server UOW stack when the global store is nil (proxiedServerMode) and
// to the direct store otherwise (beads-1xs1).
//
// `bd stale` is not a noDbCommand and had no ensureDirectMode guard, so in
// proxiedServerMode the bare store.GetStaleIssues calls nil-panicked on
// hub-connected crew. GetStaleIssues is now on the UOW IssueUseCase (widened
// issueops.GetStaleIssuesInTx from *sql.Tx to DBTX, mirroring the beads-mh3e
// diff / history interface-ext precedent), so the proxied path serves it too.
func fetchStaleIssues(ctx context.Context, filter types.StaleFilter) ([]*types.Issue, error) {
	if usesProxiedServer() {
		if uowProvider == nil {
			FatalError("proxied-server UOW provider not initialized")
		}
		uw, err := uowProvider.NewUOW(ctx)
		if err != nil {
			return nil, err
		}
		defer uw.Close(ctx)
		return uw.IssueUseCase().GetStaleIssues(ctx, filter)
	}
	return store.GetStaleIssues(ctx, filter)
}
