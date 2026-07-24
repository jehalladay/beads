package main

import (
	"context"
	"fmt"

	"github.com/steveyegge/beads/internal/storage/uow"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/validation"
)

// runRenameProxiedServer renames an issue ID via the proxied unit-of-work
// stack, for hub-connected crew where the global `store` is nil (beads-lh54,
// fszd/aocj umbrella). UpdateIssueID lived only on DoltStore, not the domain
// use-case, so this is an interface-extension leg: RenameIssueID was added to
// IssueUseCase (backed by issueops.UpdateIssueIDInTx widened from *sql.Tx to
// DBTX). It mirrors the direct path (cmd/bd/rename.go): existence checks
// (old exists / new free), rename, best-effort text-reference rewrite across
// all issues, commit, --json payload.
func runRenameProxiedServer(ctx context.Context, oldID, newID string, force bool) error {
	if uowProvider == nil {
		FatalError("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("open unit of work: %v", err)
	}
	defer uw.Close(ctx)

	// beads-c3igh: enforce the DB-prefix invariant that `bd create --id` enforces
	// (create_proxied_server.go) and rename's help promises. The format regex in
	// the caller (rename.go) accepts ANY prefix; without this a rename could
	// inject an off-prefix, unroutable bead. Live DB prefix stays authoritative;
	// a disagreeing config.yaml prefix folds into the allowed-list (beads-xevo).
	// --force is the deliberate override (parity with create --id --force).
	cctx, cerr := uw.ConfigUseCase().LoadCreateContext(ctx)
	if cerr != nil {
		return HandleErrorRespectJSON("load config context: %v", cerr)
	}
	dbPrefix, allowed := resolvePrefixValidation(cctx.IssuePrefix, cctx.AllowedPrefixes)
	if verr := validation.ValidateIDPrefixAllowed(newID, dbPrefix, allowed, force); verr != nil {
		return HandleErrorRespectJSON("%v", verr)
	}

	issueUC := uw.IssueUseCase()

	oldIssue, err := issueUC.GetIssue(ctx, oldID)
	if err != nil || oldIssue == nil {
		return HandleErrorRespectJSON("issue %s not found", oldID)
	}

	if existing, gerr := issueUC.GetIssue(ctx, newID); gerr == nil && existing != nil {
		return HandleErrorRespectJSON("issue %s already exists", newID)
	}

	oldIssue.ID = newID
	if err := issueUC.RenameIssueID(ctx, oldID, newID, oldIssue, actor); err != nil {
		return HandleErrorRespectJSON("failed to rename issue: %v", err)
	}

	// beads-kjuye: a ref-rewrite failure must ROLL THE RENAME BACK, not commit it
	// with a soft warning. The rename + cross-issue reference rewrite are one
	// composite write staged on the single UOW; returning here (before Commit)
	// leaves nothing committed — the deferred uw.Close() calls
	// RollbackUnlessCommitted (uow.go), so the OLD id keeps resolving and no
	// dangling old-id reference survives. This is true parity with the atomic
	// DIRECT path (rename.go: one store.RunInTransaction whose ref-rewrite error
	// rolls the rename back — beads-uorhi). Previously this swallowed the error
	// into refWarning and committed the rename + an arbitrary partial suffix of
	// ref-rewrites at RC=0, re-introducing the exact pre-uorhi dangling-ref bug on
	// the proxied path.
	rewrittenIDs, err := updateReferencesInAllIssuesProxied(ctx, uw, oldID, newID)
	if err != nil {
		return HandleErrorRespectJSON("failed to update references (rename rolled back): %v", err)
	}

	// beads-rlthr: capture the post-rewrite snapshots of every issue whose BODY
	// fields were ref-rewritten, BEFORE Commit closes the UOW, so the on_update
	// hook can fire for each AFTER the commit — the proxied twin of the direct
	// path, where those same per-issue field rewrites go through
	// hookTrackingTransaction.UpdateIssue and fire EventUpdate post-commit
	// (hook_decorator.go:501). The proxied path routed the rewrites through the
	// undecorated issueUC.UpdateIssue, so a hub-connected (store==nil) crew's
	// on_update automation silently never ran for issues whose bodies a rename
	// rewrote. Sibling of beads-fs73t (gate) / beads-elq6a (undefer). CLEAN-NEG:
	// the renamed row itself (UpdateIssueID is undecorated on both paths) and
	// comment-body rewrites (UpdateCommentText fires no hook on either path) — so
	// only the field-rewrite ids are collected.
	updateSnapshots := make([]*types.Issue, 0, len(rewrittenIDs))
	for _, id := range rewrittenIDs {
		if snap := captureProxiedHookSnapshot(ctx, uw, id, false); snap != nil {
			updateSnapshots = append(updateSnapshots, snap)
		}
	}

	if err := uw.Commit(ctx, fmt.Sprintf("bd: rename %s -> %s", oldID, newID)); err != nil && !isDoltNothingToCommit(err) {
		return HandleErrorRespectJSON("failed to commit: %v", err)
	}

	// beads-rlthr: fire on_update for the ref-rewritten issues after the commit
	// lands (best-effort, matching the direct decorator's fire-and-forget and the
	// existing proxied update path). A post-commit fire is required — a pre-commit
	// call would be rolled back by the deferred uw.Close (same ordering as
	// applyUpdateProxiedOne / the proxied create-hook path).
	fireProxiedUpdateSnapshots(ctx, updateSnapshots...)

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

// updateReferencesInAllIssuesProxied is the proxied-UOW mirror of
// updateReferencesInAllIssues (cmd/bd/rename.go): rewrite word-boundary text
// references to oldID across every issue's title/description/design/notes/
// acceptance_criteria via the use-case, best-effort.
//
// beads-rlthr: returns the IDs of issues whose BODY FIELDS were rewritten (the
// len(updates)>0 rows), so the caller can fire on_update for each after commit —
// the proxied twin of the direct path's per-issue tx.UpdateIssue → EventUpdate.
// Comment-body-only rewrites are NOT included (UpdateIssueCommentText is
// undecorated on both paths → fires no hook — CLEAN-NEG).
func updateReferencesInAllIssuesProxied(ctx context.Context, uw uow.UnitOfWork, oldID, newID string) ([]string, error) {
	issueUC := uw.IssueUseCase()
	page, err := issueUC.SearchIssues(ctx, "", types.IssueFilter{})
	if err != nil {
		return nil, fmt.Errorf("failed to list issues: %w", err)
	}

	var rewrittenIDs []string

	// beads-1nvr5: share the direct path's id-charclass boundary rewriter so
	// the two rename paths can never diverge. The old `\b`...`\b` matched inside
	// hyphen-extended sibling ids (bd-abc-2), corrupting references to distinct
	// issues.
	rewrite := idReferenceRewriter(oldID, newID)

	// beads-k0yri: do NOT skip the renamed row — its own body was re-written
	// verbatim by RenameIssueID above, so a self-reference to the old id would
	// otherwise stay dangling (twin of the direct-path fix in rename.go). The
	// rewriter maps oldID->newID with an id-char-bounded newID token, so
	// visiting the already-renamed row is idempotent.
	for _, issue := range page.Items {
		updates := make(map[string]any)
		if v, ok := rewrite(issue.Title); ok {
			updates["title"] = v
		}
		if v, ok := rewrite(issue.Description); ok {
			updates["description"] = v
		}
		if v, ok := rewrite(issue.Design); ok {
			updates["design"] = v
		}
		if v, ok := rewrite(issue.Notes); ok {
			updates["notes"] = v
		}
		if v, ok := rewrite(issue.AcceptanceCriteria); ok {
			updates["acceptance_criteria"] = v
		}

		if len(updates) > 0 {
			if err := issueUC.UpdateIssue(ctx, issue.ID, updates, actor); err != nil {
				return nil, fmt.Errorf("failed to update references in %s: %w", issue.ID, err)
			}
			// beads-rlthr: this field rewrite is the proxied analog of the direct
			// path's hook-decorated tx.UpdateIssue — record it so the caller fires
			// on_update for this issue after the commit lands.
			rewrittenIDs = append(rewrittenIDs, issue.ID)
		}

		// beads-g8qfo: rewrite id refs inside comment bodies too — the comments
		// table was never visited by the reference sweep, silently leaving
		// dangling old-id refs. Mirrors the direct path (rename.go
		// rewriteCommentRefs). Only permanent issues route through the proxied
		// rename, so the issue comment table (not wisp_comments) is correct.
		comments, cerr := uw.CommentUseCase().GetCommentsForIssue(ctx, issue.ID)
		if cerr != nil {
			return nil, fmt.Errorf("failed to read comments for %s: %w", issue.ID, cerr)
		}
		for _, c := range comments {
			if v, ok := rewrite(c.Text); ok {
				if err := uw.CommentUseCase().UpdateIssueCommentText(ctx, c.ID, v); err != nil {
					return nil, fmt.Errorf("failed to update comment reference in %s: %w", issue.ID, err)
				}
			}
		}
	}

	return rewrittenIDs, nil
}
