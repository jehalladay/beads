package issueops

import (
	"context"
	"fmt"
	"strings"

	"github.com/steveyegge/beads/internal/types"
)

// GetLabelsInTx retrieves all labels for an issue within an existing transaction.
// Automatically routes to wisp_labels if the ID is an active wisp.
// Returns labels sorted alphabetically.
func GetLabelsInTx(ctx context.Context, tx DBTX, table, issueID string) ([]string, error) {
	if table == "" {
		isWisp := IsActiveWispInTx(ctx, tx, issueID)
		_, table, _, _ = WispTableRouting(isWisp)
	}
	//nolint:gosec // G201: table is from WispTableRouting ("labels" or "wisp_labels")
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`SELECT label FROM %s WHERE issue_id = ? ORDER BY label`, table), issueID)
	if err != nil {
		return nil, fmt.Errorf("get labels: %w", err)
	}
	defer rows.Close()

	var labels []string
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err != nil {
			return nil, fmt.Errorf("get labels: scan: %w", err)
		}
		labels = append(labels, label)
	}
	return labels, rows.Err()
}

// GetLabelsForIssuesInTx fetches labels for multiple issues in a single transaction.
// Routes each ID to labels or wisp_labels based on wisp status.
// Uses a single batched wisp-partition query plus batched IN clauses per label
// table, so the number of round-trips is O(1 + N/queryBatchSize) rather than
// O(N). This matters on remote backends (Dolt) where per-ID round-trips would
// otherwise blow past the context deadline — see GH#3414.
//
// Callers hydrating multiple batches inside one tx may pass a precomputed
// active-wisp set scoped to issueIDs to avoid rebuilding it.
func GetLabelsForIssuesInTx(ctx context.Context, tx DBTX, issueIDs []string, wispSetOpt ...map[string]struct{}) (map[string][]string, error) {
	if len(issueIDs) == 0 {
		return make(map[string][]string), nil
	}

	var wispIDs, permIDs []string
	if len(wispSetOpt) > 0 && wispSetOpt[0] != nil {
		wispIDs, permIDs = partitionByWispSet(issueIDs, wispSetOpt[0])
	} else {
		var err error
		wispIDs, permIDs, err = PartitionWispIDsInTx(ctx, tx, issueIDs)
		if err != nil {
			return nil, err
		}
	}

	result := make(map[string][]string)
	if len(wispIDs) > 0 {
		if err := getLabelsIntoFromTable(ctx, tx, "wisp_labels", wispIDs, result); err != nil {
			return nil, err
		}
	}
	if len(permIDs) > 0 {
		if err := getLabelsIntoFromTable(ctx, tx, "labels", permIDs, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// GetLabelsForIssuesFromTableInTx is a fast path for callers that already know
// which label table applies to every ID in the batch (e.g. searchTableInTx,
// which queries either the issues or wisps table exclusively). It skips the
// wisp-partition round-trip entirely. labelTable must be "labels" or
// "wisp_labels"; callers route via FilterTables.
func GetLabelsForIssuesFromTableInTx(ctx context.Context, tx DBTX, labelTable string, issueIDs []string) (map[string][]string, error) {
	if len(issueIDs) == 0 {
		return make(map[string][]string), nil
	}
	result := make(map[string][]string)
	if err := getLabelsIntoFromTable(ctx, tx, labelTable, issueIDs, result); err != nil {
		return nil, err
	}
	return result, nil
}

// getLabelsIntoFromTable executes the batched SELECT for a single label table
// and accumulates results into the provided map.
//
//nolint:gosec // G201: labelTable is "labels" or "wisp_labels" (hardcoded by callers).
func getLabelsIntoFromTable(ctx context.Context, tx DBTX, labelTable string, ids []string, result map[string][]string) error {
	for start := 0; start < len(ids); start += queryBatchSize {
		end := start + queryBatchSize
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		placeholders := make([]string, len(batch))
		args := make([]any, len(batch))
		for i, id := range batch {
			placeholders[i] = "?"
			args[i] = id
		}
		rows, err := tx.QueryContext(ctx, fmt.Sprintf(
			`SELECT issue_id, label FROM %s WHERE issue_id IN (%s) ORDER BY issue_id, label`,
			labelTable, strings.Join(placeholders, ",")), args...)
		if err != nil {
			return fmt.Errorf("get labels for issues from %s: %w", labelTable, err)
		}
		for rows.Next() {
			var issueID, label string
			if err := rows.Scan(&issueID, &label); err != nil {
				_ = rows.Close()
				return fmt.Errorf("get labels for issues: scan: %w", err)
			}
			result[issueID] = append(result[issueID], label)
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("get labels for issues: rows: %w", err)
		}
	}
	return nil
}

// AddLabelInTx adds a label to an issue and records an event within an existing
// transaction. Automatically routes to wisp tables if the ID is an active wisp.
// Uses INSERT IGNORE for idempotency.
// maxLabelLen is the width of the label VARCHAR(255) column in both the
// `labels` and `wisp_labels` tables (migrations 0003/0021). A label longer
// than this otherwise reaches the INSERT and fails with a raw, value-echoing
// "Error 1105 ... too large for column" — the standalone `bd label add` path
// does NOT run Issue.Validate, so its length guard (beads-2953) does not cover
// it. Guarding here closes that gap for AddLabel (beads-ho89).
const maxLabelLen = 255

func AddLabelInTx(ctx context.Context, tx DBTX, labelTable, eventTable, issueID, label, actor string) error {
	// Trim surrounding whitespace so the stored label matches what the
	// query/filter side searches for (utils.NormalizeLabels trims its input);
	// an untrimmed "  bug  " is permanently unmatchable (beads-13zc). This is
	// the single live-path chokepoint for AddLabel across the dolt and
	// embeddeddolt stores, and mirrors the CLI `bd label add` TrimSpace guard.
	label = strings.TrimSpace(label)
	// beads-9jjj8: case-fold labels at WRITE so all three verbs agree. The
	// query/filter side is case-INSENSITIVE (LOWER(label)=LOWER(?) throughout
	// sqlbuild), so storing verbatim mixed case let 'FOO' and 'foo' coexist yet
	// both surface under `--label foo` (un-disambiguatable), and `label remove
	// foo` failed to remove a stored 'FOO' (case-exact DELETE) even though the
	// user had just found it via that same query casing. Folding at write is the
	// coherent end-state (matches NormalizeIssueType's write-fold and the
	// assignee LOWER()/EqualFold precedent); RemoveLabelInTx also folds so the
	// trap closes for pre-existing mixed-case rows.
	label = strings.ToLower(label)
	if label == "" {
		return fmt.Errorf("label must not be empty")
	}
	if len(label) > maxLabelLen {
		return fmt.Errorf("label must be %d characters or less (got %d)", maxLabelLen, len(label))
	}
	// Reject interior delimiter chars (beads-pqzx): the markdown "### Labels"
	// round-trip splits that section on ',' and newlines (parseLabels,
	// cmd/bd/markdown.go), so a stored label containing ',' / '\n' / '\r'
	// re-imports as MULTIPLE labels — round-trip identity corruption, the
	// native leg of the SCM label-delimiter-collision class (ADO ';',
	// Notion ','). Spaces stay legal (beads-ehw7: parseLabels never splits on
	// spaces). This is the single AddLabel chokepoint, so it guards tag +
	// label add + every other caller.
	if strings.ContainsAny(label, ",\n\r") {
		return fmt.Errorf("label %q must not contain a comma or newline (these are reserved as label delimiters)", label)
	}
	if labelTable == "" || eventTable == "" {
		isWisp := IsActiveWispInTx(ctx, tx, issueID)
		_, lt, et, _ := WispTableRouting(isWisp)
		if labelTable == "" {
			labelTable = lt
		}
		if eventTable == "" {
			eventTable = et
		}
	}
	//nolint:gosec // G201: labelTable is from WispTableRouting ("labels" or "wisp_labels")
	res, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT IGNORE INTO %s (issue_id, label) VALUES (?, ?)`, labelTable), issueID, label)
	if err != nil {
		return fmt.Errorf("add label: %w", err)
	}
	// INSERT IGNORE is a no-op when the label is already present; recording a
	// label_added event in that case pollutes the audit trail with an addition
	// that never happened (beads-usz1). Only record the event on a real insert.
	if affected, aerr := res.RowsAffected(); aerr == nil && affected == 0 {
		return nil
	}
	comment := "Added label: " + label
	//nolint:gosec // G201: eventTable is from WispTableRouting ("events" or "wisp_events")
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (id, issue_id, event_type, actor, comment) VALUES (?, ?, ?, ?, ?)`, eventTable),
		NewEventID(), issueID, types.EventLabelAdded, actor, comment); err != nil {
		return fmt.Errorf("add label: record event: %w", err)
	}
	return nil
}

// SetLabelsInTx replaces an issue's label set with exactly `labels`, diffing
// against the current set inside ONE transaction: it removes only
// current-not-desired and adds only desired-not-present, so unchanged labels
// are left untouched (no churn event) and the whole replace is atomic — a
// mid-diff failure rolls back the caller's tx rather than leaving a
// half-applied set (beads-idvy). This lifts the canonical diff logic that
// previously lived only in the proxied domain path (labelUseCase.setMany) down
// to the shared in-tx seam so the direct CLI path (applyLabelUpdates) and the
// proxied path share ONE implementation. Desired labels are trimmed and
// de-duplicated the same way AddLabelInTx trims (beads-13zc); an empty/
// whitespace-only entry is skipped.
func SetLabelsInTx(ctx context.Context, tx DBTX, labelTable, eventTable, issueID string, labels []string, actor string) error {
	// Resolve wisp routing once so the list/remove/add below all target the
	// same tables (and we don't re-probe IsActiveWispInTx per call).
	if labelTable == "" || eventTable == "" {
		isWisp := IsActiveWispInTx(ctx, tx, issueID)
		_, lt, et, _ := WispTableRouting(isWisp)
		if labelTable == "" {
			labelTable = lt
		}
		if eventTable == "" {
			eventTable = et
		}
	}

	// beads-9jjj8: fold desired labels to lower so the diff below compares like
	// with like — AddLabelInTx/RemoveLabelInTx now case-fold, and stored labels
	// are lower, so an un-folded desired 'FOO' would false-diff against a stored
	// 'foo' (spurious remove+re-add churn, and a stale mixed-case event comment).
	desired := make(map[string]bool, len(labels))
	for _, l := range labels {
		if l = strings.ToLower(strings.TrimSpace(l)); l != "" {
			desired[l] = true
		}
	}

	current, err := GetLabelsInTx(ctx, tx, labelTable, issueID)
	if err != nil {
		return err
	}
	existing := make(map[string]bool, len(current))
	for _, l := range current {
		existing[l] = true
		if !desired[l] {
			if err := RemoveLabelInTx(ctx, tx, labelTable, eventTable, issueID, l, actor); err != nil {
				return err
			}
		}
	}
	for l := range desired {
		if !existing[l] {
			if err := AddLabelInTx(ctx, tx, labelTable, eventTable, issueID, l, actor); err != nil {
				return err
			}
		}
	}
	return nil
}

// RemoveLabelInTx removes a label from an issue and records an event within
// an existing transaction. Automatically routes to wisp tables if the ID is
// an active wisp.
//
//nolint:gosec // G201: table names come from WispTableRouting (hardcoded constants)
func RemoveLabelInTx(ctx context.Context, tx DBTX, labelTable, eventTable, issueID, label, actor string) error {
	// Trim so a padded `--remove-label "  bug  "` matches a label stored as
	// "bug" (labels are persisted trimmed after beads-13zc). A whitespace-only
	// arg trims to empty and can never match an existing label, so the DELETE
	// no-ops (matching the add-side empty guard's intent).
	label = strings.TrimSpace(label)
	// beads-9jjj8: match on LOWER() so remove is case-insensitive like the
	// query side (LOWER(label)=LOWER(?) throughout sqlbuild). Previously the
	// DELETE was case-EXACT, so `label remove foo` could not remove a stored
	// 'FOO' the user had just surfaced with `--label foo` (the find-then-
	// cannot-remove trap). New writes fold to lower (AddLabelInTx), but folding
	// here too clears pre-existing mixed-case rows and covers a padded/mixed
	// remove arg regardless.
	label = strings.ToLower(label)
	if labelTable == "" || eventTable == "" {
		isWisp := IsActiveWispInTx(ctx, tx, issueID)
		_, lt, et, _ := WispTableRouting(isWisp)
		if labelTable == "" {
			labelTable = lt
		}
		if eventTable == "" {
			eventTable = et
		}
	}
	res, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE issue_id = ? AND LOWER(label) = ?`, labelTable), issueID, label)
	if err != nil {
		return fmt.Errorf("remove label: %w", err)
	}
	// The DELETE is a no-op when the label was never on the issue; recording a
	// label_removed event in that case pollutes the audit trail with a removal
	// that never happened (beads-usz1). Only record the event on a real delete.
	if affected, aerr := res.RowsAffected(); aerr == nil && affected == 0 {
		return nil
	}
	comment := "Removed label: " + label
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (id, issue_id, event_type, actor, comment) VALUES (?, ?, ?, ?, ?)`, eventTable),
		NewEventID(), issueID, types.EventLabelRemoved, actor, comment); err != nil {
		return fmt.Errorf("remove label: record event: %w", err)
	}
	return nil
}
