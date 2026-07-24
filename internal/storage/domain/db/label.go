package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/steveyegge/beads/internal/storage/dberrors"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

func NewLabelSQLRepository(runner Runner) domain.LabelSQLRepository {
	return &labelSQLRepositoryImpl{
		runner: runner,
		events: NewEventsSQLRepository(runner),
	}
}

type labelSQLRepositoryImpl struct {
	runner Runner
	events domain.EventsSQLRepository
}

var _ domain.LabelSQLRepository = (*labelSQLRepositoryImpl)(nil)

func pickLabelTable(useWisps bool) string {
	if useWisps {
		return "wisp_labels"
	}
	return "labels"
}

func (r *labelSQLRepositoryImpl) Insert(ctx context.Context, issueID, label, actor string, opts domain.LabelOpts) error {
	if issueID == "" {
		return fmt.Errorf("db: LabelSQLRepository.Insert: issueID must not be empty")
	}
	if label == "" {
		return fmt.Errorf("db: LabelSQLRepository.Insert: label must not be empty")
	}
	// beads-ukeeh (domain/proxied twin of the direct issueops.AddLabelInTx fold,
	// beads-9jjj8): fold the label to lower-case before the write. The label
	// query/filter side is case-INSENSITIVE (LOWER(label)=LOWER(?) throughout
	// sqlbuild), so storing verbatim let 'FOO' and 'foo' coexist and made an
	// issue findable by `--label foo` yet un-removable by `label remove foo` on a
	// hub-connected (proxied) crew. Folding here matches the direct path so both
	// modes store the same canonical casing. Empty-after-fold is impossible
	// (ToLower does not empty a non-empty string), so the empty guard above holds.
	label = strings.ToLower(label)
	table := pickLabelTable(opts.UseWispsTable)
	//nolint:gosec // G201: table is one of two hardcoded constants
	res, err := r.runner.ExecContext(ctx,
		fmt.Sprintf("INSERT IGNORE INTO %s (issue_id, label) VALUES (?, ?)", table),
		issueID, label,
	)
	if err != nil {
		return fmt.Errorf("db: LabelSQLRepository.Insert %s/%s: %w", issueID, label, err)
	}
	// INSERT IGNORE is a no-op when the label is already present; recording a
	// label_added event in that case pollutes the audit trail with an addition
	// that never happened (beads-usz1). Mirror the direct path
	// (issueops.AddLabelInTx): only record the event on a real insert, and skip
	// only when RowsAffected==0 AND the driver actually reported it (aerr==nil),
	// so a driver that doesn't support RowsAffected still records (fail-safe).
	if affected, aerr := res.RowsAffected(); aerr == nil && affected == 0 {
		return nil
	}
	// Mirror the direct path (issueops.AddLabelInTx): the label event's
	// human-readable value goes in the comment column ("Added label: X"), not
	// old_value/new_value, so proxied and direct crew produce the same audit row
	// shape (beads-6p27f).
	return r.events.Record(ctx, domain.Event{
		IssueID: issueID,
		Type:    types.EventLabelAdded,
		Actor:   actor,
		Comment: "Added label: " + label,
	}, domain.RecordEventOpts{UseWispsTable: opts.UseWispsTable})
}

func (r *labelSQLRepositoryImpl) Delete(ctx context.Context, issueID, label, actor string, opts domain.LabelOpts) error {
	if issueID == "" {
		return fmt.Errorf("db: LabelSQLRepository.Delete: issueID must not be empty")
	}
	if label == "" {
		return fmt.Errorf("db: LabelSQLRepository.Delete: label must not be empty")
	}
	// beads-ukeeh (domain/proxied twin of issueops.RemoveLabelInTx, beads-9jjj8):
	// fold the input AND match on LOWER(label) so remove is case-insensitive like
	// the query side, and so legacy mixed-case rows (stored before the Insert fold
	// above) are still clearable. A case-EXACT DELETE could not remove a 'FOO'
	// surfaced by `--label foo`.
	label = strings.ToLower(label)
	table := pickLabelTable(opts.UseWispsTable)
	//nolint:gosec // G201: table is one of two hardcoded constants
	res, err := r.runner.ExecContext(ctx,
		fmt.Sprintf("DELETE FROM %s WHERE issue_id = ? AND LOWER(label) = ?", table),
		issueID, label,
	)
	if err != nil {
		return fmt.Errorf("db: LabelSQLRepository.Delete %s/%s: %w", issueID, label, err)
	}
	// The DELETE is a no-op when the label was never on the issue; recording a
	// label_removed event in that case pollutes the audit trail with a removal
	// that never happened (beads-usz1). Mirror the direct path
	// (issueops.RemoveLabelInTx): only record the event on a real delete, and
	// skip only when RowsAffected==0 AND the driver actually reported it
	// (aerr==nil), so a driver that doesn't support RowsAffected still records.
	if affected, aerr := res.RowsAffected(); aerr == nil && affected == 0 {
		return nil
	}
	// Mirror the direct path (issueops.RemoveLabelInTx): the label event's
	// human-readable value goes in the comment column ("Removed label: X"), not
	// old_value/new_value (beads-6p27f).
	return r.events.Record(ctx, domain.Event{
		IssueID: issueID,
		Type:    types.EventLabelRemoved,
		Actor:   actor,
		Comment: "Removed label: " + label,
	}, domain.RecordEventOpts{UseWispsTable: opts.UseWispsTable})
}

func (r *labelSQLRepositoryImpl) List(ctx context.Context, issueID string, opts domain.LabelOpts) ([]string, error) {
	if issueID == "" {
		return nil, fmt.Errorf("db: LabelSQLRepository.List: issueID must not be empty")
	}
	table := pickLabelTable(opts.UseWispsTable)
	//nolint:gosec // G201: table is one of two hardcoded constants
	rows, err := r.runner.QueryContext(ctx,
		fmt.Sprintf("SELECT label FROM %s WHERE issue_id = ? ORDER BY label", table),
		issueID,
	)
	if err != nil {
		return nil, fmt.Errorf("db: LabelSQLRepository.List %s: %w", issueID, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err != nil {
			return nil, fmt.Errorf("db: LabelSQLRepository.List: scan: %w", err)
		}
		out = append(out, label)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: LabelSQLRepository.List: rows: %w", err)
	}
	return out, nil
}

func (r *labelSQLRepositoryImpl) ListByIssueIDs(ctx context.Context, issueIDs []string, opts domain.LabelOpts) (map[string][]string, error) {
	result := make(map[string][]string)
	if len(issueIDs) == 0 {
		return result, nil
	}
	placeholders := make([]string, len(issueIDs))
	args := make([]any, len(issueIDs))
	for i, id := range issueIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	table := pickLabelTable(opts.UseWispsTable)
	//nolint:gosec // G201: table is one of two hardcoded constants
	q := fmt.Sprintf(
		"SELECT issue_id, label FROM %s WHERE issue_id IN (%s) ORDER BY issue_id, label",
		table, strings.Join(placeholders, ","),
	)
	rows, err := r.runner.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("db: LabelSQLRepository.ListByIssueIDs: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var issueID, label string
		if err := rows.Scan(&issueID, &label); err != nil {
			return nil, fmt.Errorf("db: LabelSQLRepository.ListByIssueIDs: scan: %w", err)
		}
		result[issueID] = append(result[issueID], label)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: LabelSQLRepository.ListByIssueIDs: rows: %w", err)
	}
	return result, nil
}

func (r *labelSQLRepositoryImpl) DeleteAllForIDs(ctx context.Context, ids []string, opts domain.LabelOpts) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	table := "labels"
	if opts.UseWispsTable {
		table = "wisp_labels"
	}
	total := 0
	for start := 0; start < len(ids); start += deleteBatchSize {
		end := start + deleteBatchSize
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
		//nolint:gosec // G201: table is one of two hardcoded constants; ? placeholders only.
		res, err := r.runner.ExecContext(ctx,
			fmt.Sprintf("DELETE FROM %s WHERE issue_id IN (%s)", table, strings.Join(placeholders, ",")),
			args...)
		if err != nil {
			if opts.UseWispsTable && dberrors.IsTableNotExist(err) {
				return total, nil
			}
			return total, fmt.Errorf("db: LabelSQLRepository.DeleteAllForIDs from %s: %w", table, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("db: LabelSQLRepository.DeleteAllForIDs rows affected: %w", err)
		}
		total += int(n)
	}
	return total, nil
}

func (r *labelSQLRepositoryImpl) CountAllForIDs(ctx context.Context, ids []string, opts domain.LabelOpts) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	table := "labels"
	if opts.UseWispsTable {
		table = "wisp_labels"
	}
	count, err := issueops.CountRowsForIssueIDsInTx(ctx, r.runner, table, ids)
	if err != nil {
		if opts.UseWispsTable && dberrors.IsTableNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("db: LabelSQLRepository.CountAllForIDs: %w", err)
	}
	return count, nil
}
