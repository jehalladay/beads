package main

import (
	"context"
	"fmt"
	"os"

	"github.com/steveyegge/beads/internal/types"
)

// runCompactStatsProxiedServer serves `bd admin compact --stats` on
// hub-connected crew where the global `store` is nil in proxiedServerMode
// (beads-aocj compact leg). The direct path (runCompactStats) dereferences the
// nil store and panics; this fetches the Tier 1/2 candidate sets through the
// proxied UOW (IssueUseCase.GetTier1/2CompactionCandidates) and renders the
// identical output via renderCompactStats.
//
// Only the read-only --stats mode is served through the proxied UOW. The
// mutating modes (--analyze/--apply/--auto) require the direct compaction store
// (snapshot + overwrite + the AI compactor) and remain gated by ensureDirectMode
// in the command dispatch, so they fail with a clean hinted error rather than a
// nil-store panic.
func runCompactStatsProxiedServer(ctx context.Context) error {
	if uowProvider == nil {
		FatalError("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("open unit of work: %v", err)
	}
	defer uw.Close(ctx)

	tier1, err := uw.IssueUseCase().GetTier1CompactionCandidates(ctx)
	if err != nil {
		return HandleErrorRespectJSON("failed to get Tier 1 candidates: %v", err)
	}
	tier2, err := uw.IssueUseCase().GetTier2CompactionCandidates(ctx)
	if err != nil {
		return HandleErrorRespectJSON("failed to get Tier 2 candidates: %v", err)
	}

	renderCompactStats(tier1, tier2)
	return nil
}

// renderCompactStats prints the compaction-statistics summary for the two
// candidate tiers, honoring --json. Shared by the direct (runCompactStats) and
// proxied (runCompactStatsProxiedServer) paths so their output stays identical.
func renderCompactStats(tier1, tier2 []*types.CompactionCandidate) {
	tier1Size := 0
	for _, c := range tier1 {
		tier1Size += c.OriginalSize
	}
	tier2Size := 0
	for _, c := range tier2 {
		tier2Size += c.OriginalSize
	}

	if jsonOutput {
		output := map[string]interface{}{
			"tier1": map[string]interface{}{
				"candidates": len(tier1),
				"total_size": tier1Size,
			},
			"tier2": map[string]interface{}{
				"candidates":  len(tier2),
				"total_size":  tier2Size,
				"implemented": false,
			},
		}
		if err := outputJSON(output); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		return
	}

	fmt.Println("Compaction Statistics")
	fmt.Printf("Tier 1 (30+ days closed):\n")
	fmt.Printf("  Candidates: %d\n", len(tier1))
	fmt.Printf("  Total size: %d bytes\n", tier1Size)
	if tier1Size > 0 {
		fmt.Printf("  Estimated savings: %d bytes (70%%)\n\n", tier1Size*7/10)
	}

	fmt.Printf("Tier 2 (90+ days closed, Tier 1 compacted): not yet implemented\n")
	fmt.Printf("  Candidates: %d\n", len(tier2))
	fmt.Printf("  Total size: %d bytes\n", tier2Size)
}
