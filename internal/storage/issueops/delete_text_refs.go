package issueops

import (
	"context"
	"database/sql"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/types"
)

// RewriteTextReferencesToDeletedInTx tombstones every LIVE reference to an
// about-to-be-deleted id in the surviving issues connected to it, matching what
// `bd delete` (single) does. It rewrites the 5 text fields (title/description/
// notes/design/acceptance_criteria) AND comment bodies, using the shared,
// idempotent domain.DeletedReferenceRewriter.
//
// beads-rb00b: the tombstone pass previously lived only in cmd/bd/delete.go and
// the domain use-case; every bulk-delete path (bd gc decay, bd purge, bd prune,
// bd mol burn/squash, cook) routes through DeleteIssueInTx / DeleteIssuesInTx and
// so left dangling live references to deleted issues. Hoisting the pass here — the
// shared storage chokepoint — makes ALL delete paths tombstone uniformly.
//
// MUST be called BEFORE the issue rows are deleted so the dependency edges used to
// find connected neighbors still exist. Callers pass every id being deleted in
// this operation (regular + wisp) so a survivor is never counted as a neighbor of
// itself. The rewriter is idempotent, so a higher layer that already tombstoned
// (cmd single/batch) is a safe no-op here.
//
// Returns the number of surviving issues actually mutated. actor is the update
// author (defaults to "bd" when empty, matching other issueops write paths).
func RewriteTextReferencesToDeletedInTx(
	ctx context.Context, tx *sql.Tx, deletedIDs []string, actor string,
) (int, error) {
	if len(deletedIDs) == 0 {
		return 0, nil
	}
	if actor == "" {
		actor = "bd"
	}

	deletedSet := make(map[string]bool, len(deletedIDs))
	for _, id := range deletedIDs {
		deletedSet[id] = true
	}

	// Collect the surviving neighbors (issues linked to any deleted id in either
	// direction) once, keyed by id so a neighbor connected to several deleted ids
	// is visited a single time.
	neighbors := make(map[string]*types.Issue)
	for _, id := range deletedIDs {
		deps, err := GetDependenciesInTx(ctx, tx, id)
		if err != nil {
			return 0, err
		}
		dependents, err := GetDependentsInTx(ctx, tx, id)
		if err != nil {
			return 0, err
		}
		for _, n := range append(deps, dependents...) {
			if n == nil || deletedSet[n.ID] {
				continue
			}
			if _, seen := neighbors[n.ID]; !seen {
				neighbors[n.ID] = n
			}
		}
	}
	if len(neighbors) == 0 {
		return 0, nil
	}

	touched := make(map[string]bool)
	for _, id := range deletedIDs {
		rewrite := domain.DeletedReferenceRewriter(id)
		for connID, conn := range neighbors {
			updates := map[string]interface{}{}
			if v, ok := rewrite(conn.Title); ok {
				updates["title"] = v
			}
			if v, ok := rewrite(conn.Description); ok {
				updates["description"] = v
			}
			if conn.Notes != "" {
				if v, ok := rewrite(conn.Notes); ok {
					updates["notes"] = v
				}
			}
			if conn.Design != "" {
				if v, ok := rewrite(conn.Design); ok {
					updates["design"] = v
				}
			}
			if conn.AcceptanceCriteria != "" {
				if v, ok := rewrite(conn.AcceptanceCriteria); ok {
					updates["acceptance_criteria"] = v
				}
			}

			// Comment bodies (beads-au6dv twin): a reference may live only in a
			// comment. Rewrite matching comment bodies independently of the field
			// updates.
			comments, cerr := GetIssueCommentsInTx(ctx, tx, connID)
			if cerr != nil {
				return len(touched), cerr
			}
			for _, c := range comments {
				if v, ok := rewrite(c.Text); ok {
					if err := UpdateCommentTextInTx(ctx, tx, connID, c.ID, v); err != nil {
						return len(touched), err
					}
					touched[connID] = true
				}
			}

			if len(updates) == 0 {
				continue
			}
			if _, err := UpdateIssueInTx(ctx, tx, connID, updates, actor); err != nil {
				return len(touched), err
			}
			touched[connID] = true

			// Mirror the rewritten values back onto the in-memory neighbor so a
			// later deletedID pass in this loop sees the already-tombstoned text
			// (multi-ID correctness; the rewriter is idempotent regardless, but
			// this avoids a redundant UPDATE).
			if title, ok := updates["title"].(string); ok {
				conn.Title = title
			}
			if desc, ok := updates["description"].(string); ok {
				conn.Description = desc
			}
			if notes, ok := updates["notes"].(string); ok {
				conn.Notes = notes
			}
			if design, ok := updates["design"].(string); ok {
				conn.Design = design
			}
			if ac, ok := updates["acceptance_criteria"].(string); ok {
				conn.AcceptanceCriteria = ac
			}
		}
	}
	return len(touched), nil
}
