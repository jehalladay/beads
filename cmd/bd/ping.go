package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/beads"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

var pingCmd = &cobra.Command{
	Use:     "ping",
	Args:    cobra.NoArgs, // beads-7pnnd: reject stray positionals with a clean usage error
	GroupID: "maint",
	Short:   "Check database connectivity",
	Long: `Lightweight health check that confirms bd can reach its database.

Steps:
  1. Resolve the .beads workspace
  2. Open the store (embedded or server)
  3. Run a trivial query (issue count)
  4. Report timing

Exit 0 on success, exit 1 on failure.

Examples:
  bd ping              # Quick connectivity check
  bd ping --json       # Structured output for automation`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("ping")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		start := time.Now()

		beadsDir := beads.FindBeadsDir()
		if beadsDir == "" {
			return pingFail(start, "no .beads directory found")
		}
		resolveMs := time.Since(start).Milliseconds()

		// beads-jegd5: in proxied-server mode the global `store` is nil (main.go
		// PersistentPreRunE wires uowProvider then returns BEFORE store init), so
		// getStore() below returns nil and ping ALWAYS false-failed "store not
		// initialized" for the entire hub-connected fleet — even with a healthy
		// proxied connection, and contradicting ping's own help ("Open the store
		// (embedded OR server)"). ping is read-only (SearchIssues Limit:1), so
		// route through the proxied read UOW, mirroring bd count/list/show
		// (count_proxied_server.go / list_proxied_server.go, the aocj/fszd/eh0z
		// umbrella that missed this sibling — same class as beads-9dsym bd todo).
		if usesProxiedServer() {
			return runPingProxiedServer(start, resolveMs)
		}

		st := getStore()
		if st == nil {
			return pingFail(start, "store not initialized")
		}
		if lm, ok := storage.UnwrapStore(st).(storage.LifecycleManager); ok && lm.IsClosed() {
			return pingFail(start, "store is closed")
		}
		storeMs := time.Since(start).Milliseconds()

		filter := types.IssueFilter{Limit: 1}
		_, err := st.SearchIssues(rootCtx, "", filter)
		if err != nil {
			return pingFail(start, fmt.Sprintf("query failed: %v", err))
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
	},
}

// pingReportOK prints the plain-text success line. Shared by the direct RunE and
// the proxied leg (runPingProxiedServer, beads-jegd5) so both are byte-identical.
func pingReportOK(totalMs int64) {
	fmt.Fprintf(os.Stdout, "%s bd ping: ok (%dms)\n", ui.RenderPass("✓"), totalMs)
}

func pingFail(start time.Time, reason string) error {
	totalMs := time.Since(start).Milliseconds()
	if jsonOutput {
		if jerr := outputJSON(map[string]interface{}{
			"status":   "error",
			"error":    reason,
			"total_ms": totalMs,
		}); jerr != nil {
			return jerr
		}
		return SilentExit()
	}
	return HandleError("bd ping: %s (%dms)", reason, totalMs)
}

func init() {
	rootCmd.AddCommand(pingCmd)
}
