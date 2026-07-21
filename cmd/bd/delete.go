package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

var deleteCmd = &cobra.Command{
	Use:     "delete <issue-id> [issue-id...]",
	GroupID: "issues",
	Short:   "Delete one or more issues and clean up references",
	Long: `Delete one or more issues and clean up all references to them.
This command will:
1. Remove all dependency links (any type, both directions) involving the issues
2. Update text references to "[deleted:ID]" in directly connected issues
3. Permanently delete the issues from the database

This is a destructive operation that cannot be undone. Use with caution.

BATCH DELETION:
Delete multiple issues at once:
  bd delete bd-1 bd-2 bd-3 --force

Delete from file (one ID per line):
  bd delete --from-file deletions.txt --force

Preview before deleting:
  bd delete --from-file deletions.txt --dry-run

DEPENDENCY HANDLING:
Default: Fails if any issue has dependents not in deletion set
  bd delete bd-1 bd-2

Cascade: Recursively delete all dependents
  bd delete bd-1 --cascade --force

Force: Delete and orphan dependents
  bd delete bd-1 --force`,
	Args:          cobra.MinimumNArgs(0),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		CheckReadonly("delete")

		evt := metrics.NewCommandEvent("delete")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		if usesProxiedServer() {
			runDeleteProxiedServer(cmd, rootCtx, args)
			return nil
		}

		fromFile, _ := cmd.Flags().GetString("from-file")
		force, _ := cmd.Flags().GetBool("force")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		cascade, _ := cmd.Flags().GetBool("cascade")
		issueIDs := make([]string, 0, len(args))
		issueIDs = append(issueIDs, args...)
		if fromFile != "" {
			fileIDs, err := readIssueIDsFromFile(fromFile)
			if err != nil {
				return HandleErrorRespectJSON("reading file: %v", err)
			}
			issueIDs = append(issueIDs, fileIDs...)
		}
		if len(issueIDs) == 0 {
			_ = cmd.Usage()
			return HandleErrorRespectJSON("no issue IDs provided")
		}
		issueIDs = uniqueStrings(issueIDs)

		if store == nil {
			if err := ensureStoreActive(); err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
		}

		if len(issueIDs) > 1 || cascade {
			if err := deleteBatch(cmd, issueIDs, force, dryRun, cascade, jsonOutput, false); err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
			return nil
		}

		issueID := issueIDs[0]
		ctx := rootCtx
		// Get the issue to be deleted, using prefix-based routing
		routedResult, err := resolveAndGetIssueForMutation(ctx, store, issueID)
		if err != nil {
			if isNotFoundErr(err) {
				return HandleErrorRespectJSON("issue %s not found", issueID)
			}
			return HandleErrorRespectJSON("%v", err)
		}
		defer routedResult.Close()
		issue := routedResult.Issue
		issueID = routedResult.ResolvedID
		activeStore := routedResult.Store
		connectedIssues := make(map[string]*types.Issue)
		deps, err := activeStore.GetDependencies(ctx, issueID)
		if err != nil {
			return HandleErrorRespectJSON("getting dependencies: %v", err)
		}
		for _, dep := range deps {
			connectedIssues[dep.ID] = dep
		}
		dependents, err := activeStore.GetDependents(ctx, issueID)
		if err != nil {
			return HandleErrorRespectJSON("getting dependents: %v", err)
		}
		for _, dependent := range dependents {
			connectedIssues[dependent.ID] = dependent
		}
		depRecords, err := activeStore.GetDependencyRecords(ctx, issueID)
		if err != nil {
			return HandleErrorRespectJSON("getting dependency records: %v", err)
		}
		// Build the regex pattern for matching issue IDs (handles hyphenated IDs properly)
		// Pattern: (^|non-word-char)(issueID)($|non-word-char) where word-char includes hyphen
		idPattern := `(^|[^A-Za-z0-9_-])(` + regexp.QuoteMeta(issueID) + `)($|[^A-Za-z0-9_-])`
		re := regexp.MustCompile(idPattern)
		if !force {
			if jsonOutput {
				// The DIRECT preview leg previously ignored --json and printed the
				// human DELETE PREVIEW unconditionally, leaking unparseable
				// plaintext (rc=0) to a --json consumer (beads-fgwko). Emit a JSON
				// envelope instead, computed from the SAME preview variables the
				// human path uses, so the reported counts match what the direct
				// force path will actually do (dependencies_removed /
				// references_updated — the direct force-path output keys). We do
				// NOT reuse the proxied twin's DeleteIssues-derived schema: the
				// domain DeleteIssues use-case is stricter (it errors on dependents
				// unless --cascade), whereas the direct path simply removes the
				// dependency edges — so its counts, not DeleteIssues', are the
				// truthful preview here.
				referencesToUpdate := 0
				for _, connIssue := range connectedIssues {
					// beads-lj36j: include title so the preview count matches
					// what the force path now rewrites.
					if re.MatchString(connIssue.Title) ||
						re.MatchString(connIssue.Description) ||
						(connIssue.Notes != "" && re.MatchString(connIssue.Notes)) ||
						(connIssue.Design != "" && re.MatchString(connIssue.Design)) ||
						(connIssue.AcceptanceCriteria != "" && re.MatchString(connIssue.AcceptanceCriteria)) {
						referencesToUpdate++
					}
				}
				return outputJSON(map[string]any{
					"would_delete":         issueID,
					"dependencies_removed": len(depRecords) + len(dependents),
					"references_updated":   referencesToUpdate,
					"connected":            sortedKeys(connectedIssues),
					"dry_run":              true,
				})
			}
			fmt.Printf("\n%s\n", ui.RenderFail("⚠️  DELETE PREVIEW"))
			fmt.Printf("\nIssue to delete:\n")
			fmt.Printf("  %s: %s\n", issueID, displayTitle(issue.Title))
			totalDeps := len(depRecords) + len(dependents)
			if totalDeps > 0 {
				fmt.Printf("\nDependency links to remove: %d\n", totalDeps)
				for _, dep := range depRecords {
					fmt.Printf("  %s → %s (%s)\n", dep.IssueID, dep.DependsOnID, dep.Type)
				}
				for _, dep := range dependents {
					fmt.Printf("  %s → %s (inbound)\n", dep.ID, issueID)
				}
			}
			if len(connectedIssues) > 0 {
				fmt.Printf("\nConnected issues where text references will be updated:\n")
				issuesWithRefs := 0
				for id, connIssue := range connectedIssues {
					// beads-lj36j: include title (force now rewrites it too).
					hasRefs := re.MatchString(connIssue.Title) ||
						re.MatchString(connIssue.Description) ||
						(connIssue.Notes != "" && re.MatchString(connIssue.Notes)) ||
						(connIssue.Design != "" && re.MatchString(connIssue.Design)) ||
						(connIssue.AcceptanceCriteria != "" && re.MatchString(connIssue.AcceptanceCriteria))
					if hasRefs {
						fmt.Printf("  %s: %s\n", id, displayTitle(connIssue.Title))
						issuesWithRefs++
					}
				}
				if issuesWithRefs == 0 {
					fmt.Printf("  (none have text references)\n")
				}
			}
			fmt.Printf("\n%s\n", ui.RenderWarn("This operation cannot be undone!"))
			fmt.Printf("To proceed, run: %s\n\n", ui.RenderWarn("bd delete "+issueID+" --force"))
			return nil
		}
		// beads-qh4jx: count the labels + events the delete removes so the
		// single-force --json/text output reports them like the batch path
		// (batchStore.DeleteIssues → DeleteResult{LabelsCount,EventsCount}). The
		// issue row's DELETE cascades to labels/events via ON DELETE CASCADE
		// (migrations 0003/0005), so these ARE removed — the single path just
		// never counted or reported them (a SILENT count-drop, not just an absent
		// key). Read before the tx (the rows still exist); a read failure is
		// non-fatal to the delete — report 0 rather than abort a destructive op
		// on a stats read.
		labelsRemoved := 0
		if labels, lerr := activeStore.GetLabels(ctx, issueID); lerr == nil {
			labelsRemoved = len(labels)
		}
		eventsRemoved := 0
		if events, eerr := activeStore.GetEvents(ctx, issueID, 0); eerr == nil {
			eventsRemoved = len(events)
		}
		// Force-deleting orphans the surviving inbound dependents (their edge to
		// the target is removed; the issues themselves are kept), matching the
		// batch path's orphaned_issues (external dependents under --force).
		orphanedIssues := make([]string, 0, len(dependents))
		for _, dep := range dependents {
			orphanedIssues = append(orphanedIssues, dep.ID)
		}
		sort.Strings(orphanedIssues)

		updatedIssueCount := 0
		totalDepsRemoved := 0
		deleteErr := transactHonoringAutoCommit(ctx, activeStore, fmt.Sprintf("bd: delete %s", issueID), func(tx storage.Transaction) error {
			// beads-if01i: reset the accumulators at closure entry.
			// transactHonoringAutoCommit → RunInTransaction → withRetry
			// re-invokes this closure on a retryable error (serialization
			// conflict 1213/1205, pre-commit connection blip) from a
			// rolled-back state; without the reset a retry adds each attempt's
			// increments on top of the last, inflating the reported
			// "Removed N dependency link(s)" / "Updated text references in N
			// issue(s)" (human + --json). The SQL tx itself rolls back per
			// attempt, so only the report drifted. Same class as t0h3z
			// (burn/squash/batch); the single-delete leg was out of its scope.
			updatedIssueCount = 0
			totalDepsRemoved = 0
			// beads-36d6n: loop-to-fixed-point rewriter so a run of adjacent
			// references sharing one delimiter is fully rewritten (single-pass
			// left the second as a dangling live ref).
			rewrite := deletedReferenceRewriter(issueID)
			for id, connIssue := range connectedIssues {
				updates := make(map[string]interface{})
				// beads-lj36j: rewrite the title too, matching the domain
				// rewriter (beads-989m0) and the rename/rename_prefix twins.
				// This DIRECT single-delete leg was missed by 989m0 (which only
				// reached the proxied leg via the domain path), leaving a
				// dangling live ref in the field shown in every list/show view.
				if v, ok := rewrite(connIssue.Title); ok {
					updates["title"] = v
				}
				if v, ok := rewrite(connIssue.Description); ok {
					updates["description"] = v
				}
				if connIssue.Notes != "" {
					if v, ok := rewrite(connIssue.Notes); ok {
						updates["notes"] = v
					}
				}
				if connIssue.Design != "" {
					if v, ok := rewrite(connIssue.Design); ok {
						updates["design"] = v
					}
				}
				if connIssue.AcceptanceCriteria != "" {
					if v, ok := rewrite(connIssue.AcceptanceCriteria); ok {
						updates["acceptance_criteria"] = v
					}
				}
				if len(updates) > 0 {
					if err := tx.UpdateIssue(ctx, id, updates, actor); err != nil {
						return fmt.Errorf("update references in %s: %w", id, err)
					}
					updatedIssueCount++
				}
			}
			for _, dep := range depRecords {
				if err := tx.RemoveDependency(ctx, dep.IssueID, dep.DependsOnID, actor); err != nil {
					return fmt.Errorf("remove dependency %s → %s: %w", dep.IssueID, dep.DependsOnID, err)
				}
				totalDepsRemoved++
			}
			for _, dep := range dependents {
				if err := tx.RemoveDependency(ctx, dep.ID, issueID, actor); err != nil {
					return fmt.Errorf("remove dependency %s → %s: %w", dep.ID, issueID, err)
				}
				totalDepsRemoved++
			}
			if err := tx.DeleteIssue(ctx, issueID); err != nil {
				return fmt.Errorf("delete %s: %w", issueID, err)
			}
			return nil
		})
		if deleteErr != nil {
			return HandleErrorRespectJSON("deleting issue: %v", deleteErr)
		}

		// beads-au6dv: the field rewrites above tombstone id references in the 5
		// issue text fields, but a reference an author wrote inside a COMMENT body
		// ("see bd-abc") was never visited — so after the delete it kept the live
		// id and became a dangling reference to a now-deleted issue, the exact
		// gap g8qfo just closed for rename. Rewrite matching comment bodies on the
		// connected issues too, reusing the shared rewriteCommentRefs helper +
		// the loop-to-fixed-point tombstone rewriter. Best-effort follow pass
		// (outside the delete tx, matching rename's non-tx comment sweep); a
		// comment-rewrite failure must not fail the already-committed delete.
		commentRewrite := deletedReferenceRewriter(issueID)
		for id := range connectedIssues {
			if err := rewriteCommentRefs(ctx, activeStore, id, commentRewrite); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
			}
		}

		commandDidWrite.Store(true)

		if jsonOutput {
			// beads-qh4jx: emit the SAME shape as the batch/proxied paths so a
			// --json consumer sees one stable contract regardless of arg count:
			// - "deleted" is ALWAYS an array (was a bare string on single delete,
			//   type-flipping string↔array between 1 and N args — a load-bearing
			//   divergence for a consumer parsing d["deleted"]);
			// - deleted_count / labels_removed / events_removed / orphaned_issues
			//   are now present (single path previously dropped them, including
			//   the labels/events that WERE deleted — a silent count-drop).
			if err := outputJSON(map[string]interface{}{
				"deleted":              []string{issueID},
				"deleted_count":        1,
				"dependencies_removed": totalDepsRemoved,
				"labels_removed":       labelsRemoved,
				"events_removed":       eventsRemoved,
				"references_updated":   updatedIssueCount,
				"orphaned_issues":      orphanedIssues,
			}); err != nil {
				return err
			}
		} else {
			fmt.Printf("%s Deleted %s\n", ui.RenderPass("✓"), issueID)
			fmt.Printf("  Removed %d dependency link(s)\n", totalDepsRemoved)
			fmt.Printf("  Removed %d label(s)\n", labelsRemoved)
			fmt.Printf("  Removed %d event(s)\n", eventsRemoved)
			fmt.Printf("  Updated text references in %d issue(s)\n", updatedIssueCount)
			if len(orphanedIssues) > 0 {
				fmt.Printf("  %s Orphaned %d issue(s): %s\n",
					ui.RenderWarn("⚠"), len(orphanedIssues), strings.Join(orphanedIssues, ", "))
			}
		}
		return nil
	},
}

// deleteIssue removes an issue from the database.
func deleteIssue(ctx context.Context, issueID string) error {
	return store.DeleteIssue(ctx, issueID)
}

//nolint:unparam // cmd parameter required for potential future use
func deleteBatch(_ *cobra.Command, issueIDs []string, force bool, dryRun bool, cascade bool, jsonOutput bool, _ bool, _ ...string) error {
	if store == nil {
		if err := ensureStoreActive(); err != nil {
			return err
		}
	}
	ctx := rootCtx
	issues := make(map[string]*types.Issue)
	notFound := []string{}
	var routedStore storage.DoltStorage
	for _, id := range issueIDs {
		result, err := resolveAndGetIssueForMutation(ctx, store, id)
		if err != nil {
			if isNotFoundErr(err) {
				notFound = append(notFound, id)
			} else {
				return fmt.Errorf("getting issue %s: %v", id, err)
			}
		} else {
			issues[result.ResolvedID] = result.Issue
			if result.Routed && routedStore == nil {
				routedStore = result.Store
			} else {
				result.Close()
			}
		}
	}
	if routedStore != nil {
		defer func() { _ = routedStore.Close() }()
	}
	if len(notFound) > 0 {
		return fmt.Errorf("issues not found: %s", strings.Join(notFound, ", "))
	}
	batchStore := store
	if routedStore != nil {
		batchStore = routedStore
	}
	if dryRun || !force {
		result, err := batchStore.DeleteIssues(ctx, issueIDs, cascade, false, true)
		if err != nil {
			showDeletionPreview(issueIDs, issues, cascade, err)
			return err
		}
		showDeletionPreview(issueIDs, issues, cascade, nil)
		fmt.Printf("\nWould delete: %d issues\n", result.DeletedCount)
		fmt.Printf("Would remove: %d dependencies, %d labels, %d events\n",
			result.DependenciesCount, result.LabelsCount, result.EventsCount)
		if len(result.OrphanedIssues) > 0 {
			fmt.Printf("Would orphan: %d issues\n", len(result.OrphanedIssues))
		}
		if dryRun {
			fmt.Printf("\n(Dry-run mode - no changes made)\n")
		} else {
			fmt.Printf("\n%s\n", ui.RenderWarn("This operation cannot be undone!"))
			if cascade {
				fmt.Printf("To proceed with cascade deletion, run: %s\n",
					ui.RenderWarn("bd delete "+strings.Join(issueIDs, " ")+" --cascade --force"))
			} else {
				fmt.Printf("To proceed, run: %s\n",
					ui.RenderWarn("bd delete "+strings.Join(issueIDs, " ")+" --force"))
			}
		}
		return nil
	}
	// beads-rb00b: the text-reference tombstone pass moved INTO the storage-layer
	// delete (issueops.DeleteIssuesInTx), so it now runs atomically inside the
	// same delete transaction and covers every bulk path (gc/purge/prune/burn),
	// not just this cmd handler. result.ReferencesUpdated carries the count; the
	// old collect-then-rewrite-after-delete dance here is gone (it rewrote in a
	// second transaction and left the other bulk paths uncovered).
	result, err := batchStore.DeleteIssues(ctx, issueIDs, cascade, force, false)
	if err != nil {
		return err
	}

	updatedCount := result.ReferencesUpdated

	commandDidWrite.Store(true)

	if jsonOutput {
		if err := outputJSON(map[string]interface{}{
			"deleted":              issueIDs,
			"deleted_count":        result.DeletedCount,
			"dependencies_removed": result.DependenciesCount,
			"labels_removed":       result.LabelsCount,
			"events_removed":       result.EventsCount,
			"references_updated":   updatedCount,
			"orphaned_issues":      result.OrphanedIssues,
		}); err != nil {
			return err
		}
	} else {
		fmt.Printf("%s Deleted %d issue(s)\n", ui.RenderPass("✓"), result.DeletedCount)
		fmt.Printf("  Removed %d dependency link(s)\n", result.DependenciesCount)
		fmt.Printf("  Removed %d label(s)\n", result.LabelsCount)
		fmt.Printf("  Removed %d event(s)\n", result.EventsCount)
		fmt.Printf("  Updated text references in %d issue(s)\n", updatedCount)
		if len(result.OrphanedIssues) > 0 {
			fmt.Printf("  %s Orphaned %d issue(s): %s\n",
				ui.RenderWarn("⚠"), len(result.OrphanedIssues), strings.Join(result.OrphanedIssues, ", "))
		}
	}
	return nil
}

// showDeletionPreview shows what would be deleted
func showDeletionPreview(issueIDs []string, issues map[string]*types.Issue, cascade bool, depError error) {
	fmt.Printf("\n%s\n", ui.RenderFail("⚠️  DELETE PREVIEW"))
	fmt.Printf("\nIssues to delete (%d):\n", len(issueIDs))
	for _, id := range issueIDs {
		if issue := issues[id]; issue != nil {
			fmt.Printf("  %s: %s\n", id, displayTitle(issue.Title))
		}
	}
	if cascade {
		fmt.Printf("\n%s Cascade mode enabled - will also delete all dependent issues\n", ui.RenderWarn("⚠"))
	}
	if depError != nil {
		fmt.Printf("\n%s\n", ui.RenderFail(depError.Error()))
	}
}

// deletedReferenceRewriter delegates to the single source of truth,
// domain.DeletedReferenceRewriter (beads-rb00b consolidated the three former
// copies — cmd/domain/storage — into one shared, idempotent implementation).
// It replaces EVERY standalone LIVE reference to id with the "[deleted:id]"
// tombstone (loop-to-fixed-point for adjacent runs, beads-36d6n; hyphen-extended
// siblings untouched, beads-1nvr5) and is idempotent over already-tombstoned text.
func deletedReferenceRewriter(id string) func(string) (string, bool) {
	return domain.DeletedReferenceRewriter(id)
}

// readIssueIDsFromFile reads issue IDs from a file (one per line)
func readIssueIDsFromFile(filename string) ([]string, error) {
	// #nosec G304 - user-provided file path is intentional
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var ids []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ids = append(ids, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

// uniqueStrings removes duplicates from a slice of strings
func uniqueStrings(slice []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(slice))
	for _, s := range slice {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

func init() {
	deleteCmd.Flags().BoolP("force", "f", false, "Actually delete (without this flag, shows preview)")
	deleteCmd.Flags().String("from-file", "", "Read issue IDs from file (one per line)")
	deleteCmd.Flags().Bool("dry-run", false, "Preview what would be deleted without making changes")
	deleteCmd.Flags().Bool("cascade", false, "Recursively delete all dependent issues")
	deleteCmd.ValidArgsFunction = issueIDCompletion
	rootCmd.AddCommand(deleteCmd)
}
