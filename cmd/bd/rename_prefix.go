package main

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/utils"
)

var renamePrefixCmd = &cobra.Command{
	Use:     "rename-prefix <new-prefix>",
	GroupID: "maint",
	Short:   "Rename the issue prefix for all issues in the database",
	Long: `Rename the issue prefix for all issues in the database.
This will update all issue IDs and all text references across all fields.

USE CASES:
- Shortening long prefixes (e.g., 'knowledge-work-' → 'kw-')
- Rebranding project naming conventions
- Consolidating multiple prefixes after database corruption
- Migrating to team naming standards

Prefix validation rules:
- Max length: 8 characters
- Allowed characters: lowercase letters, numbers, hyphens
- Must start with a letter
- Must end with a hyphen (e.g., 'kw-', 'work-')
- Cannot be empty or just a hyphen

Multiple prefix detection and repair:
If issues have multiple prefixes (corrupted database), use --repair to consolidate them.
The --repair flag will rename all issues with incorrect prefixes to the new prefix,
preserving issues that already have the correct prefix.

EXAMPLES:
  bd rename-prefix kw-                # Rename from 'knowledge-work-' to 'kw-'
  bd rename-prefix mtg- --repair      # Consolidate multiple prefixes into 'mtg-'
  bd rename-prefix team- --dry-run    # Preview changes without applying

NOTE: This is a rare operation. Most users never need this command.`,
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("rename-prefix")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		newPrefix := args[0]
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		repair, _ := cmd.Flags().GetBool("repair")

		if !dryRun {
			CheckReadonly("rename-prefix")
		}

		ctx := rootCtx

		if store == nil {
			if err := ensureStoreActive(); err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
		}

		if err := validatePrefix(newPrefix); err != nil {
			return HandleErrorRespectJSON("%v", err)
		}

		oldPrefix, err := store.GetConfig(ctx, "issue_prefix")
		if err != nil || oldPrefix == "" {
			return HandleErrorRespectJSON("failed to get current prefix: %v", err)
		}

		newPrefix = strings.TrimRight(newPrefix, "-")

		issues, err := store.SearchIssues(ctx, "", types.IssueFilter{})
		if err != nil {
			return HandleErrorRespectJSON("failed to list issues: %v", err)
		}

		prefixes := detectPrefixes(issues)

		if len(prefixes) > 1 {
			fmt.Fprintf(os.Stderr, "%s Multiple prefixes detected in database:\n", ui.RenderFail("✗"))
			for prefix, count := range prefixes {
				fmt.Fprintf(os.Stderr, "  - %s: %d issues\n", ui.RenderWarn(prefix), count)
			}
			fmt.Fprintf(os.Stderr, "\n")

			if !repair {
				return HandleErrorWithHint(
					"cannot rename with multiple prefixes. Use --repair to consolidate.",
					fmt.Sprintf("Example: bd rename-prefix %s --repair", newPrefix),
				)
			}

			if err := repairPrefixes(ctx, store, actor, newPrefix, issues, prefixes, dryRun); err != nil {
				return HandleErrorRespectJSON("failed to repair prefixes: %v", err)
			}
			if !dryRun {
				commandDidWrite.Store(true)
			}
			return nil
		}

		if len(prefixes) == 1 && oldPrefix == newPrefix {
			return HandleErrorRespectJSON("new prefix is the same as current prefix: %s", oldPrefix)
		}

		if len(issues) == 0 {
			// beads-rvmpg: under --json emit a structured result, not the
			// human line (else the JSON consumer gets "No issues to rename..."
			// on stdout and no object). Suppress the human print behind !jsonOutput.
			if !jsonOutput {
				fmt.Printf("No issues to rename. Updating prefix to %s\n", newPrefix)
			}
			if !dryRun {
				if err := store.SetConfig(ctx, "issue_prefix", newPrefix); err != nil {
					return HandleErrorRespectJSON("failed to update prefix: %v", err)
				}
				commandDidWrite.Store(true)
			}
			if jsonOutput {
				return outputJSON(map[string]interface{}{
					"old_prefix":   oldPrefix,
					"new_prefix":   newPrefix,
					"issues_count": 0,
					"dry_run":      dryRun,
				})
			}
			return nil
		}

		if dryRun {
			// beads-rvmpg: under --json emit the planned renames as a structured
			// result instead of the human DRY RUN text (which would otherwise be
			// the only thing on stdout, unparseable as JSON).
			if jsonOutput {
				planned := make([]map[string]interface{}, 0, len(issues))
				for _, issue := range issues {
					oldID := fmt.Sprintf("%s-%s", oldPrefix, strings.TrimPrefix(issue.ID, oldPrefix+"-"))
					newID := fmt.Sprintf("%s-%s", newPrefix, strings.TrimPrefix(issue.ID, oldPrefix+"-"))
					planned = append(planned, map[string]interface{}{
						"old_id": oldID,
						"new_id": newID,
					})
				}
				return outputJSON(map[string]interface{}{
					"old_prefix":      oldPrefix,
					"new_prefix":      newPrefix,
					"issues_count":    len(issues),
					"dry_run":         true,
					"planned_renames": planned,
				})
			}
			fmt.Printf("DRY RUN: Would rename %d issues from prefix '%s' to '%s'\n\n", len(issues), oldPrefix, newPrefix)
			fmt.Printf("Sample changes:\n")
			for i, issue := range issues {
				if i >= 5 {
					fmt.Printf("... and %d more issues\n", len(issues)-5)
					break
				}
				oldID := fmt.Sprintf("%s-%s", oldPrefix, strings.TrimPrefix(issue.ID, oldPrefix+"-"))
				newID := fmt.Sprintf("%s-%s", newPrefix, strings.TrimPrefix(issue.ID, oldPrefix+"-"))
				fmt.Printf("  %s -> %s\n", ui.RenderAccent(oldID), ui.RenderAccent(newID))
			}
			return nil
		}

		// beads-qpiw: keep human progress/success text off stdout under --json,
		// else `bd rename-prefix X --json | jq` sees "Renaming...\n{...}" and
		// fails to parse the happy path (ado.go precedent gates prints likewise).
		if !jsonOutput {
			fmt.Printf("Renaming %d issues from prefix '%s' to '%s'...\n", len(issues), oldPrefix, newPrefix)
		}

		if err := renamePrefixInDB(ctx, oldPrefix, newPrefix, issues); err != nil {
			return HandleErrorRespectJSON("failed to rename prefix: %v", err)
		}

		commandDidWrite.Store(true)

		if !jsonOutput {
			fmt.Printf("%s Successfully renamed prefix from %s to %s\n", ui.RenderPass("✓"), ui.RenderAccent(oldPrefix), ui.RenderAccent(newPrefix))
		}

		if jsonOutput {
			result := map[string]interface{}{
				"old_prefix":   oldPrefix,
				"new_prefix":   newPrefix,
				"issues_count": len(issues),
			}
			if eerr := outputJSON(result); eerr != nil {
				return eerr
			}
		}

		return nil
	},
}

func validatePrefix(prefix string) error {
	prefix = strings.TrimRight(prefix, "-")

	if prefix == "" {
		return fmt.Errorf("prefix cannot be empty")
	}

	matched, _ := regexp.MatchString(`^[a-z][a-z0-9-]*$`, prefix)
	if !matched {
		return fmt.Errorf("prefix must start with a lowercase letter and contain only lowercase letters, numbers, and hyphens: %s", prefix)
	}

	if strings.HasPrefix(prefix, "-") || strings.HasSuffix(prefix, "--") {
		return fmt.Errorf("prefix has invalid hyphen placement: %s", prefix)
	}

	return nil
}

// detectPrefixes analyzes all issues and returns a map of prefix -> count
func detectPrefixes(issues []*types.Issue) map[string]int {
	prefixes := make(map[string]int)
	for _, issue := range issues {
		prefix := utils.ExtractIssuePrefix(issue.ID)
		if prefix != "" {
			prefixes[prefix]++
		}
	}
	return prefixes
}

// issueSort is used for sorting issues by prefix and number
type issueSort struct {
	issue  *types.Issue
	prefix string
	number int
}

// repairPrefixes consolidates multiple prefixes into a single target prefix
// Issues with the correct prefix are left unchanged.
// Issues with incorrect prefixes get new hash-based IDs.
func repairPrefixes(ctx context.Context, st storage.DoltStorage, actorName string, targetPrefix string, issues []*types.Issue, prefixes map[string]int, dryRun bool) error {

	// Separate issues into correct and incorrect prefix groups
	var correctIssues []*types.Issue
	var incorrectIssues []issueSort

	for _, issue := range issues {
		prefix := utils.ExtractIssuePrefix(issue.ID)
		number := utils.ExtractIssueNumber(issue.ID)

		if prefix == targetPrefix {
			correctIssues = append(correctIssues, issue)
		} else {
			incorrectIssues = append(incorrectIssues, issueSort{
				issue:  issue,
				prefix: prefix,
				number: number,
			})
		}
	}

	// Sort incorrect issues: first by prefix lexicographically, then by number
	slices.SortFunc(incorrectIssues, func(a, b issueSort) int {
		return cmp.Or(
			cmp.Compare(a.prefix, b.prefix),
			cmp.Compare(a.number, b.number),
		)
	})

	// Build a map of all renames for text replacement using hash IDs
	// Track used IDs to avoid collisions within the batch
	renameMap := make(map[string]string)
	usedIDs := make(map[string]bool)

	// Mark existing correct IDs as used
	for _, issue := range correctIssues {
		usedIDs[issue.ID] = true
	}

	// Generate hash IDs for all incorrect issues
	for _, is := range incorrectIssues {
		newID, err := generateRepairHashID(targetPrefix, is.issue, actorName, usedIDs)
		if err != nil {
			return fmt.Errorf("failed to generate hash ID for %s: %w", is.issue.ID, err)
		}
		renameMap[is.issue.ID] = newID
		usedIDs[newID] = true
	}

	if dryRun {
		// beads-rvmpg: under --json emit a structured preview (same key-set as
		// the applied path + dry_run:true + planned_renames) instead of the
		// human DRY RUN text, which would otherwise be unparseable stdout.
		if jsonOutput {
			planned := make([]map[string]interface{}, 0, len(incorrectIssues))
			for _, is := range incorrectIssues {
				oldID := is.issue.ID
				planned = append(planned, map[string]interface{}{
					"old_id": oldID,
					"new_id": renameMap[oldID],
				})
			}
			return outputJSON(map[string]interface{}{
				"target_prefix":    targetPrefix,
				"prefixes_found":   len(prefixes),
				"issues_repaired":  len(incorrectIssues),
				"issues_unchanged": len(correctIssues),
				"dry_run":          true,
				"planned_renames":  planned,
			})
		}
		fmt.Printf("DRY RUN: Would repair %d issues with incorrect prefixes\n\n", len(incorrectIssues))
		fmt.Printf("Issues with correct prefix (%s): %d\n", ui.RenderAccent(targetPrefix), len(correctIssues))
		fmt.Printf("Issues to repair: %d\n\n", len(incorrectIssues))

		fmt.Printf("Planned renames (showing first 10):\n")
		for i, is := range incorrectIssues {
			if i >= 10 {
				fmt.Printf("... and %d more\n", len(incorrectIssues)-10)
				break
			}
			oldID := is.issue.ID
			newID := renameMap[oldID]
			fmt.Printf("  %s -> %s\n", ui.RenderWarn(oldID), ui.RenderAccent(newID))
		}
		return nil
	}

	// Perform the repairs. beads-qpiw: suppress human progress under --json so
	// the trailing JSON result object is the only thing on stdout.
	if !jsonOutput {
		fmt.Printf("Repairing database with multiple prefixes...\n")
		fmt.Printf("  Issues with correct prefix (%s): %d\n", ui.RenderAccent(targetPrefix), len(correctIssues))
		fmt.Printf("  Issues to repair: %d\n\n", len(incorrectIssues))
	}

	// Pattern to match any issue ID reference in text (both hash and sequential IDs)
	oldPrefixPattern := regexp.MustCompile(`\b[a-z][a-z0-9-]*-[a-z0-9]+\b`)

	// Replace all issue IDs in text fields using the rename map
	replaceFunc := func(match string) string {
		if newID, ok := renameMap[match]; ok {
			return newID
		}
		return match
	}

	// beads-siggb: adapt the batch ReplaceAllStringFunc into the
	// (string) -> (string, changed) shape rewriteCommentRefsInTx expects, so the
	// --repair path rewrites COMMENT bodies the same way renamePrefixInDB (the
	// single-old-prefix path) already does via g8qfo. repairPrefixes rewrote the
	// 5 issue text fields but never visited the comments table, so a comment that
	// referenced any repaired id (a cross-issue ref OR the row's own old id) kept
	// the now-nonexistent id after consolidation — a dangling reference reported
	// with RC=0 and no warning.
	commentRewrite := func(s string) (string, bool) {
		out := oldPrefixPattern.ReplaceAllStringFunc(s, replaceFunc)
		return out, out != s
	}

	// beads-xu7q9: consolidate every incorrect-prefix issue + the prefix config
	// update in ONE transaction. --repair exists to CURE a corrupted
	// multi-prefix DB; running each UpdateIssueID as its own self-committing
	// write meant a mid-loop fault left the DB PARTIALLY renamed — strictly
	// worse than the state --repair was invoked to fix. Rendering the human
	// "Renamed" lines is deferred to after a clean commit so a rolled-back
	// rename is never reported as done. Capture the old→new pairs BEFORE the
	// closure since it mutates is.issue.ID.
	type renamePair struct{ oldID, newID string }
	renamed := make([]renamePair, 0, len(incorrectIssues))
	for _, is := range incorrectIssues {
		renamed = append(renamed, renamePair{oldID: is.issue.ID, newID: renameMap[is.issue.ID]})
	}

	err := st.RunInTransaction(ctx, fmt.Sprintf("bd: repair prefixes -> %s", targetPrefix), func(tx storage.Transaction) error {
		for _, is := range incorrectIssues {
			oldID := is.issue.ID
			newID := renameMap[oldID]

			// Apply text replacements in all issue fields
			issue := is.issue
			issue.ID = newID

			issue.Title = oldPrefixPattern.ReplaceAllStringFunc(issue.Title, replaceFunc)
			issue.Description = oldPrefixPattern.ReplaceAllStringFunc(issue.Description, replaceFunc)
			if issue.Design != "" {
				issue.Design = oldPrefixPattern.ReplaceAllStringFunc(issue.Design, replaceFunc)
			}
			if issue.AcceptanceCriteria != "" {
				issue.AcceptanceCriteria = oldPrefixPattern.ReplaceAllStringFunc(issue.AcceptanceCriteria, replaceFunc)
			}
			if issue.Notes != "" {
				issue.Notes = oldPrefixPattern.ReplaceAllStringFunc(issue.Notes, replaceFunc)
			}

			// Update the issue in the database
			if err := tx.UpdateIssueID(ctx, oldID, newID, issue, actorName); err != nil {
				return fmt.Errorf("failed to update issue %s -> %s: %w", oldID, newID, err)
			}

			// beads-siggb: comments are re-keyed to newID by the UpdateIssueID FK
			// cascade, so fetch by newID and rewrite any repaired-id ref in the
			// body — the same comment-body pass renamePrefixInDB runs (g8qfo).
			if err := rewriteCommentRefsInTx(ctx, tx, newID, commentRewrite); err != nil {
				return err
			}
		}

		// Set the new prefix in config
		if err := tx.SetConfig(ctx, "issue_prefix", targetPrefix); err != nil {
			return fmt.Errorf("failed to update config: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}

	if !jsonOutput {
		for _, r := range renamed {
			fmt.Printf("  Renamed %s -> %s\n", ui.RenderWarn(r.oldID), ui.RenderAccent(r.newID))
		}
	}

	if !jsonOutput {
		fmt.Printf("\n%s Successfully consolidated %d prefixes into %s\n",
			ui.RenderPass("✓"), len(prefixes), ui.RenderAccent(targetPrefix))
		fmt.Printf("  %d issues repaired, %d issues unchanged\n", len(incorrectIssues), len(correctIssues))
	}

	if jsonOutput {
		result := map[string]interface{}{
			"target_prefix":    targetPrefix,
			"prefixes_found":   len(prefixes),
			"issues_repaired":  len(incorrectIssues),
			"issues_unchanged": len(correctIssues),
		}
		_ = outputJSON(result)
	}

	return nil
}

func renamePrefixInDB(ctx context.Context, oldPrefix, newPrefix string, issues []*types.Issue) error {
	// beads-xu7q9: rename every issue + rewrite its comment refs + update the
	// prefix config in ONE transaction, so a mid-loop DB fault rolls the whole
	// rename back instead of leaving a half-renamed mixed-prefix DB (which was
	// the pre-fix hazard the old NOTE here flagged). store.UpdateIssueID /
	// UpdateCommentText / SetConfig each self-commit outside a tx, so the loop
	// below runs against the tx handle instead of the store.

	// beads-kzj4s: the suffix must be base36 alphanumeric, not digits-only.
	// bd IDs are base36 hashes (e.g. oldpref-16f), so a `-(\d+)` suffix silently
	// skipped every letter-bearing ref in desc/design/notes/AC/comment bodies,
	// leaving dangling old-prefix refs after rename-prefix. Mirror the repair
	// path's alnum suffix (rename_prefix.go:355), keeping the oldPrefix anchor so
	// only refs to THIS prefix are rewritten. This same pattern also drives the
	// g8qfo comment-body rewrite below, which was inert for alnum IDs until now.
	oldPrefixPattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(oldPrefix) + `-[a-z0-9]+\b`)

	replaceFunc := func(match string) string {
		return strings.Replace(match, oldPrefix+"-", newPrefix+"-", 1)
	}

	// beads-g8qfo: adapt the prefix ReplaceAllStringFunc into the (string) ->
	// (string, changed) shape rewriteCommentRefs expects, so comment bodies get
	// the same prefix rewrite as the 5 issue fields — the comments table was
	// never visited before, silently leaving dangling old-prefix refs.
	prefixRewrite := func(s string) (string, bool) {
		out := oldPrefixPattern.ReplaceAllStringFunc(s, replaceFunc)
		return out, out != s
	}

	commitMsg := fmt.Sprintf("bd: rename prefix %s -> %s", oldPrefix, newPrefix)
	return store.RunInTransaction(ctx, commitMsg, func(tx storage.Transaction) error {
		for _, issue := range issues {
			oldID := issue.ID
			numPart := strings.TrimPrefix(oldID, oldPrefix+"-")
			newID := fmt.Sprintf("%s-%s", newPrefix, numPart)

			issue.ID = newID

			issue.Title = oldPrefixPattern.ReplaceAllStringFunc(issue.Title, replaceFunc)
			issue.Description = oldPrefixPattern.ReplaceAllStringFunc(issue.Description, replaceFunc)
			if issue.Design != "" {
				issue.Design = oldPrefixPattern.ReplaceAllStringFunc(issue.Design, replaceFunc)
			}
			if issue.AcceptanceCriteria != "" {
				issue.AcceptanceCriteria = oldPrefixPattern.ReplaceAllStringFunc(issue.AcceptanceCriteria, replaceFunc)
			}
			if issue.Notes != "" {
				issue.Notes = oldPrefixPattern.ReplaceAllStringFunc(issue.Notes, replaceFunc)
			}

			if err := tx.UpdateIssueID(ctx, oldID, newID, issue, actor); err != nil {
				return fmt.Errorf("failed to update issue %s: %w", oldID, err)
			}

			// Comments are re-keyed to newID by the UpdateIssueID FK cascade, so
			// fetch by newID and rewrite any old-prefix ref in the body.
			if err := rewriteCommentRefsInTx(ctx, tx, newID, prefixRewrite); err != nil {
				return err
			}
		}

		if err := tx.SetConfig(ctx, "issue_prefix", newPrefix); err != nil {
			return fmt.Errorf("failed to update config: %w", err)
		}
		return nil
	})
}

// generateRepairHashID generates a hash-based ID for an issue during repair.
// Uses content hashing and checks usedIDs for batch collision avoidance.
func generateRepairHashID(prefix string, issue *types.Issue, actor string, usedIDs map[string]bool) (string, error) {
	// Generate a hash ID from issue content (same approach as generateHashIDForIssue)
	content := fmt.Sprintf("%s|%s|%s|%d|%d",
		issue.Title,
		issue.Description,
		actor,
		issue.CreatedAt.UnixNano(),
		0, // nonce
	)
	h := sha256.Sum256([]byte(content))
	shortHash := hex.EncodeToString(h[:4]) // 4 bytes = 8 hex chars
	newID := fmt.Sprintf("%s-%s", prefix, shortHash)

	// Check if this ID was already used in this batch
	// If so, we need to generate a new one with a different nonce
	attempts := 0
	for usedIDs[newID] && attempts < 100 {
		attempts++
		content = fmt.Sprintf("%s|%s|%s|%d|%d",
			issue.Title,
			issue.Description,
			actor,
			issue.CreatedAt.UnixNano(),
			attempts,
		)
		h = sha256.Sum256([]byte(content))
		shortHash = hex.EncodeToString(h[:4])
		newID = fmt.Sprintf("%s-%s", prefix, shortHash)
	}

	if usedIDs[newID] {
		return "", fmt.Errorf("failed to generate unique ID after %d attempts", attempts)
	}

	return newID, nil
}

func init() {
	renamePrefixCmd.Flags().Bool("dry-run", false, "Preview changes without applying them")
	renamePrefixCmd.Flags().Bool("repair", false, "Repair database with multiple prefixes by consolidating them")
	rootCmd.AddCommand(renamePrefixCmd)
}
