package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

var renameCmd = &cobra.Command{
	Use:   "rename <old-id> <new-id>",
	Short: "Rename an issue ID",
	Long: `Rename an issue from one ID to another.

This updates:
- The issue's primary ID
- All references in other issues (descriptions, titles, notes, etc.)
- Dependencies pointing to/from this issue
- Labels, comments, and events

Examples:
  bd rename bd-w382l bd-dolt     # Rename to memorable ID
  bd rename gt-abc123 gt-auth    # Use descriptive ID

Note: The new ID must use a valid prefix for this database.`,
	Args:          cobra.ExactArgs(2),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runRename,
}

func init() {
	rootCmd.AddCommand(renameCmd)
}

func runRename(cmd *cobra.Command, args []string) error {
	evt := metrics.NewCommandEvent("rename")
	defer func() {
		if c := metrics.Global(); c != nil {
			c.CloseEventAndAdd(evt)
		}
	}()

	oldID := args[0]
	newID := args[1]

	// beads-s7ey: --json is a global persistent flag, so `bd rename --json` is
	// accepted — but this command previously emitted plain text on BOTH success
	// and error, ignoring the flag. Route errors through HandleErrorRespectJSON
	// (emits {"error":...} under --json) and print a JSON success payload.
	if oldID == newID {
		return HandleErrorRespectJSON("old and new IDs are the same")
	}

	idPattern := regexp.MustCompile(`^[a-z]+-[a-zA-Z0-9._-]+$`)
	if !idPattern.MatchString(newID) {
		return HandleErrorRespectJSON("invalid new ID format %q: must be prefix-suffix (e.g., bd-dolt)", newID)
	}

	ctx := context.Background()

	// In proxied-server mode the global `store` is nil (main.go PersistentPreRun
	// returns before newDoltStore), so the store.UpdateIssueID path below would
	// fail with "storage is nil". Route through the proxied UOW stack instead
	// (beads-lh54, fszd/aocj umbrella) — UpdateIssueID was previously only on
	// DoltStore, now also on the domain IssueUseCase (RenameIssueID).
	if usesProxiedServer() {
		return runRenameProxiedServer(ctx, oldID, newID)
	}

	if err := ensureStoreActive(); err != nil {
		return HandleErrorRespectJSON("failed to get storage: %v", err)
	}

	oldIssue, err := store.GetIssue(ctx, oldID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return HandleErrorRespectJSON("issue %s not found", oldID)
		}
		return HandleErrorRespectJSON("failed to get issue %s: %v", oldID, err)
	}

	_, err = store.GetIssue(ctx, newID)
	if err == nil {
		return HandleErrorRespectJSON("issue %s already exists", newID)
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return HandleErrorRespectJSON("failed to check for existing issue: %v", err)
	}

	oldIssue.ID = newID
	actor := getActorWithGit()
	if err := store.UpdateIssueID(ctx, oldID, newID, oldIssue, actor); err != nil {
		return HandleErrorRespectJSON("failed to rename issue: %v", err)
	}

	refWarning := ""
	if err := updateReferencesInAllIssues(ctx, store, oldID, newID, actor); err != nil {
		refWarning = err.Error()
		if !jsonOutput {
			fmt.Printf("Warning: failed to update some references: %v\n", err)
		}
	}

	commandDidWrite.Store(true)

	if jsonOutput {
		out := map[string]interface{}{
			"renamed": true,
			"old_id":  oldID,
			"new_id":  newID,
		}
		if refWarning != "" {
			out["ref_update_warning"] = refWarning
		}
		return outputJSON(out)
	}

	fmt.Printf("Renamed %s -> %s\n", ui.RenderWarn(oldID), ui.RenderAccent(newID))

	return nil
}

// updateReferencesInAllIssues updates text references to the old ID in all issues
func updateReferencesInAllIssues(ctx context.Context, store storage.DoltStorage, oldID, newID, actor string) error {
	// Get all issues
	issues, err := store.SearchIssues(ctx, "", types.IssueFilter{})
	if err != nil {
		return fmt.Errorf("failed to list issues: %w", err)
	}

	// Pattern to match the old ID as a word boundary
	oldPattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(oldID) + `\b`)

	for _, issue := range issues {
		if issue.ID == newID {
			continue // Skip the renamed issue itself
		}

		updated := false
		updates := make(map[string]interface{})

		// Check and update each text field
		if oldPattern.MatchString(issue.Title) {
			updates["title"] = oldPattern.ReplaceAllString(issue.Title, newID)
			updated = true
		}
		if oldPattern.MatchString(issue.Description) {
			updates["description"] = oldPattern.ReplaceAllString(issue.Description, newID)
			updated = true
		}
		if oldPattern.MatchString(issue.Design) {
			updates["design"] = oldPattern.ReplaceAllString(issue.Design, newID)
			updated = true
		}
		if oldPattern.MatchString(issue.Notes) {
			updates["notes"] = oldPattern.ReplaceAllString(issue.Notes, newID)
			updated = true
		}
		if oldPattern.MatchString(issue.AcceptanceCriteria) {
			updates["acceptance_criteria"] = oldPattern.ReplaceAllString(issue.AcceptanceCriteria, newID)
			updated = true
		}

		if updated {
			if err := store.UpdateIssue(ctx, issue.ID, updates, actor); err != nil {
				return fmt.Errorf("failed to update references in %s: %w", issue.ID, err)
			}
		}
	}

	return nil
}
