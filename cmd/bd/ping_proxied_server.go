package main

import (
	"context"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// runPingProxiedServer is the proxied-server leg of `bd ping` (beads-jegd5).
// The direct RunE calls getStore() unconditionally, which is nil in
// proxiedServerMode — so ping false-failed "store not initialized" for the
// whole hub-connected fleet. ping is a read-only connectivity check, so it opens
// a proxied read UOW and runs the same trivial SearchIssues{Limit:1} probe as
// the direct path, preserving ping's exact timing + --json output contract
// (mirrors count_proxied_server.go / show_proxied_server.go). The uowProvider-nil
// and open-UOW error paths flow through pingFail so the connectivity failure is
// reported via ping's own contract (exit 1 / --json error object), not a
// FatalError.
func runPingProxiedServer(start time.Time, resolveMs int64) error {
	if uowProvider == nil {
		return pingFail(start, "proxied-server UOW provider not initialized")
	}
	ctx := rootCtx
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		return pingFail(start, "open unit of work: "+err.Error())
	}
	defer uw.Close(context.Background())
	storeMs := time.Since(start).Milliseconds()

	filter := types.IssueFilter{Limit: 1}
	if _, err := uw.IssueUseCase().SearchIssues(ctx, "", filter); err != nil {
		return pingFail(start, "query failed: "+err.Error())
	}
	totalMs := time.Since(start).Milliseconds()
	queryMs := totalMs - storeMs

	if jsonOutput {
		return outputJSON(map[string]interface{}{
			"status":     "ok",
			"resolve_ms": resolveMs,
			"store_ms":   storeMs - resolveMs,
			"query_ms":   queryMs,
			"total_ms":   totalMs,
		})
	}

	pingReportOK(totalMs)
	return nil
}
