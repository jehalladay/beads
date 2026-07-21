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
	"github.com/steveyegge/beads/internal/validation"
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
	// beads-c3igh: --force is the deliberate-override escape hatch for the
	// DB-prefix guard, mirroring `bd create --id`'s --force (create.go). Without
	// it a rename could inject an off-prefix, effectively-unroutable bead.
	renameCmd.Flags().Bool("force", false, "Allow renaming to an ID whose prefix is not a valid/allowed prefix for this database")
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

	force, _ := cmd.Flags().GetBool("force")

	ctx := context.Background()

	// In proxied-server mode the global `store` is nil (main.go PersistentPreRun
	// returns before newDoltStore), so the store.UpdateIssueID path below would
	// fail with "storage is nil". Route through the proxied UOW stack instead
	// (beads-lh54, fszd/aocj umbrella) — UpdateIssueID was previously only on
	// DoltStore, now also on the domain IssueUseCase (RenameIssueID).
	if usesProxiedServer() {
		return runRenameProxiedServer(ctx, oldID, newID, force)
	}

	if err := ensureStoreActive(); err != nil {
		return HandleErrorRespectJSON("failed to get storage: %v", err)
	}

	// beads-c3igh: enforce the DB-prefix invariant that `bd create --id` enforces
	// (create.go) and that rename's own help promises ("must use a valid prefix
	// for this database"). The format regex above accepts ANY prefix; without
	// this a rename could inject an off-prefix, unroutable bead. The live DB
	// prefix stays authoritative; a disagreeing config.yaml prefix is folded into
	// the allowed-list (beads-xevo). --force is the deliberate override.
	liveDBPrefix, _ := store.GetConfig(ctx, "issue_prefix")
	allowedFromDB, _ := store.GetConfig(ctx, "allowed_prefixes")
	dbPrefix, allowedPrefixes := resolvePrefixValidation(liveDBPrefix, allowedFromDB)
	if err := validation.ValidateIDPrefixAllowed(newID, dbPrefix, allowedPrefixes, force); err != nil {
		return HandleErrorRespectJSON("%v", err)
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

	// beads-uorhi: the rename + cross-issue reference rewrite is a composite
	// write. Previously it ran as a self-committing store.UpdateIssueID followed
	// by a separate self-committing ref-rewrite loop, so a mid-sweep failure
	// (a per-issue UpdateIssue error, process death, or ctx cancel) left the id
	// already renamed while an arbitrary suffix of the issue set still textually
	// referenced the now-nonexistent old id — dangling refs, reported to the
	// operator only as a soft warning with RC=0. Wrap both in ONE transaction,
	// mirroring the atomic proxied UOW twin (rename_proxied_server.go: rename +
	// updateReferencesInAllIssuesProxied staged onto one uw + single Commit) and
	// the ary2n/zcq86 direct-leg-to-in-tx precedent. All-or-nothing: a ref-rewrite
	// failure now rolls back the rename too, so old id keeps resolving.
	if err := store.RunInTransaction(ctx, fmt.Sprintf("bd: rename %s -> %s", oldID, newID), func(tx storage.Transaction) error {
		if err := tx.UpdateIssueID(ctx, oldID, newID, oldIssue, actor); err != nil {
			return fmt.Errorf("failed to rename issue: %w", err)
		}
		if err := updateReferencesInAllIssuesTx(ctx, tx, oldID, newID, actor); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return HandleErrorRespectJSON("%v", err)
	}

	commandDidWrite.Store(true)

	if jsonOutput {
		out := map[string]interface{}{
			"renamed": true,
			"old_id":  oldID,
			"new_id":  newID,
		}
		return outputJSON(out)
	}

	fmt.Printf("Renamed %s -> %s\n", ui.RenderWarn(oldID), ui.RenderAccent(newID))

	return nil
}

// idReferenceRewriter returns a rewriter that replaces standalone text
// references to oldID with newID, treating an issue ID as a full token bounded
// by a NON-ID character.
//
// beads-1nvr5: the old pattern was `\b` + oldID + `\b`. Go's \b word boundary
// treats '-' as a NON-word char, so `\bbd-abc\b` matches at the hyphen INSIDE a
// hyphen-extended sibling id like bd-abc-2 — silently rewriting the bd-abc
// prefix of a DIFFERENT, unrelated issue (bd-abc-2 -> bd-xyz-2) and corrupting
// a reference to it. The rename ID regex (rename.go idPattern) and the proven
// in-tree pattern in cmd/bd/delete.go define an id token by the charclass
// [A-Za-z0-9_-], not \w. We match the same: the id is a full token only when
// the surrounding chars are not id-continuation chars, and we re-emit those
// delimiters ($1/$3). This is shared by the direct and proxied rename paths so
// the two regexes can never diverge.
func idReferenceRewriter(oldID, newID string) func(string) (string, bool) {
	re := regexp.MustCompile(`(^|[^A-Za-z0-9_-])(` + regexp.QuoteMeta(oldID) + `)($|[^A-Za-z0-9_-])`)
	repl := `${1}` + newID + `${3}`
	return func(s string) (string, bool) {
		if !re.MatchString(s) {
			return s, false
		}
		// Loop until stable: a match CONSUMES its trailing delimiter, which is
		// also the leading delimiter of an immediately-adjacent second
		// reference ("bd-abc bd-abc" shares one space), so a single
		// ReplaceAllString pass would rewrite only the first of a run. Go RE2
		// has no lookbehind to make the boundary zero-width, so we re-scan; the
		// re-emitted delimiters ($1/$3) survive each pass and the newID token is
		// itself id-char-bounded, so it can never re-match — the loop always
		// terminates.
		out := s
		for {
			next := re.ReplaceAllString(out, repl)
			if next == out {
				return out, true
			}
			out = next
		}
	}
}

// rewriteCommentRefs applies rewrite to every comment body on issueID and
// persists the ones that changed (beads-g8qfo). Shared by the singular rename
// (updateReferencesInAllIssues) and rename-prefix sweeps so the comment-visit
// logic can never drift between them.
func rewriteCommentRefs(ctx context.Context, store storage.DoltStorage, issueID string, rewrite func(string) (string, bool)) error {
	comments, err := store.GetIssueComments(ctx, issueID)
	if err != nil {
		return fmt.Errorf("failed to read comments for %s: %w", issueID, err)
	}
	for _, c := range comments {
		if v, ok := rewrite(c.Text); ok {
			if err := store.UpdateCommentText(ctx, issueID, c.ID, v); err != nil {
				return fmt.Errorf("failed to update comment reference in %s: %w", issueID, err)
			}
		}
	}
	return nil
}

// updateReferencesInAllIssuesTx is the transaction-scoped mirror of
// updateReferencesInAllIssues (beads-uorhi): the identical field + comment-body
// reference rewrite, but issued against a storage.Transaction so it commits in
// the SAME unit as the id rename. Any per-issue error returns before the tx
// commits, rolling the rename back (no dangling refs) — matching the atomic
// proxied twin (updateReferencesInAllIssuesProxied). The rewriter and visited
// fields are kept identical to the store version so the two direct paths (and
// the proxied path via idReferenceRewriter) can never diverge.
func updateReferencesInAllIssuesTx(ctx context.Context, tx storage.Transaction, oldID, newID, actor string) error {
	issues, err := tx.SearchIssues(ctx, "", types.IssueFilter{})
	if err != nil {
		return fmt.Errorf("failed to list issues: %w", err)
	}

	rewrite := idReferenceRewriter(oldID, newID)

	for _, issue := range issues {
		if issue.ID == newID {
			continue // Skip the renamed issue itself
		}

		updated := false
		updates := make(map[string]interface{})

		if v, ok := rewrite(issue.Title); ok {
			updates["title"] = v
			updated = true
		}
		if v, ok := rewrite(issue.Description); ok {
			updates["description"] = v
			updated = true
		}
		if v, ok := rewrite(issue.Design); ok {
			updates["design"] = v
			updated = true
		}
		if v, ok := rewrite(issue.Notes); ok {
			updates["notes"] = v
			updated = true
		}
		if v, ok := rewrite(issue.AcceptanceCriteria); ok {
			updates["acceptance_criteria"] = v
			updated = true
		}

		if updated {
			if err := tx.UpdateIssue(ctx, issue.ID, updates, actor); err != nil {
				return fmt.Errorf("failed to update references in %s: %w", issue.ID, err)
			}
		}

		// beads-g8qfo: comment bodies are user-authored ref sites too.
		// (Uses rewriteCommentRefsInTx, the shared tx comment-rewrite helper
		// landed by beads-xu7q9 — identical logic to what beads-uorhi needs.)
		if err := rewriteCommentRefsInTx(ctx, tx, issue.ID, rewrite); err != nil {
			return err
		}
	}

	return nil
}

// rewriteCommentRefsInTx is the transactional twin of rewriteCommentRefs: it
// reads + rewrites comment bodies against a storage.Transaction so the rewrites
// commit atomically with the enclosing rename (beads-xu7q9). Kept beside
// rewriteCommentRefs so the comment-visit logic stays identical between the
// tx and non-tx paths.
func rewriteCommentRefsInTx(ctx context.Context, tx storage.Transaction, issueID string, rewrite func(string) (string, bool)) error {
	comments, err := tx.GetIssueComments(ctx, issueID)
	if err != nil {
		return fmt.Errorf("failed to read comments for %s: %w", issueID, err)
	}
	for _, c := range comments {
		if v, ok := rewrite(c.Text); ok {
			if err := tx.UpdateCommentText(ctx, issueID, c.ID, v); err != nil {
				return fmt.Errorf("failed to update comment reference in %s: %w", issueID, err)
			}
		}
	}
	return nil
}
