package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/utils"
)

// printBurnDryRunIssues prints the "issues to delete" tree for a burn dry-run.
// Titles are routed through displayTitle (ui.SanitizeForTerminal) because a
// wisp/molecule title can originate from an untrusted import (JSONL/markdown/
// SCM) carrying OSC/CSI terminal-control escapes; the raw fmt.Printf sinks here
// bypassed the sanitize that `bd show` applies (beads-7n9y sink-class tail,
// sibling of j8li/ihaw). ephemeralOnly mirrors the wisp path's Ephemeral filter.
func printBurnDryRunIssues(subgraph *TemplateSubgraph, ephemeralOnly bool) {
	for _, issue := range subgraph.Issues {
		if ephemeralOnly && !issue.Ephemeral {
			continue
		}
		status := string(issue.Status)
		if issue.ID == subgraph.Root.ID {
			fmt.Printf("  - [%s] %s (%s) [ROOT]\n", status, displayTitle(issue.Title), issue.ID)
		} else {
			fmt.Printf("  - [%s] %s (%s)\n", status, displayTitle(issue.Title), issue.ID)
		}
	}
}

// outputBurnDryRunJSON emits the `bd mol burn --dry-run --json` preview as a
// parseable JSON envelope (beads-tcfwk, continuation of beads-51w8c), mirroring
// the fields printBurnDryRunIssues shows so a scripted preview parses with
// `| jq` instead of getting the plaintext block. ephemeralOnly matches the
// wisp-vs-persistent leg: a wisp burn only deletes ephemeral children, a
// persistent mol burn deletes the whole subgraph. Titles are sanitized via
// displayTitle for the same untrusted-import reason as the plaintext path
// (7n9y sink-class); the dry-run performs no mutation so this preview does not
// alter persisted data.
func outputBurnDryRunJSON(subgraph *TemplateSubgraph, moleculeID string, ephemeralOnly bool) error {
	type dryRunIssue struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Status string `json:"status"`
		Root   bool   `json:"root,omitempty"`
	}
	issues := make([]dryRunIssue, 0, len(subgraph.Issues))
	for _, issue := range subgraph.Issues {
		if ephemeralOnly && !issue.Ephemeral {
			continue
		}
		issues = append(issues, dryRunIssue{
			ID:     issue.ID,
			Title:  displayTitle(issue.Title),
			Status: string(issue.Status),
			Root:   issue.ID == subgraph.Root.ID,
		})
	}
	phase := "mol"
	if ephemeralOnly {
		phase = "wisp"
	}
	return outputJSON(map[string]interface{}{
		"dry_run":        true,
		"molecule_id":    moleculeID,
		"phase":          phase,
		"root_title":     displayTitle(subgraph.Root.Title),
		"would_delete":   len(issues),
		"creates_digest": false,
		"issues":         issues,
	})
}

var molBurnCmd = &cobra.Command{
	Use:   "burn <molecule-id> [molecule-id...]",
	Short: "Delete a molecule without creating a digest",
	Long: `Burn a molecule, deleting it without creating a digest.

Unlike squash (which creates a permanent digest before deletion), burn
completely removes the molecule with no trace. Use this for:
  - Abandoned patrol cycles
  - Crashed or failed workflows
  - Test/debug molecules you don't want to preserve

The burn operation differs based on molecule phase:
  - Wisp (ephemeral): Direct delete
  - Mol (persistent): Cascade delete (syncs to remotes)

CAUTION: This is a destructive operation. The molecule's data will be
permanently lost. If you want to preserve a summary, use 'bd mol squash'.

Example:
  bd mol burn bd-abc123              # Delete molecule with no trace
  bd mol burn bd-abc123 --dry-run    # Preview what would be deleted
  bd mol burn bd-abc123 --force      # Skip confirmation
  bd mol burn bd-a1 bd-b2 bd-c3      # Batch delete multiple wisps`,
	Args:          cobra.MinimumNArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runMolBurn,
}

// BurnResult holds the result of a burn operation
type BurnResult struct {
	MoleculeID   string   `json:"molecule_id"`
	DeletedIDs   []string `json:"deleted_ids"`
	DeletedCount int      `json:"deleted_count"`
}

// BatchBurnResult holds aggregated results when burning multiple molecules
type BatchBurnResult struct {
	Results      []BurnResult `json:"results"`
	TotalDeleted int          `json:"total_deleted"`
	FailedCount  int          `json:"failed_count"`
}

func runMolBurn(cmd *cobra.Command, args []string) error {
	CheckReadonly("mol burn")

	evt := metrics.NewCommandEvent("mol-burn")
	defer func() {
		if c := metrics.Global(); c != nil {
			c.CloseEventAndAdd(evt)
		}
	}()

	ctx := rootCtx

	// beads-ojyjj (aocj fail-loud class): in proxied-server mode main.go's
	// PersistentPreRunE returns before newDoltStore (main.go:1147-1155) leaving
	// the global store nil, so mol burn's store.GetIssue + transact would
	// nil-panic — and the bare store==nil check misdiagnoses the proxied config
	// as a local "no database connection". Burn writes via a raw
	// storage.Transaction the proxied UOW does not yield, so fail loud with an
	// accurate message (mirrors mgjco/merge-slot). Guard BEFORE the nil check.
	if usesProxiedServer() {
		return HandleErrorRespectJSON("mol burn is not supported in proxied-server mode (connect directly with an embedded/dolt store)")
	}
	if err := ensureStoreActive(); err != nil {
		return HandleErrorWithHintRespectJSON(err.Error(), diagHint())
	}

	dryRun, _ := cmd.Flags().GetBool("dry-run")
	force, _ := cmd.Flags().GetBool("force")
	if yes, _ := cmd.Flags().GetBool("yes"); yes {
		force = true
	}

	if len(args) == 1 {
		return burnSingleMolecule(ctx, args[0], dryRun, force)
	}

	return burnMultipleMolecules(ctx, args, dryRun, force)
}

func burnSingleMolecule(ctx context.Context, moleculeID string, dryRun, force bool) error {
	resolvedID, err := utils.ResolvePartialID(ctx, store, moleculeID)
	if err != nil {
		return HandleErrorRespectJSON("resolving molecule ID %s: %v", moleculeID, err)
	}

	rootIssue, err := store.GetIssue(ctx, resolvedID)
	if err != nil {
		return HandleErrorRespectJSON("loading molecule: %v", err)
	}

	if rootIssue.Ephemeral {
		return burnWispMolecule(ctx, resolvedID, dryRun, force)
	}
	return burnPersistentMolecule(ctx, resolvedID, dryRun, force)
}

func burnMultipleMolecules(ctx context.Context, moleculeIDs []string, dryRun, force bool) error {
	var wispIDs []string
	var persistentIDs []string
	var failedResolve []string

	// First pass: resolve and categorize all IDs
	for _, moleculeID := range moleculeIDs {
		resolvedID, err := utils.ResolvePartialID(ctx, store, moleculeID)
		if err != nil {
			if !jsonOutput {
				fmt.Fprintf(os.Stderr, "Warning: failed to resolve %s: %v\n", moleculeID, err)
			}
			failedResolve = append(failedResolve, moleculeID)
			continue
		}

		issue, err := store.GetIssue(ctx, resolvedID)
		if err != nil {
			if !jsonOutput {
				fmt.Fprintf(os.Stderr, "Warning: failed to load %s: %v\n", resolvedID, err)
			}
			failedResolve = append(failedResolve, moleculeID)
			continue
		}

		if issue.Ephemeral {
			wispIDs = append(wispIDs, resolvedID)
		} else {
			persistentIDs = append(persistentIDs, resolvedID)
		}
	}

	if len(wispIDs) == 0 && len(persistentIDs) == 0 {
		if jsonOutput {
			// beads-m9gn: init Results so the no-valid-molecules branch emits
			// "results":[] not null, matching the success path (:197 uses
			// make([]BurnResult, 0)). Results is []BurnResult with no omitempty,
			// so the bare BatchBurnResult{FailedCount:...} literal left it nil —
			// the guib/036h/5fv3/jxel/4mkg/8wyu nil-slice asymmetry, here across
			// two branches of the same command.
			_ = outputJSON(BatchBurnResult{Results: make([]BurnResult, 0), FailedCount: len(failedResolve)})
			if len(failedResolve) > 0 {
				return &exitError{Code: 1}
			}
			return nil
		}
		fmt.Println("No valid molecules to burn")
		if len(failedResolve) > 0 {
			// Every id failed to resolve/load: exit non-zero so scripts don't
			// silently treat an all-failed burn as success (beads-uscf).
			return &exitError{Code: 1}
		}
		return nil
	}

	if dryRun {
		if jsonOutput {
			// beads-tcfwk: the batch dry-run already suppressed plaintext under
			// --json, but emitted NOTHING — so `bd mol burn a b --dry-run --json |
			// jq` failed on empty input. Emit a parseable envelope at parity with
			// the single-molecule path so the whole `mol burn --dry-run --json`
			// surface is machine-readable. Slices are made non-nil ([] not null),
			// matching the BatchBurnResult nil-slice contract (beads-m9gn).
			nonNil := func(s []string) []string {
				if s == nil {
					return []string{}
				}
				return s
			}
			_ = outputJSON(map[string]interface{}{
				"dry_run":        true,
				"batch":          true,
				"would_burn":     len(wispIDs) + len(persistentIDs),
				"wisp_ids":       nonNil(wispIDs),
				"persistent_ids": nonNil(persistentIDs),
				"failed_resolve": nonNil(failedResolve),
				"creates_digest": false,
			})
			if len(failedResolve) > 0 {
				return &exitError{Code: 1}
			}
			return nil
		}
		fmt.Printf("\nDry run: would burn %d wisp(s) and %d persistent molecule(s)\n", len(wispIDs), len(persistentIDs))
		if len(wispIDs) > 0 {
			fmt.Printf("\nWisps to delete:\n")
			for _, id := range wispIDs {
				fmt.Printf("  - %s\n", id)
			}
		}
		if len(persistentIDs) > 0 {
			fmt.Printf("\nPersistent molecules to delete:\n")
			for _, id := range persistentIDs {
				fmt.Printf("  - %s\n", id)
			}
		}
		if len(failedResolve) > 0 {
			fmt.Printf("\nFailed to resolve (%d):\n", len(failedResolve))
			for _, id := range failedResolve {
				fmt.Printf("  - %s\n", id)
			}
		}
		if len(failedResolve) > 0 {
			// A dry-run that couldn't resolve every id exits non-zero too, matching
			// the single-molecule path (which errors at resolve) (beads-uscf).
			return &exitError{Code: 1}
		}
		return nil
	}

	if !force && !jsonOutput {
		fmt.Printf("About to burn %d wisp(s) and %d persistent molecule(s)\n", len(wispIDs), len(persistentIDs))
		fmt.Printf("This will permanently delete all molecule data with no digest.\n")
		fmt.Printf("\nContinue? [y/N] ")

		var response string
		_, _ = fmt.Scanln(&response)
		if response != "y" && response != "Y" {
			fmt.Println("Canceled.")
			return nil
		}
	}

	batchResult := BatchBurnResult{
		Results:     make([]BurnResult, 0),
		FailedCount: len(failedResolve),
	}

	// Batch delete all wisps in one call
	if len(wispIDs) > 0 {
		result, err := burnWisps(ctx, store, wispIDs)
		if err != nil {
			if !jsonOutput {
				fmt.Fprintf(os.Stderr, "Error burning wisps: %v\n", err)
			}
		} else {
			batchResult.TotalDeleted += result.DeletedCount
			batchResult.Results = append(batchResult.Results, *result)
		}
	}

	// Handle persistent molecules individually (they need subgraph loading)
	for _, id := range persistentIDs {
		subgraph, err := loadTemplateSubgraph(ctx, store, id)
		if err != nil {
			if !jsonOutput {
				fmt.Fprintf(os.Stderr, "Warning: failed to load subgraph for %s: %v\n", id, err)
			}
			batchResult.FailedCount++
			continue
		}

		var issueIDs []string
		for _, issue := range subgraph.Issues {
			issueIDs = append(issueIDs, issue.ID)
		}

		if err := deleteBatch(nil, issueIDs, true, false, false, false, false, "mol burn"); err != nil {
			return HandleErrorRespectJSON("%v", err)
		}
		batchResult.TotalDeleted += len(issueIDs)
		batchResult.Results = append(batchResult.Results, BurnResult{
			MoleculeID:   id,
			DeletedIDs:   issueIDs,
			DeletedCount: len(issueIDs),
		})
	}

	if jsonOutput {
		_ = outputJSON(batchResult)
		if batchResult.FailedCount > 0 {
			return &exitError{Code: 1}
		}
		return nil
	}

	fmt.Printf("%s Burned %d molecule(s): %d issues deleted\n", ui.RenderPass("✓"), len(wispIDs)+len(persistentIDs), batchResult.TotalDeleted)
	if batchResult.FailedCount > 0 {
		fmt.Printf("  %d failed\n", batchResult.FailedCount)
		// Partial failure (some ids failed to resolve/load or a subgraph failed
		// to load): the valid molecules were still burned, but exit non-zero so
		// scripts don't proceed as if everything succeeded (beads-uscf).
		return &exitError{Code: 1}
	}
	return nil
}

func burnWispMolecule(ctx context.Context, resolvedID string, dryRun, force bool) error {
	subgraph, err := loadTemplateSubgraph(ctx, store, resolvedID)
	if err != nil {
		return HandleErrorRespectJSON("loading wisp molecule: %v", err)
	}

	var wispIDs []string
	for _, issue := range subgraph.Issues {
		if issue.Ephemeral {
			wispIDs = append(wispIDs, issue.ID)
		}
	}

	if len(wispIDs) == 0 {
		if jsonOutput {
			return outputJSON(BurnResult{
				MoleculeID:   resolvedID,
				DeletedCount: 0,
			})
		}
		fmt.Printf("No wisp issues found for molecule %s\n", resolvedID)
		return nil
	}

	if dryRun {
		// beads-tcfwk (8lqh --json-contract family, continuation of beads-51w8c):
		// under --json emit a parseable preview envelope instead of the plaintext
		// block, so a scripted `bd mol burn <wisp> --dry-run --json | jq` parses.
		if jsonOutput {
			return outputBurnDryRunJSON(subgraph, resolvedID, true)
		}
		fmt.Printf("\nDry run: would burn wisp %s\n\n", resolvedID)
		fmt.Printf("Root: %s\n", displayTitle(subgraph.Root.Title))
		fmt.Printf("\nWisp issues to delete (%d total):\n", len(wispIDs))
		printBurnDryRunIssues(subgraph, true)
		fmt.Printf("\nNo digest will be created (use 'bd mol squash' to create one).\n")
		return nil
	}

	if !force && !jsonOutput {
		fmt.Printf("About to burn wisp %s (%d issues)\n", resolvedID, len(wispIDs))
		fmt.Printf("This will permanently delete all wisp data with no digest.\n")
		fmt.Printf("Use 'bd mol squash' instead if you want to preserve a summary.\n")
		fmt.Printf("\nContinue? [y/N] ")

		var response string
		_, _ = fmt.Scanln(&response)
		if response != "y" && response != "Y" {
			fmt.Println("Canceled.")
			return nil
		}
	}

	result, err := burnWisps(ctx, store, wispIDs)
	if err != nil {
		return HandleErrorRespectJSON("burning wisp: %v", err)
	}
	result.MoleculeID = resolvedID

	if jsonOutput {
		return outputJSON(result)
	}

	fmt.Printf("%s Burned wisp: %d issues deleted\n", ui.RenderPass("✓"), result.DeletedCount)
	fmt.Printf("  Ephemeral: %s\n", resolvedID)
	fmt.Printf("  No digest created.\n")
	return nil
}

func burnPersistentMolecule(ctx context.Context, resolvedID string, dryRun, force bool) error {
	subgraph, err := loadTemplateSubgraph(ctx, store, resolvedID)
	if err != nil {
		return HandleErrorRespectJSON("loading molecule: %v", err)
	}

	var issueIDs []string
	for _, issue := range subgraph.Issues {
		issueIDs = append(issueIDs, issue.ID)
	}

	if len(issueIDs) == 0 {
		if jsonOutput {
			return outputJSON(BurnResult{
				MoleculeID:   resolvedID,
				DeletedCount: 0,
			})
		}
		fmt.Printf("No issues found for molecule %s\n", resolvedID)
		return nil
	}

	if dryRun {
		// beads-tcfwk (8lqh --json-contract family, continuation of beads-51w8c):
		// under --json emit a parseable preview envelope instead of the plaintext
		// block, so a scripted `bd mol burn <mol> --dry-run --json | jq` parses.
		if jsonOutput {
			return outputBurnDryRunJSON(subgraph, resolvedID, false)
		}
		fmt.Printf("\nDry run: would burn mol %s\n\n", resolvedID)
		fmt.Printf("Root: %s\n", displayTitle(subgraph.Root.Title))
		fmt.Printf("\nIssues to delete (%d total):\n", len(issueIDs))
		printBurnDryRunIssues(subgraph, false)
		fmt.Printf("\nNote: Persistent mol - deletions sync to remotes.\n")
		fmt.Printf("No digest will be created (use 'bd mol squash' to create one).\n")
		return nil
	}

	if !force && !jsonOutput {
		fmt.Printf("About to burn mol %s (%d issues)\n", resolvedID, len(issueIDs))
		fmt.Printf("This will permanently delete all molecule data with no digest.\n")
		fmt.Printf("Note: Persistent mol - deletions sync to remotes.\n")
		fmt.Printf("Use 'bd mol squash' instead if you want to preserve a summary.\n")
		fmt.Printf("\nContinue? [y/N] ")

		var response string
		_, _ = fmt.Scanln(&response)
		if response != "y" && response != "Y" {
			fmt.Println("Canceled.")
			return nil
		}
	}

	if err := deleteBatch(nil, issueIDs, true, false, false, jsonOutput, false, "mol burn"); err != nil {
		return HandleErrorRespectJSON("%v", err)
	}
	return nil
}

// burnWisps deletes all wisp issues atomically within a single transaction.
// If any delete fails, the entire operation is rolled back to prevent partial deletion.
func burnWisps(ctx context.Context, s storage.DoltStorage, ids []string) (*BurnResult, error) {
	result := &BurnResult{
		DeletedIDs: make([]string, 0, len(ids)),
	}

	// beads-t0h3z: the result accumulator must be built INSIDE the closure so a
	// withRetry re-invocation (serialization conflict / connection blip on the
	// DoltStore path) starts fresh each attempt. Accumulating on the outer
	// `result` across attempts double-counts (DeletedCount inflated,
	// DeletedIDs carrying duplicate ids). Last-successful-attempt wins because
	// the whole closure re-runs; we only publish to `result` on success.
	err := transact(ctx, s, "bd: burn wisps", func(tx storage.Transaction) error {
		deletedIDs := make([]string, 0, len(ids))
		for _, id := range ids {
			if err := tx.DeleteIssue(ctx, id); err != nil {
				return fmt.Errorf("failed to delete wisp %s: %w", id, err)
			}
			deletedIDs = append(deletedIDs, id)
		}
		result.DeletedIDs = deletedIDs
		result.DeletedCount = len(deletedIDs)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

func init() {
	molBurnCmd.Flags().Bool("dry-run", false, "Preview what would be deleted")
	molBurnCmd.Flags().Bool("force", false, "Skip confirmation prompt")
	molBurnCmd.Flags().BoolP("yes", "y", false, "Alias for --force (skip confirmation)")
	_ = molBurnCmd.Flags().MarkHidden("yes")

	molCmd.AddCommand(molBurnCmd)
}
