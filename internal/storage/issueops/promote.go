package issueops

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"github.com/steveyegge/beads/internal/storage"
)

// PromoteFromEphemeralInTx promotes a wisp to a permanent issue.
//
// Writes are split across two transactions so the regular-side inserts can
// commit independently of the wisp-side deletes:
//   - regularTx: receives the inserts into issues, labels, dependencies,
//     events, comments, and child_counters.
//   - ignoredTx: receives the wisp lookup reads and the deletes from
//     wisps and wisp_* auxiliary tables.
//
// The cross-table INSERT...SELECT statements run on regularTx; their SELECT
// from wisp_* reads previously-committed wisp rows, which are visible to
// regularTx even though the deletes on ignoredTx are not yet committed.
//
//nolint:gosec // G201: table names are hardcoded constants
func PromoteFromEphemeralInTx(ctx context.Context, regularTx, ignoredTx *sql.Tx, id string, actor string) error {
	// Verify the ID is an active wisp (reads wisps table).
	if !IsActiveWispInTx(ctx, ignoredTx, id) {
		return fmt.Errorf("wisp %s not found", id)
	}

	// Get the wisp issue data (reads wisps table).
	issue, err := GetIssueInTx(ctx, ignoredTx, id)
	if err != nil {
		return fmt.Errorf("get wisp for promote: %w", err)
	}
	if issue == nil {
		return fmt.Errorf("wisp %s not found", id)
	}

	// Clear ephemeral flag for persistent storage.
	issue.Ephemeral = false

	// Create in issues table via CreateIssueInTx (reads config from regular tables).
	bc, err := NewBatchContext(ctx, regularTx, storage.BatchCreateOptions{
		SkipPrefixValidation: true,
	})
	if err != nil {
		return fmt.Errorf("new batch context: %w", err)
	}
	if err := CreateIssueInTx(ctx, regularTx, ignoredTx, bc, issue, actor); err != nil {
		return fmt.Errorf("promote wisp to issues: %w", err)
	}

	// Copy labels: wisp_labels → labels.
	if _, err := regularTx.ExecContext(ctx, `
		INSERT IGNORE INTO labels (issue_id, label)
		SELECT issue_id, label FROM wisp_labels WHERE issue_id = ?
	`, id); err != nil {
		log.Printf("promote %s: failed to copy labels: %v", id, err)
	}

	// Copy dependencies: wisp_dependencies → dependencies (best-effort).
	if _, err := regularTx.ExecContext(ctx, `
		INSERT IGNORE INTO dependencies (issue_id, depends_on_id, type, created_at, created_by, metadata, thread_id)
		SELECT issue_id, depends_on_id, type, created_at, created_by, metadata, thread_id
		FROM wisp_dependencies WHERE issue_id = ?
	`, id); err != nil {
		log.Printf("promote %s: failed to copy dependencies: %v", id, err)
	}

	// Copy events: wisp_events → events (best-effort).
	if _, err := regularTx.ExecContext(ctx, `
		INSERT IGNORE INTO events (issue_id, event_type, actor, old_value, new_value, comment, created_at)
		SELECT issue_id, event_type, actor, old_value, new_value, comment, created_at
		FROM wisp_events WHERE issue_id = ?
	`, id); err != nil {
		log.Printf("promote %s: failed to copy events: %v", id, err)
	}

	// Copy comments: wisp_comments → comments (best-effort).
	if _, err := regularTx.ExecContext(ctx, `
		INSERT IGNORE INTO comments (issue_id, author, text, created_at)
		SELECT issue_id, author, text, created_at
		FROM wisp_comments WHERE issue_id = ?
	`, id); err != nil {
		log.Printf("promote %s: failed to copy comments: %v", id, err)
	}

	// Delete from wisp tables.
	return DeleteIssueInTx(ctx, regularTx, ignoredTx, id)
}
