package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/storage/sqlbuild"
	"github.com/steveyegge/beads/internal/types"
)

func NewIssueSQLRepository(runner Runner) domain.IssueSQLRepository {
	return &issueSQLRepositoryImpl{
		runner: runner,
		events: NewEventsSQLRepository(runner),
	}
}

type issueSQLRepositoryImpl struct {
	runner Runner
	events domain.EventsSQLRepository
}

var _ domain.IssueSQLRepository = (*issueSQLRepositoryImpl)(nil)

// issueSelectColumns aliases the shared canonical column list; the scan side
// delegates to issueops.ScanIssueFrom, which scans it positionally.
const issueSelectColumns = sqlbuild.IssueSelectColumns

var allowedUpdateFields = map[string]struct{}{
	"status": {}, "priority": {}, "title": {}, "assignee": {},
	"description": {}, "design": {}, "acceptance_criteria": {}, "notes": {},
	"issue_type": {}, "estimated_minutes": {}, "external_ref": {}, "spec_id": {},
	"started_at": {}, "closed_at": {}, "close_reason": {}, "closed_by_session": {},
	"source_repo": {}, "sender": {}, "wisp": {}, "wisp_type": {}, "no_history": {}, "pinned": {},
	"mol_type": {}, "event_kind": {}, "actor": {}, "target": {}, "payload": {},
	"due_at": {}, "defer_until": {}, "await_id": {}, "waiters": {},
	"bonded_from": {},
	"metadata":    {},
}

var updateFieldColumnRename = map[string]string{
	"wisp": "ephemeral",
}

func (r *issueSQLRepositoryImpl) Insert(ctx context.Context, issue *types.Issue, actor string, opts domain.InsertIssueOpts) error {
	if issue == nil {
		return errors.New("db: Insert: issue must not be nil")
	}

	normalizeIssueTimestamps(issue)
	if issue.ContentHash == "" {
		issue.ContentHash = issue.ComputeContentHash()
	}

	if issue.ID == "" {
		return errors.New("db: Insert: explicit ID required (ID generation belongs to CreateIssueUseCase)")
	}

	table := pickIssueTable(opts.UseWispsTable)

	// insertIssueRow runs INSERT ... ON DUPLICATE KEY UPDATE (an upsert): when
	// the id already exists the row is UPDATED, not created. Recording an
	// EventCreated in that case pollutes the audit trail with a "created" event
	// for an overwrite that never created anything (beads-64nbj). The direct
	// path guards this — issueops.CreateIssueInTxWithResult records EventCreated
	// only when isNew (InsertIssueIfNew: existingCount==0). Mirror that by
	// checking existence BEFORE the upsert; the isNew existence-check is the safe
	// mirror because ON DUPLICATE KEY UPDATE's RowsAffected is ambiguous
	// (2 on a changed update, 0 on a no-op update, 1 on a fresh insert). This is
	// the create-event twin of the 5vpoh label no-op-event fix.
	existed, err := r.Exists(ctx, issue.ID, domain.IssueTableOpts{UseWispsTable: opts.UseWispsTable})
	if err != nil {
		return err
	}
	if err := insertIssueRow(ctx, r.runner, table, issue); err != nil {
		return err
	}
	if existed {
		// Overwrite of an existing row — no create happened, so no created event
		// (matches the direct isNew guard).
		return nil
	}
	return r.events.Record(ctx, domain.Event{
		IssueID: issue.ID,
		Type:    types.EventCreated,
		Actor:   actor,
	}, domain.RecordEventOpts{UseWispsTable: opts.UseWispsTable})
}

func (r *issueSQLRepositoryImpl) InsertBatch(ctx context.Context, issues []*types.Issue, actor string, opts domain.InsertIssueOpts) error {
	for _, issue := range issues {
		if err := r.Insert(ctx, issue, actor, opts); err != nil {
			return err
		}
	}
	return nil
}

func (r *issueSQLRepositoryImpl) Update(ctx context.Context, id string, updates map[string]any, actor string, opts domain.IssueTableOpts) error {
	if id == "" {
		return errors.New("db: Update: id must not be empty")
	}
	if len(updates) == 0 {
		return nil
	}

	// beads-iu9f Phase B / 25k6: on the update path (Finalize), run the same
	// input-type guards the shared seam does BEFORE the SQL, so a >500 title or
	// negative estimate fails with a clean domain error instead of a raw Dolt
	// column error. Skipped for graph-apply/assignee/ref-rewrite callers.
	if opts.Finalize {
		if err := issueops.ValidateUpdateInputs(updates); err != nil {
			return err
		}
	}

	table := pickIssueTable(opts.UseWispsTable)

	// Read the prior issue up-front when the caller is changing the status:
	// needed for the closed_at/started_at/pinned management below, the is_blocked
	// recompute at the end, AND the audit event's OldValue + DetermineEventType
	// (beads-ssuvz — the direct path reads oldIssue for exactly this). A full
	// read (vs the old status/started_at/pinned-only SELECT) keeps the proxied
	// update event in parity with the direct twin (issueops.updateIssueInTx),
	// which records the specific event type (closed/reopened/status_changed) with
	// the old/new value diff instead of a generic "updated".
	var oldIssue *types.Issue
	var oldStatus types.Status
	var oldStartedAt sql.NullTime
	_, statusChanging := updates["status"]
	if statusChanging {
		got, err := r.Get(ctx, id, opts)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("db: Update %s: %w", id, sql.ErrNoRows)
			}
			return fmt.Errorf("db: Update %s: read old issue: %w", id, err)
		}
		oldIssue = got
		oldStatus = got.Status
		if got.StartedAt != nil {
			oldStartedAt = sql.NullTime{Time: *got.StartedAt, Valid: true}
		}
	}

	setClauses := make([]string, 0, len(updates))
	args := make([]any, 0, len(updates)+1)
	for key, value := range updates {
		if _, ok := allowedUpdateFields[key]; !ok {
			return fmt.Errorf("db: Update: field %q is not allowed", key)
		}
		column := key
		if renamed, ok := updateFieldColumnRename[key]; ok {
			column = renamed
		}
		setClauses = append(setClauses, fmt.Sprintf("`%s` = ?", column))
		args = append(args, normalizeUpdateValue(key, value))
	}

	// beads-h3iv: auto-manage closed_at on a status transition, mirroring the
	// shared seam (issueops.ManageClosedAt, wired into UpdateIssueInTx for the
	// direct/embedded path). Without this the domain/proxied UPDATE path set
	// status=closed but left closed_at NULL, so the Finalize invariant
	// ("closed issues must have closed_at timestamp") rejected every
	// `bd update --status closed` over the proxied server. Only inject when the
	// caller did not already set closed_at explicitly, and only on a real
	// transition into/out of the closed state.
	if statusChanging {
		if _, callerSetClosedAt := updates["closed_at"]; !callerSetClosedAt {
			newStatus := coerceStatus(updates["status"])
			switch {
			case newStatus == types.StatusClosed && oldStatus != types.StatusClosed:
				setClauses = append(setClauses, "closed_at = ?")
				args = append(args, time.Now().UTC())
				// beads-6qo8t: default close_reason to "Closed" on the OPEN→closed
				// transition, mirroring `bd close` and the direct-path shared seam
				// (issueops.ManageClosedAt). Keeps the domain/proxied UPDATE path in
				// parity so `bd update --status closed` stores close_reason='Closed'
				// like `bd close`, not NULL. Only when the caller did not set
				// close_reason explicitly, and only on a real fresh close.
				if _, callerSetReason := updates["close_reason"]; !callerSetReason {
					setClauses = append(setClauses, "close_reason = ?")
					args = append(args, "Closed")
				}
			case newStatus != types.StatusClosed && oldStatus == types.StatusClosed:
				// beads-9gp5d: clear close_reason and closed_by_session alongside
				// closed_at on reopen, mirroring the direct shared seam
				// (issueops.ManageClosedAt reopen branch, beads-ni2ph, which
				// appends all three unconditionally). Clearing only closed_at left
				// a stale close_reason/closed_by_session on the proxied
				// `bd update --status open` path — a contradictory
				// "open but closed by session X" row (the exact state ni2ph fixed
				// on the direct path).
				setClauses = append(setClauses, "closed_at = ?", "close_reason = ?", "closed_by_session = ?")
				args = append(args, nil, "", "")
			}
		}

		// beads-hfb4: auto-manage started_at on a transition INTO in_progress,
		// mirroring the shared seam (issueops.ManageStartedAt, wired into
		// UpdateIssueInTx for the direct/embedded path). Without this the
		// domain/proxied UPDATE path left started_at NULL after
		// `bd update --status in_progress` (the --claim path sets it, a bare
		// status update did not) — a silent data-fidelity gap vs the direct path
		// (started_at feeds cycle-time/age/stale metrics). An existing started_at
		// is preserved and an explicit caller value is respected, matching
		// ManageStartedAt.
		if _, callerSetStartedAt := updates["started_at"]; !callerSetStartedAt {
			if coerceStatus(updates["status"]) == types.StatusInProgress && !oldStartedAt.Valid {
				setClauses = append(setClauses, "started_at = ?")
				args = append(args, time.Now().UTC())
			}
		}

		// beads-y20w2: the pinned COLUMN (--pinned) is a prune/purge protection
		// marker managed SOLELY by --pinned/--no-pinned, orthogonal to the
		// "pinned" STATUS (beads-9ynk). Entering the pinned STATUS never sets the
		// column, so an auto-clear on the status-pinned-EXIT leg could only be a
		// no-op or a silent clobber of an independently-set shield (the u3la5
		// data-loss class). n79c introduced this mirror; u3la5 narrowed its key
		// COLUMN->STATUS but left the EXIT leg; y20w2 removes it entirely here too,
		// keeping parity with the issueops shared seam (updateIssueInTx).
	}

	setClauses = append(setClauses, "updated_at = ?")
	args = append(args, time.Now().UTC())
	args = append(args, id)

	//nolint:gosec // G201: table is one of two hardcoded constants
	q := fmt.Sprintf("UPDATE %s SET %s WHERE id = ?", table, strings.Join(setClauses, ", "))
	res, err := r.runner.ExecContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("db: Update %s: %w", id, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("db: Update %s: rows affected: %w", id, err)
	}
	if rows == 0 {
		// beads-j91h: RowsAffected==0 conflates two distinct cases — the row
		// does not exist, OR the row exists but every SET value already equals
		// its current value (a no-op update; MySQL/Dolt report 0 changed rows
		// without CLIENT_FOUND_ROWS). The direct store path does not gate on
		// rows==0 at all, so a same-value update succeeds there; treating it as
		// ErrNoRows here made the proxied path hard-error on a no-op where the
		// direct path succeeds (direct-vs-proxied asymmetry). Disambiguate with
		// an existence check: a present row means a no-op (success); only a
		// truly missing row is ErrNoRows.
		exists, existsErr := r.Exists(ctx, id, opts)
		if existsErr != nil {
			return fmt.Errorf("db: Update %s: %w", id, existsErr)
		}
		if !exists {
			return fmt.Errorf("db: Update %s: %w", id, sql.ErrNoRows)
		}
		// Row exists, update was a no-op — fall through so the event is still
		// recorded (matches the direct path's behavior of always succeeding).
	}

	// Record the audit event through the shared issueops helper so the proxied
	// path emits the SAME event type + old/new value diff as the direct twin
	// (beads-ssuvz): a status change records status_changed/closed/reopened (not
	// a generic "updated"), with OldValue=json(oldIssue) / NewValue=json(updates).
	// oldIssue is non-nil only when statusChanging (a non-status update keeps
	// EventUpdated with an empty OldValue, matching the direct path where
	// DetermineEventType returns EventUpdated for a no-status update). r.runner
	// satisfies issueops.DBTX, and the event table is picked from UseWispsTable —
	// identical to r.events.Record's routing.
	eventTable := "events"
	if opts.UseWispsTable {
		eventTable = "wisp_events"
	}
	if err := issueops.RecordUpdateEventInTable(ctx, r.runner, eventTable, id, oldIssue, updates, actor); err != nil {
		return fmt.Errorf("db: Update %s: record event: %w", id, err)
	}

	// beads-iu9f Phase B / 25k6 / lsbu / rzx8: on the update path (Finalize),
	// re-run the shared finalize — full Issue.ValidateWithCustom on the merged
	// persisted state (rejects >500 title / non-object metadata) + content_hash
	// recompute — in this same runner tx. A validation failure rolls back the
	// caller's transaction, so an invalid write never commits.
	if opts.Finalize {
		if err := issueops.FinalizeUpdatedIssueInTx(ctx, r.runner, table, id); err != nil {
			return err
		}
	}

	if statusChanging {
		newStatus := coerceStatus(updates["status"])
		oldActive := oldStatus != types.StatusClosed && oldStatus != types.StatusPinned
		newActive := newStatus != types.StatusClosed && newStatus != types.StatusPinned
		if oldActive != newActive {
			var (
				affectedIssues, affectedWisps []string
				aerr                          error
			)
			if opts.UseWispsTable {
				affectedIssues, affectedWisps, aerr = issueops.AffectedByStatusChangeForWispInTx(ctx, r.runner, id)
			} else {
				affectedIssues, affectedWisps, aerr = issueops.AffectedByStatusChangeInTx(ctx, r.runner, id)
			}
			if aerr != nil {
				return fmt.Errorf("db: Update %s: affected by status change: %w", id, aerr)
			}
			if err := issueops.RecomputeIsBlockedInTx(ctx, r.runner, affectedIssues, affectedWisps); err != nil {
				return fmt.Errorf("db: Update %s: recompute is_blocked: %w", id, err)
			}
		}
	}
	return nil
}

// ApplyMetadataEdits applies per-key metadata sets/unsets and/or a shallow
// object merge atomically SERVER-SIDE via issueops.MergeMetadataInTx /
// ApplyMetadataKeyEditsInTx (a single JSON_SET/JSON_REMOVE inside r.runner's
// tx), never a client-side whole-blob read-modify-write — so two concurrent
// proxied metadata edits to different keys both survive (beads-jibd/fnp6).
// Ordering matches the client-side path it replaces: merge first, then sets,
// then unsets. On Finalize it re-runs the shared finalize (metadata-object
// validation + content_hash recompute) in the same tx, so a bad write rolls
// back. Records NO audit event for the metadata-only edit, matching the
// direct/embedded backend (beads-ht3em).
func (r *issueSQLRepositoryImpl) ApplyMetadataEdits(ctx context.Context, id string, sets map[string]json.RawMessage, unsets []string, merge json.RawMessage, actor string, opts domain.IssueTableOpts) error {
	if id == "" {
		return errors.New("db: ApplyMetadataEdits: id must not be empty")
	}
	if len(sets) == 0 && len(unsets) == 0 && len(merge) == 0 {
		return nil
	}

	table := pickIssueTable(opts.UseWispsTable)

	if len(merge) > 0 {
		if err := issueops.MergeMetadataInTx(ctx, r.runner, table, id, merge); err != nil {
			return fmt.Errorf("db: ApplyMetadataEdits %s: merge: %w", id, err)
		}
	}
	if len(sets) > 0 || len(unsets) > 0 {
		if err := issueops.ApplyMetadataKeyEditsInTx(ctx, r.runner, table, id, sets, unsets); err != nil {
			return fmt.Errorf("db: ApplyMetadataEdits %s: key edits: %w", id, err)
		}
	}

	// beads-ht3em: do NOT record an audit event for a metadata-only edit. The
	// direct/embedded backend records NONE — the shared metadata seam
	// (issueops.ApplyMetadataKeyEditsInTx / MergeMetadataInTx) has no
	// events.Record, and the direct cmd chokepoint (cmd/bd/update.go
	// auditIssueUpdate) fires only inside the regularUpdates block, never for
	// the UpdateMetadataFields metadata leg. This path previously recorded a
	// bare, empty-valued EventUpdated, so `bd update --set-metadata` wrote 1
	// audit event on a hub-connected/proxied crew and 0 on an embedded crew — a
	// backend-asymmetric phantom event of the same class as the proxied
	// no-op/phantom events dropped in beads-5vpoh (label no-op) and beads-64nbj
	// (ODKU phantom EventCreated). Status/field changes still record their
	// events through the Update path (RecordUpdateEventInTable, beads-ssuvz);
	// only the pure-metadata leg is silent, matching the direct twin.

	// Re-run the shared finalize (metadata-object validation + content_hash
	// recompute) in this same tx, matching the Update path (beads-lsbu/rzx8).
	if opts.Finalize {
		if err := issueops.FinalizeUpdatedIssueInTx(ctx, r.runner, table, id); err != nil {
			return err
		}
	}
	return nil
}

// AppendNotes atomically appends text to the issue's notes at the DB via a
// single server-side CONCAT_WS (beads-jscve), so a concurrent proxied
// `bd update --append-notes` can't lose an update via a client-side
// read-modify-write. Wisp-aware via opts.UseWispsTable, mirroring
// ApplyMetadataEdits.
func (r *issueSQLRepositoryImpl) AppendNotes(ctx context.Context, id, text string, opts domain.IssueTableOpts) error {
	if id == "" {
		return errors.New("db: AppendNotes: id must not be empty")
	}
	table := pickIssueTable(opts.UseWispsTable)
	if err := issueops.AppendNotesInTx(ctx, r.runner, table, id, text); err != nil {
		return fmt.Errorf("db: AppendNotes %s: %w", id, err)
	}
	if opts.Finalize {
		if err := issueops.FinalizeUpdatedIssueInTx(ctx, r.runner, table, id); err != nil {
			return err
		}
	}
	return nil
}

func coerceStatus(v any) types.Status {
	switch s := v.(type) {
	case string:
		return types.Status(s)
	case types.Status:
		return s
	default:
		return ""
	}
}

func (r *issueSQLRepositoryImpl) Claim(ctx context.Context, id, actor string, opts domain.IssueTableOpts) (domain.ClaimRowResult, error) {
	if id == "" {
		return domain.ClaimRowResult{}, errors.New("db: Claim: id must not be empty")
	}

	oldIssue, err := r.Get(ctx, id, opts)
	if err != nil {
		return domain.ClaimRowResult{}, fmt.Errorf("db: Claim %s: read old issue: %w", id, err)
	}

	table := pickIssueTable(opts.UseWispsTable)
	now := time.Now().UTC()
	startedWasZero := oldIssue.StartedAt == nil

	var res sql.Result
	if startedWasZero {
		//nolint:gosec // G201: table is one of two hardcoded constants
		res, err = r.runner.ExecContext(ctx, fmt.Sprintf(`
			UPDATE %s
			SET assignee = ?, status = 'in_progress', updated_at = ?, started_at = ?
			WHERE id = ? AND status = 'open' AND (assignee = '' OR assignee IS NULL OR assignee = ?)
		`, table), actor, now, now, id, actor)
	} else {
		//nolint:gosec // G201: table is one of two hardcoded constants
		res, err = r.runner.ExecContext(ctx, fmt.Sprintf(`
			UPDATE %s
			SET assignee = ?, status = 'in_progress', updated_at = ?
			WHERE id = ? AND status = 'open' AND (assignee = '' OR assignee IS NULL OR assignee = ?)
		`, table), actor, now, id, actor)
	}
	if err != nil {
		return domain.ClaimRowResult{}, fmt.Errorf("db: Claim %s: %w", id, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return domain.ClaimRowResult{}, fmt.Errorf("db: Claim %s: rows affected: %w", id, err)
	}

	if rows == 0 {
		var currentAssignee sql.NullString
		var currentStatus types.Status
		//nolint:gosec // G201: table is one of two hardcoded constants
		if err := r.runner.QueryRowContext(ctx,
			fmt.Sprintf("SELECT assignee, status FROM %s WHERE id = ?", table), id,
		).Scan(&currentAssignee, &currentStatus); err != nil {
			return domain.ClaimRowResult{}, fmt.Errorf("db: Claim %s: read current state: %w", id, err)
		}
		assignee := ""
		if currentAssignee.Valid {
			assignee = currentAssignee.String
		}
		return domain.ClaimRowResult{
			Updated:          false,
			CurrentAssignee:  assignee,
			CurrentStatus:    currentStatus,
			StartedAtWasZero: startedWasZero,
			OldIssue:         oldIssue,
		}, nil
	}

	oldData, _ := json.Marshal(oldIssue)
	newData, _ := json.Marshal(map[string]any{"assignee": actor, "status": "in_progress"})
	if err := r.events.Record(ctx, domain.Event{
		IssueID:  id,
		Type:     types.EventType("claimed"),
		Actor:    actor,
		OldValue: string(oldData),
		NewValue: string(newData),
	}, domain.RecordEventOpts{UseWispsTable: opts.UseWispsTable}); err != nil {
		return domain.ClaimRowResult{}, fmt.Errorf("db: Claim %s: record event: %w", id, err)
	}

	return domain.ClaimRowResult{
		Updated:          true,
		CurrentAssignee:  actor,
		CurrentStatus:    types.StatusInProgress,
		StartedAtWasZero: startedWasZero,
		OldIssue:         oldIssue,
	}, nil
}

func (r *issueSQLRepositoryImpl) Get(ctx context.Context, id string, opts domain.IssueTableOpts) (*types.Issue, error) {
	if id == "" {
		return nil, errors.New("db: Get: id must not be empty")
	}
	table := pickIssueTable(opts.UseWispsTable)
	//nolint:gosec // G201: table is one of two hardcoded constants
	row := r.runner.QueryRowContext(ctx, fmt.Sprintf("SELECT %s FROM %s WHERE id = ?", issueSelectColumns, table), id)
	issue, err := scanIssue(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, fmt.Errorf("db: Get %s: %w", id, err)
	}
	return issue, nil
}

func (r *issueSQLRepositoryImpl) GetByIDs(ctx context.Context, ids []string, opts domain.IssueTableOpts) ([]*types.Issue, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	table := pickIssueTable(opts.UseWispsTable)
	//nolint:gosec // G201: table is one of two hardcoded constants
	q := fmt.Sprintf("SELECT %s FROM %s WHERE id IN (%s)", issueSelectColumns, table, strings.Join(placeholders, ","))
	rows, err := r.runner.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("db: GetByIDs: %w", err)
	}
	defer rows.Close()

	var out []*types.Issue
	for rows.Next() {
		issue, err := scanIssue(rows)
		if err != nil {
			return nil, fmt.Errorf("db: GetByIDs: scan: %w", err)
		}
		out = append(out, issue)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: GetByIDs: rows: %w", err)
	}
	return out, nil
}

func (r *issueSQLRepositoryImpl) Exists(ctx context.Context, id string, opts domain.IssueTableOpts) (bool, error) {
	if id == "" {
		return false, errors.New("db: Exists: id must not be empty")
	}
	table := pickIssueTable(opts.UseWispsTable)
	//nolint:gosec // G201: table is one of two hardcoded constants
	row := r.runner.QueryRowContext(ctx, fmt.Sprintf("SELECT 1 FROM %s WHERE id = ? LIMIT 1", table), id)
	var one int
	err := row.Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("db: Exists %s: %w", id, err)
	}
	return true, nil
}

func (r *issueSQLRepositoryImpl) CountForPrefix(ctx context.Context, prefix string, opts domain.IssueTableOpts) (int, error) {
	if prefix == "" {
		return 0, errors.New("db: CountForPrefix: prefix must not be empty")
	}
	table := pickIssueTable(opts.UseWispsTable)
	var count int
	//nolint:gosec // G201: table is one of two hardcoded constants
	err := r.runner.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT COUNT(*)
		FROM %s
		WHERE id LIKE CONCAT(?, '-%%')
		  AND INSTR(SUBSTRING(id, LENGTH(?) + 2), '.') = 0
	`, table), prefix, prefix).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("db: CountForPrefix %s: %w", prefix, err)
	}
	return count, nil
}

func (r *issueSQLRepositoryImpl) NextCounterID(ctx context.Context, prefix string) (int, error) {
	if prefix == "" {
		return 0, errors.New("db: NextCounterID: prefix must not be empty")
	}

	res, err := r.runner.ExecContext(ctx, "UPDATE issue_counter SET last_id = last_id + 1 WHERE prefix = ?", prefix)
	if err != nil {
		return 0, fmt.Errorf("db: NextCounterID: increment %q: %w", prefix, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("db: NextCounterID: rows affected %q: %w", prefix, err)
	}

	if rows == 0 {
		if err := r.seedCounterFromExisting(ctx, prefix); err != nil {
			return 0, fmt.Errorf("db: NextCounterID: seed %q: %w", prefix, err)
		}
		res, err = r.runner.ExecContext(ctx, "UPDATE issue_counter SET last_id = last_id + 1 WHERE prefix = ?", prefix)
		if err != nil {
			return 0, fmt.Errorf("db: NextCounterID: increment after seed %q: %w", prefix, err)
		}
		rows, err = res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("db: NextCounterID: rows affected after seed %q: %w", prefix, err)
		}
		if rows == 0 {
			if _, err := r.runner.ExecContext(ctx, "INSERT INTO issue_counter (prefix, last_id) VALUES (?, 1)", prefix); err != nil {
				return 0, fmt.Errorf("db: NextCounterID: insert initial %q: %w", prefix, err)
			}
		}
	}

	var nextID int
	if err := r.runner.QueryRowContext(ctx, "SELECT last_id FROM issue_counter WHERE prefix = ?", prefix).Scan(&nextID); err != nil {
		return 0, fmt.Errorf("db: NextCounterID: read last_id %q: %w", prefix, err)
	}
	return nextID, nil
}

func (r *issueSQLRepositoryImpl) seedCounterFromExisting(ctx context.Context, prefix string) error {
	var existing int
	err := r.runner.QueryRowContext(ctx, "SELECT last_id FROM issue_counter WHERE prefix = ?", prefix).Scan(&existing)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read existing counter %q: %w", prefix, err)
	}

	rows, err := r.runner.QueryContext(ctx, "SELECT id FROM issues WHERE id LIKE CONCAT(?, '-%')", prefix)
	if err != nil {
		return fmt.Errorf("scan issues for %q: %w", prefix, err)
	}
	defer rows.Close()

	maxNum := 0
	pfxDash := prefix + "-"
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		suffix := strings.TrimPrefix(id, pfxDash)
		if strings.Contains(suffix, ".") {
			continue
		}
		if n, err := strconv.Atoi(suffix); err == nil && n > maxNum {
			maxNum = n
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate issues for %q: %w", prefix, err)
	}

	if maxNum > 0 {
		if _, err := r.runner.ExecContext(ctx, "INSERT INTO issue_counter (prefix, last_id) VALUES (?, ?)", prefix, maxNum); err != nil {
			return fmt.Errorf("seed counter %q at %d: %w", prefix, maxNum, err)
		}
	}
	return nil
}

func normalizeIssueTimestamps(issue *types.Issue) {
	// beads-82pv3: truncate to second precision (the DATETIME column width),
	// mirroring issueops.PrepareIssueForInsert (beads-17n4h/8ukct) on the
	// direct/embedded path. This is the shared domain insert chokepoint (Insert +
	// InsertBatch) used by proxied-server create, which emits the in-memory
	// result.Issue verbatim under --json — without truncation a relative
	// --due/--defer (ParseRelativeTime carries ns from time.Now()) or a ns
	// created/updated emitted ns while every later read returned the
	// second-truncated column: a read-after-write mismatch on the proxied twin
	// that 17n4h (issueops-only) did not cover.
	now := time.Now().UTC().Truncate(time.Second)
	if issue.CreatedAt.IsZero() {
		issue.CreatedAt = now
	} else {
		issue.CreatedAt = issue.CreatedAt.UTC().Truncate(time.Second)
	}
	if issue.UpdatedAt.IsZero() {
		issue.UpdatedAt = now
	} else {
		issue.UpdatedAt = issue.UpdatedAt.UTC().Truncate(time.Second)
	}
	if issue.DueAt != nil {
		truncated := issue.DueAt.UTC().Truncate(time.Second)
		issue.DueAt = &truncated
	}
	if issue.DeferUntil != nil {
		truncated := issue.DeferUntil.UTC().Truncate(time.Second)
		issue.DeferUntil = &truncated
	}
	// beads-kg5tf: closed_at and started_at are DATETIME columns too
	// (0001_create_issues.up.sql closed_at DATETIME; 0027 started_at DATETIME),
	// emitted under --json (types.go closed_at/started_at ,omitempty), but 82pv3
	// only added due/defer here. On the domain/proxied `bd create --status closed`
	// path, create() (issue.go) computes closed_at = maxTime.Add(second) from a
	// raw-ns CreatedAt (deliberately un-truncated there for GenerateHashID
	// stability) BEFORE reaching this chokepoint, so an un-truncated closed_at was
	// emitted verbatim while the DATETIME column stored second precision — the same
	// read-after-write mismatch 82pv3 fixed for due/defer. The direct path is clean
	// (PrepareIssueForInsert truncates CreatedAt before computing closed_at).
	// Truncate both at the shared insert chokepoint so the emit matches the column.
	if issue.ClosedAt != nil {
		truncated := issue.ClosedAt.UTC().Truncate(time.Second)
		issue.ClosedAt = &truncated
	}
	if issue.StartedAt != nil {
		truncated := issue.StartedAt.UTC().Truncate(time.Second)
		issue.StartedAt = &truncated
	}
}

func pickIssueTable(useWisps bool) string {
	if useWisps {
		return "wisps"
	}
	return "issues"
}

//nolint:gosec // G201: table is a hardcoded constant ("issues" or "wisps")
func insertIssueRow(ctx context.Context, runner Runner, table string, issue *types.Issue) error {
	_, err := runner.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (
			id, content_hash, title, description, design, acceptance_criteria, notes,
			status, priority, issue_type, assignee, estimated_minutes,
			created_at, created_by, owner, updated_at, started_at, closed_at, external_ref, spec_id,
			compaction_level, compacted_at, compacted_at_commit, original_size,
			sender, ephemeral, no_history, wisp_type, pinned, is_template,
			mol_type, work_type, source_system, source_repo, close_reason, closed_by_session,
			event_kind, actor, target, payload,
			await_type, await_id, timeout_ns, waiters, bonded_from,
			due_at, defer_until, metadata
		) VALUES (
			?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?, ?,
			?, ?, ?
		)
		ON DUPLICATE KEY UPDATE
			%s
	`, table, issueops.IssueUpsertAssignments(false)),
		issue.ID, issue.ContentHash, issue.Title, issue.Description, issue.Design, issue.AcceptanceCriteria, issue.Notes,
		string(issue.Status), issue.Priority, string(issue.IssueType), nullString(issue.Assignee), nullIntPtr(issue.EstimatedMinutes),
		issue.CreatedAt, issue.CreatedBy, issue.Owner, issue.UpdatedAt, issue.StartedAt, issue.ClosedAt, nullStringPtr(issue.ExternalRef), issue.SpecID,
		issue.CompactionLevel, issue.CompactedAt, nullStringPtr(issue.CompactedAtCommit), nullIntVal(issue.OriginalSize),
		issue.Sender, issue.Ephemeral, issue.NoHistory, string(issue.WispType), issue.Pinned, issue.IsTemplate,
		string(issue.MolType), string(issue.WorkType), issue.SourceSystem, issue.SourceRepo, issue.CloseReason, issue.ClosedBySession,
		issue.EventKind, issue.Actor, issue.Target, issue.Payload,
		issue.AwaitType, issue.AwaitID, issue.Timeout.Nanoseconds(), formatJSONStringArray(issue.Waiters), issueops.FormatBondedFrom(issue.BondedFrom),
		issue.DueAt, issue.DeferUntil, jsonMetadata(issue.Metadata),
	)
	if err != nil {
		return fmt.Errorf("db: insert into %s: %w", table, err)
	}
	return nil
}

type issueScanner interface {
	Scan(dest ...any) error
}

// scanIssue delegates to the classic scan so both stacks hydrate issues with
// identical semantics (bd-6dnrw.44 item 12, extract-don't-duplicate per .46).
// The shared scan reads created_at/updated_at as strings with format
// fallbacks where a hand-rolled sql.NullTime scan hard-fails on any driver
// that hands timestamps back as text.
func scanIssue(s issueScanner) (*types.Issue, error) {
	return issueops.ScanIssueFrom(s)
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullStringPtr(s *string) any {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}

func nullIntPtr(i *int) any {
	if i == nil {
		return nil
	}
	return *i
}

func nullIntVal(i int) any {
	if i == 0 {
		return nil
	}
	return i
}

func jsonMetadata(raw json.RawMessage) any {
	if len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}

func formatJSONStringArray(items []string) string {
	if len(items) == 0 {
		return ""
	}
	b, err := json.Marshal(items)
	if err != nil {
		return ""
	}
	return string(b)
}

var timestampUpdateFields = map[string]struct{}{
	"started_at": {}, "closed_at": {}, "due_at": {}, "defer_until": {},
}

func normalizeUpdateValue(key string, value any) any {
	if _, ok := timestampUpdateFields[key]; ok {
		switch v := value.(type) {
		case time.Time:
			return v.UTC()
		case *time.Time:
			if v == nil {
				return nil
			}
			t := v.UTC()
			return t
		}
		return value
	}
	switch key {
	case "status":
		if s, ok := value.(types.Status); ok {
			return string(s)
		}
	case "issue_type":
		if t, ok := value.(types.IssueType); ok {
			return string(t)
		}
	case "metadata":
		switch v := value.(type) {
		case json.RawMessage:
			return string(v)
		case []byte:
			return string(v)
		}
	case "waiters":
		// waiters is stored as a JSON-string-array TEXT column. Mirror the
		// direct/embedded update path (issueops/update.go), which json.Marshals
		// the []string before binding — the domain/proxied path previously bound
		// the raw []string and failed "unsupported type []string" (beads-qppc,
		// surfaced by proxied `bd gate add-waiter`). Same domain-vs-direct
		// UPDATE-path asymmetry class as beads-h3iv/hfb4/n79c/lsbu.
		switch v := value.(type) {
		case []string:
			b, _ := json.Marshal(v)
			return string(b)
		case []byte:
			return string(v)
		case json.RawMessage:
			return string(v)
		}
	case "bonded_from":
		// beads-ijzkb: bonded_from is a JSON array of BondRef stored as TEXT.
		// Marshal the []types.BondRef before binding, mirroring waiters — the
		// domain/proxied path would otherwise bind the raw slice and fail
		// "unsupported type" (the beads-qppc waiters class).
		switch v := value.(type) {
		case string:
			return v
		case []byte:
			return string(v)
		case json.RawMessage:
			return string(v)
		default:
			b, _ := json.Marshal(value)
			return string(b)
		}
	}
	return value
}

func (r *issueSQLRepositoryImpl) SearchAcrossIssuesAndWisps(ctx context.Context, query string, filter types.IssueFilter) (domain.SearchPage, error) {
	return r.searchAcrossIssuesAndWisps(ctx, query, filter)
}

func (r *issueSQLRepositoryImpl) SearchAcrossIssuesAndWispsWithCounts(ctx context.Context, query string, filter types.IssueFilter) (domain.SearchCountsPage, error) {
	return r.searchAcrossIssuesAndWispsWithCounts(ctx, query, filter)
}

func (r *issueSQLRepositoryImpl) GetReadyWork(ctx context.Context, filter types.WorkFilter) (domain.SearchPage, error) {
	return r.getReadyWorkUnion(ctx, filter)
}

func (r *issueSQLRepositoryImpl) GetReadyWorkWithCounts(ctx context.Context, filter types.WorkFilter) (domain.SearchCountsPage, error) {
	return r.getReadyWorkWithCountsUnion(ctx, filter)
}

func (r *issueSQLRepositoryImpl) Delete(ctx context.Context, id string, opts domain.IssueTableOpts) error {
	table := "issues"
	if opts.UseWispsTable {
		table = "wisps"
	}
	//nolint:gosec // G201: table is a hardcoded constant.
	res, err := r.runner.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE id = ?", table), id)
	if err != nil {
		return fmt.Errorf("db: IssueSQLRepository.Delete %s from %s: %w", id, table, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("db: IssueSQLRepository.Delete rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("issue not found: %s", id)
	}
	return nil
}

func (r *issueSQLRepositoryImpl) DeleteByIDs(ctx context.Context, ids []string, opts domain.IssueTableOpts) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	table := "issues"
	if opts.UseWispsTable {
		table = "wisps"
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
		//nolint:gosec // G201: table is a hardcoded constant; placeholders are ?.
		res, err := r.runner.ExecContext(ctx,
			fmt.Sprintf("DELETE FROM %s WHERE id IN (%s)", table, strings.Join(placeholders, ",")),
			args...)
		if err != nil {
			return total, fmt.Errorf("db: IssueSQLRepository.DeleteByIDs from %s: %w", table, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("db: IssueSQLRepository.DeleteByIDs rows affected: %w", err)
		}
		total += int(n)
	}
	return total, nil
}

func (r *issueSQLRepositoryImpl) PartitionWispIDs(ctx context.Context, ids []string) ([]string, []string, error) {
	return issueops.PartitionWispIDsInTx(ctx, r.runner, ids)
}

func (r *issueSQLRepositoryImpl) FindAllDependents(ctx context.Context, ids []string) ([]string, error) {
	set, err := issueops.FindAllDependentsInTx(ctx, r.runner, ids)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	return out, nil
}

func (r *issueSQLRepositoryImpl) AffectedByDeletion(ctx context.Context, issueIDs, wispIDs []string) ([]string, []string, error) {
	return issueops.AffectedByDeletionInTx(ctx, r.runner, issueIDs, wispIDs)
}

func (r *issueSQLRepositoryImpl) RecomputeIsBlocked(ctx context.Context, issueIDs, wispIDs []string) error {
	return issueops.RecomputeIsBlockedInTx(ctx, r.runner, issueIDs, wispIDs)
}

func (r *issueSQLRepositoryImpl) AsOf(ctx context.Context, id, ref string) (*types.Issue, error) {
	return issueops.AsOfInTx(ctx, r.runner, id, ref)
}

func (r *issueSQLRepositoryImpl) History(ctx context.Context, id string) ([]*storage.HistoryEntry, error) {
	return issueops.HistoryInTx(ctx, r.runner, id)
}

func (r *issueSQLRepositoryImpl) Diff(ctx context.Context, fromRef, toRef string) ([]*storage.DiffEntry, error) {
	return issueops.DiffInTx(ctx, r.runner, fromRef, toRef)
}

func (r *issueSQLRepositoryImpl) GetEpicsEligibleForClosure(ctx context.Context) ([]*types.EpicStatus, error) {
	return issueops.GetEpicsEligibleForClosureInTx(ctx, r.runner)
}

func (r *issueSQLRepositoryImpl) GetStaleIssues(ctx context.Context, filter types.StaleFilter) ([]*types.Issue, error) {
	return issueops.GetStaleIssuesInTx(ctx, r.runner, filter)
}

func (r *issueSQLRepositoryImpl) UpdateIssueID(ctx context.Context, oldID, newID string, issue *types.Issue, actor string) error {
	return issueops.UpdateIssueIDInTx(ctx, r.runner, oldID, newID, issue, actor)
}

func (r *issueSQLRepositoryImpl) PromoteFromEphemeral(ctx context.Context, id, actor string) error {
	return issueops.PromoteFromEphemeralInTx(ctx, r.runner, id, actor)
}

func (r *issueSQLRepositoryImpl) CheckCompactionEligibility(ctx context.Context, issueID string, tier int) (bool, string, error) {
	return issueops.CheckEligibilityInTx(ctx, r.runner, issueID, tier)
}

func (r *issueSQLRepositoryImpl) GetTier1CompactionCandidates(ctx context.Context) ([]*types.CompactionCandidate, error) {
	return issueops.GetTier1CandidatesInTx(ctx, r.runner)
}

func (r *issueSQLRepositoryImpl) GetTier2CompactionCandidates(ctx context.Context) ([]*types.CompactionCandidate, error) {
	return issueops.GetTier2CandidatesInTx(ctx, r.runner)
}

func (r *issueSQLRepositoryImpl) SnapshotIssueForCompaction(ctx context.Context, issueID string, tier int) error {
	return issueops.SnapshotIssueInTx(ctx, r.runner, issueID, tier)
}

func (r *issueSQLRepositoryImpl) CompactOverwrite(ctx context.Context, issueID string, updates map[string]interface{}, tier, originalSize int, commitHash, actor string) error {
	return issueops.CompactOverwriteInTx(ctx, r.runner, issueID, updates, tier, originalSize, commitHash, actor)
}

func (r *issueSQLRepositoryImpl) GetNextChildID(ctx context.Context, parentID string) (string, error) {
	return issueops.GetNextChildIDTx(ctx, r.runner, parentID)
}

func (r *issueSQLRepositoryImpl) GetIDsByLabel(ctx context.Context, label string) ([]string, error) {
	return issueops.GetIssuesByLabelInTx(ctx, r.runner, label)
}

func (r *issueSQLRepositoryImpl) Close(ctx context.Context, id string, params domain.CloseRowParams, actor string, opts domain.IssueTableOpts) (domain.CloseRowResult, error) {
	res, err := issueops.CloseIssueInTx(ctx, r.runner, id, params.Reason, actor, params.Session)
	if err != nil {
		return domain.CloseRowResult{}, fmt.Errorf("db: IssueSQLRepository.Close %s: %w", id, err)
	}
	return domain.CloseRowResult{
		Updated:       !res.AlreadyClosed,
		AlreadyClosed: res.AlreadyClosed,
		IsWisp:        res.IsWisp,
	}, nil
}

func (r *issueSQLRepositoryImpl) Reopen(ctx context.Context, id string, params domain.ReopenRowParams, actor string, opts domain.IssueTableOpts) (domain.ReopenRowResult, error) {
	res, err := issueops.ReopenIssueInTx(ctx, r.runner, id, params.Reason, actor)
	if err != nil {
		return domain.ReopenRowResult{}, fmt.Errorf("db: IssueSQLRepository.Reopen %s: %w", id, err)
	}
	return domain.ReopenRowResult{
		Updated:     !res.AlreadyOpen,
		AlreadyOpen: res.AlreadyOpen,
		IsWisp:      res.IsWisp,
	}, nil
}

func (r *issueSQLRepositoryImpl) GetNewlyUnblockedByClose(ctx context.Context, closedID string) ([]*types.Issue, error) {
	out, err := issueops.GetNewlyUnblockedByCloseInTx(ctx, r.runner, closedID)
	if err != nil {
		return nil, fmt.Errorf("db: IssueSQLRepository.GetNewlyUnblockedByClose %s: %w", closedID, err)
	}
	return out, nil
}

func (r *issueSQLRepositoryImpl) ClaimReadyIssue(ctx context.Context, filter types.WorkFilter, actor string) (*types.Issue, error) {
	out, err := issueops.ClaimReadyIssueInTx(ctx, r.runner, filter, actor)
	if err != nil {
		return nil, fmt.Errorf("db: IssueSQLRepository.ClaimReadyIssue: %w", err)
	}
	return out, nil
}

func (r *issueSQLRepositoryImpl) ClaimReadyWisp(ctx context.Context, filter types.WorkFilter, actor string) (*types.Issue, error) {
	out, err := issueops.ClaimReadyIssueInTx(ctx, r.runner, filter, actor)
	if err != nil {
		return nil, fmt.Errorf("db: IssueSQLRepository.ClaimReadyWisp: %w", err)
	}
	return out, nil
}

func (r *issueSQLRepositoryImpl) GetBlockedIssues(ctx context.Context, filter types.WorkFilter) ([]*types.BlockedIssue, error) {
	out, err := issueops.GetBlockedIssuesInTx(ctx, r.runner, filter)
	if err != nil {
		return nil, fmt.Errorf("db: IssueSQLRepository.GetBlockedIssues: %w", err)
	}
	return out, nil
}

func (r *issueSQLRepositoryImpl) GetStatistics(ctx context.Context) (*types.Statistics, error) {
	stats := &types.Statistics{}
	if err := issueops.ScanIssueCountsInTx(ctx, r.runner, stats); err != nil {
		return nil, fmt.Errorf("db: IssueSQLRepository.GetStatistics: scan counts: %w", err)
	}
	var blocked int
	if err := r.runner.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM issues
		WHERE is_blocked = 1 AND status <> 'closed' AND status <> 'pinned'
	`).Scan(&blocked); err != nil {
		return nil, fmt.Errorf("db: IssueSQLRepository.GetStatistics: count blocked: %w", err)
	}
	stats.BlockedIssues = blocked
	// beads-phoh: count ready work through the shared bd-ready predicate
	// (identity type/label exclusions included) so `bd stats` ready_issues
	// matches `bd ready` instead of overcounting unblocked identity beads.
	readyCount, rerr := issueops.CountReadyWorkInTx(ctx, r.runner, issueops.StatsReadyWorkFilter())
	if rerr != nil {
		return nil, fmt.Errorf("db: IssueSQLRepository.GetStatistics: count ready: %w", rerr)
	}
	stats.ReadyIssues = readyCount
	// beads-13xl: populate the two Statistics fields that were declared +
	// rendered but never assigned (permanent 0), so `bd stats` agrees with
	// `bd epic status` and stops silently lying about lead time.
	epicCount, eerr := issueops.CountEpicsEligibleForClosureInTx(ctx, r.runner)
	if eerr != nil {
		return nil, fmt.Errorf("db: IssueSQLRepository.GetStatistics: count epics eligible: %w", eerr)
	}
	stats.EpicsEligibleForClosure = epicCount
	if lerr := issueops.ScanAverageLeadTimeInTx(ctx, r.runner, stats); lerr != nil {
		return nil, fmt.Errorf("db: IssueSQLRepository.GetStatistics: %w", lerr)
	}
	return stats, nil
}

func (r *issueSQLRepositoryImpl) CountIssues(ctx context.Context, query string, filter types.IssueFilter) (int64, error) {
	n, err := issueops.CountIssuesInTx(ctx, r.runner, query, filter)
	if err != nil {
		return 0, err
	}
	return int64(n), nil
}

func (r *issueSQLRepositoryImpl) CountIssuesByGroup(ctx context.Context, filter types.IssueFilter, groupBy string) (map[string]int, error) {
	return issueops.CountIssuesByGroupInTx(ctx, r.runner, filter, groupBy)
}

const deleteBatchSize = 200
