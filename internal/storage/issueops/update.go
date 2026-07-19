package issueops

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// validatePriorityUpdate enforces the priority 0-4 invariant at the shared
// update write path. The CLI update command routes priority through
// validation.ValidatePriority, but the batch, graph-apply, and proxied-handler
// paths build the updates map directly, so an out-of-range or malformed value
// would otherwise be written to the DB — bypassing the invariant that
// Issue.Validate enforces on every create (beads-r06.11).
//
// When present and valid, the value is normalized to int so the SQL bind is a
// clean integer regardless of whether the caller supplied int/int64/float64
// (JSON decoders deliver float64). A missing "priority" key is a no-op.
func validatePriorityUpdate(updates map[string]interface{}) error {
	raw, ok := updates["priority"]
	if !ok {
		return nil
	}
	var p int
	switch v := raw.(type) {
	case int:
		p = v
	case int64:
		p = int(v)
	case float64:
		// JSON numbers arrive as float64; reject non-integral values rather
		// than silently truncating (e.g. 2.5 is not a valid priority).
		if v != float64(int(v)) {
			return fmt.Errorf("invalid priority %v (expected an integer 0-4)", v)
		}
		p = int(v)
	default:
		return fmt.Errorf("invalid priority %v (expected an integer 0-4, got %T)", raw, raw)
	}
	if p < 0 || p > 4 {
		return fmt.Errorf("invalid priority %d (must be between 0 and 4)", p)
	}
	updates["priority"] = p
	return nil
}

// validateTitleUpdate enforces the title invariant (required, <=500 chars) and
// rejects a non-string value on the shared update path. Handed off from
// beads-25k6 (eng_6) and folded into the 2p6x umbrella: this catches bad INPUT
// TYPES (e.g. title:42) before the SQL layer silently coerces them, complementing
// finalizeUpdatedIssueInTx which validates the merged persisted result.
func validateTitleUpdate(updates map[string]interface{}) error {
	raw, ok := updates["title"]
	if !ok {
		return nil
	}
	title, ok := raw.(string)
	if !ok {
		return fmt.Errorf("invalid title %v (expected a string, got %T)", raw, raw)
	}
	if len(title) == 0 {
		return fmt.Errorf("title is required")
	}
	if len(title) > 500 {
		return fmt.Errorf("title must be 500 characters or less (got %d)", len(title))
	}
	return nil
}

// validateEstimatedMinutesUpdate enforces estimated_minutes >= 0 and rejects a
// non-integer value on the shared update path (beads-25k6, eng_6 → 2p6x). Like
// validatePriorityUpdate it normalizes int/int64/float64 and rejects a
// non-integral float rather than silently truncating (2.5 -> 2). nil clears it.
func validateEstimatedMinutesUpdate(updates map[string]interface{}) error {
	raw, ok := updates["estimated_minutes"]
	if !ok || raw == nil {
		return nil
	}
	var m int
	switch v := raw.(type) {
	case int:
		m = v
	case int64:
		m = int(v)
	case float64:
		if v != float64(int(v)) {
			return fmt.Errorf("invalid estimated_minutes %v (expected an integer)", v)
		}
		m = int(v)
	default:
		return fmt.Errorf("invalid estimated_minutes %v (expected an integer, got %T)", raw, raw)
	}
	if m < 0 {
		return fmt.Errorf("estimated_minutes cannot be negative")
	}
	return nil
}

// ValidateUpdateInputs runs the input-type guards (priority range, title
// required/≤500, estimated_minutes ≥0 integer) that updateIssueInTx applies
// before the SQL layer silently coerces a bad value. Exported so the raw
// domain/db update path (issueSQLRepositoryImpl.Update with Finalize) can run
// the same guards instead of leaking a raw Dolt column error (beads-iu9f Phase
// B / 25k6). It mirrors the pre-SQL checks in updateIssueInTx.
func ValidateUpdateInputs(updates map[string]interface{}) error {
	if err := validatePriorityUpdate(updates); err != nil {
		return err
	}
	if err := validateTitleUpdate(updates); err != nil {
		return err
	}
	return validateEstimatedMinutesUpdate(updates)
}

// FinalizeUpdatedIssueInTx is the exported entry to finalizeUpdatedIssueInTx for
// the raw domain/db update path (issueSQLRepositoryImpl.Update with Finalize),
// so proxied/domain updates get the same post-update ValidateWithCustom +
// metadata-object check + content_hash recompute as the shared seam.
func FinalizeUpdatedIssueInTx(ctx context.Context, tx DBTX, issueTable, id string) error {
	return finalizeUpdatedIssueInTx(ctx, tx, issueTable, id)
}

// finalizeUpdatedIssueInTx re-runs the create-path finalization on the merged
// post-update issue: full Issue.ValidateWithCustom (title/priority/status/type/
// estimated_minutes/closed_at/metadata invariants) + content_hash recompute.
// It re-reads inside the transaction so it sees the exact persisted state
// (including auto-managed closed_at/started_at/pinned). A validation failure
// returns an error, which rolls back the caller's transaction (beads-2p6x).
func finalizeUpdatedIssueInTx(ctx context.Context, tx DBTX, issueTable, id string) error {
	updated, err := GetIssueInTx(ctx, tx, id)
	if err != nil {
		return fmt.Errorf("failed to re-read issue for finalization: %w", err)
	}
	if updated == nil {
		return nil // nothing persisted (e.g. no-op); leave as-is
	}

	statuses, customTypes, err := ResolveCustomConfigInTx(ctx, tx)
	if err != nil {
		return fmt.Errorf("failed to resolve custom config for update validation: %w", err)
	}
	customStatuses := make([]string, len(statuses))
	for i, s := range statuses {
		customStatuses[i] = s.Name
	}
	if err := updated.ValidateWithCustom(customStatuses, customTypes); err != nil {
		return fmt.Errorf("update would violate issue invariants: %w", err)
	}
	// Metadata must be a JSON OBJECT, not just valid JSON (beads-lsbu).
	// ValidateWithCustom only checks json.Valid, so an array/scalar ("[1,2]",
	// "42") slips past it — but every metadata edit path (applyMetadataEdits/
	// mergeMetadata) unmarshals into map[string]json.RawMessage and hard-errors
	// on a non-object, permanently locking the issue out of future edits. Reject
	// it here so a non-object never lands via the shared write path.
	if len(updated.Metadata) > 0 && !metadataIsJSONObjectRaw(updated.Metadata) {
		return fmt.Errorf("update would violate issue invariants: metadata must be a JSON object (arrays and scalars can't be edited by --set-metadata/--unset-metadata)")
	}

	// Recompute the content hash from the post-update content so it stays a
	// faithful cross-clone content fingerprint after edits.
	newHash := updated.ComputeContentHash()
	if newHash != updated.ContentHash {
		//nolint:gosec // G201: issueTable is a WispTableRouting constant
		if _, err := tx.ExecContext(ctx,
			fmt.Sprintf("UPDATE %s SET content_hash = ? WHERE id = ?", issueTable),
			newHash, id); err != nil {
			return fmt.Errorf("failed to update content_hash: %w", err)
		}
	}
	return nil
}

// metadataIsJSONObjectRaw reports whether raw metadata is a JSON object (or
// empty/null, treated as an empty object). Arrays and scalars are rejected —
// they are valid JSON but cannot be edited by the map-based metadata edit paths.
func metadataIsJSONObjectRaw(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return true
	}
	var obj map[string]json.RawMessage
	return json.Unmarshal([]byte(trimmed), &obj) == nil
}

// IsAllowedUpdateField checks if a field name is valid for issue updates.
func IsAllowedUpdateField(key string) bool {
	allowed := map[string]bool{
		"status": true, "priority": true, "title": true, "assignee": true,
		"description": true, "design": true, "acceptance_criteria": true, "notes": true,
		"issue_type": true, "estimated_minutes": true, "external_ref": true, "spec_id": true,
		"started_at": true,
		"closed_at":  true, "close_reason": true, "closed_by_session": true,
		"source_repo": true,
		"sender":      true, "wisp": true, "wisp_type": true, "no_history": true, "pinned": true,
		"mol_type":       true,
		"event_category": true, "event_actor": true, "event_target": true, "event_payload": true,
		"due_at": true, "defer_until": true, "await_id": true, "waiters": true,
		"metadata": true,
	}
	return allowed[key]
}

// ManageClosedAt auto-sets closed_at when closing or clears it when reopening.
func ManageClosedAt(oldIssue *types.Issue, updates map[string]interface{}, setClauses []string, args []interface{}) ([]string, []interface{}) {
	statusVal, hasStatus := updates["status"]
	_, hasExplicitClosedAt := updates["closed_at"]
	if hasExplicitClosedAt || !hasStatus {
		return setClauses, args
	}

	var newStatus string
	switch v := statusVal.(type) {
	case string:
		newStatus = v
	case types.Status:
		newStatus = string(v)
	default:
		return setClauses, args
	}

	if newStatus == string(types.StatusClosed) {
		// Preserve an existing closed_at on a re-close of an already-closed
		// issue — symmetric with ManageStartedAt's started_at preservation.
		// Without this guard, `bd update --status closed` on an already-closed
		// issue silently overwrote the original close timestamp with now
		// (beads-b1l7). (CloseIssueInTx already early-returns on AlreadyClosed;
		// this closes the plain-update-path hole.) When already closed, leave
		// closed_at untouched — do NOT fall through to the reopen-clear branch.
		if oldIssue.ClosedAt == nil {
			now := time.Now().UTC()
			setClauses = append(setClauses, "closed_at = ?")
			args = append(args, now)

			// beads-6qo8t: default close_reason to "Closed" on the OPEN→closed
			// transition, mirroring `bd close` (close.go: reasons default to
			// {"Closed"} when none given). `bd update --status closed` otherwise
			// left close_reason NULL — a field-parity gap vs `bd close`, despite
			// update's help claiming it mirrors `bd close --force`. Only defaults
			// when the caller did not set close_reason explicitly (e.g. a future
			// --reason flag), and only on a fresh close (oldIssue.ClosedAt == nil,
			// same guard as closed_at) so a re-close/no-op never clobbers an
			// existing reason. Applying it at this shared seam (not the update.go
			// CLI) keeps the direct AND proxied paths at parity — the proxied
			// server path goes through domain/db/issue.go, not update.go's RunE.
			if _, hasExplicitReason := updates["close_reason"]; !hasExplicitReason {
				setClauses = append(setClauses, "close_reason = ?")
				args = append(args, "Closed")
			}
		}
	} else if oldIssue.Status == types.StatusClosed {
		setClauses = append(setClauses, "closed_at = ?", "close_reason = ?")
		args = append(args, nil, "")
	}

	return setClauses, args
}

// ManageStartedAt auto-sets started_at when transitioning to in_progress.
// If the issue already has a started_at, it is preserved (not overwritten).
func ManageStartedAt(oldIssue *types.Issue, updates map[string]interface{}, setClauses []string, args []interface{}) ([]string, []interface{}) {
	statusVal, hasStatus := updates["status"]
	_, hasExplicitStartedAt := updates["started_at"]
	if hasExplicitStartedAt || !hasStatus {
		return setClauses, args
	}

	var newStatus string
	switch v := statusVal.(type) {
	case string:
		newStatus = v
	case types.Status:
		newStatus = string(v)
	default:
		return setClauses, args
	}

	if newStatus == string(types.StatusInProgress) && oldIssue.StartedAt == nil {
		now := time.Now().UTC()
		setClauses = append(setClauses, "started_at = ?")
		args = append(args, now)
	}

	return setClauses, args
}

// DetermineEventType returns the appropriate event type for an update.
func DetermineEventType(oldIssue *types.Issue, updates map[string]interface{}) types.EventType {
	statusVal, hasStatus := updates["status"]
	if !hasStatus {
		return types.EventUpdated
	}

	var newStatus string
	switch v := statusVal.(type) {
	case string:
		newStatus = v
	case types.Status:
		newStatus = string(v)
	default:
		return types.EventUpdated
	}

	if newStatus == string(types.StatusClosed) {
		return types.EventClosed
	}
	if oldIssue.Status == types.StatusClosed {
		return types.EventReopened
	}
	return types.EventStatusChanged
}

// UpdateResult holds the result of an UpdateIssueInTx call.
type UpdateResult struct {
	OldIssue *types.Issue
	IsWisp   bool
}

// UpdateIssueInTx performs the full update SQL logic within a transaction.
// It routes to the correct table (issues/wisps) automatically.
// The caller is responsible for Dolt versioning (DOLT_ADD/COMMIT) if needed.
//
//nolint:gosec // G201: table names come from WispTableRouting (hardcoded constants)
func UpdateIssueInTx(ctx context.Context, tx DBTX, id string, updates map[string]interface{}, actor string) (*UpdateResult, error) {
	return updateIssueInTx(ctx, tx, id, updates, actor, true)
}

// UpdateIssueWithoutEventInTx applies normal update semantics without recording
// an intermediate event. Demotion uses this to preserve the historical event
// stream: create/update history is copied, then a single demotion event is added.
func UpdateIssueWithoutEventInTx(ctx context.Context, tx DBTX, id string, updates map[string]interface{}, actor string) (*UpdateResult, error) {
	return updateIssueInTx(ctx, tx, id, updates, actor, false)
}

func updateIssueInTx(ctx context.Context, tx DBTX, id string, updates map[string]interface{}, actor string, recordEvent bool) (*UpdateResult, error) {
	// Route to correct table.
	isWisp := IsActiveWispInTx(ctx, tx, id)
	issueTable, _, eventTable, _ := WispTableRouting(isWisp)

	// Validate priority range before any DB work (beads-r06.11). Callers that
	// build the updates map directly (batch/graph-apply/proxied handlers) do not
	// route through validation.ValidatePriority, so this shared guard keeps the
	// 0-4 invariant that Issue.Validate enforces on create.
	if err := validatePriorityUpdate(updates); err != nil {
		return nil, err
	}
	// Input-type guards for title/estimate (beads-25k6, folded into 2p6x): reject
	// a bad value TYPE before the SQL layer silently coerces it (e.g. title:42 →
	// "42", estimate:2.5 → 2). finalizeUpdatedIssueInTx below then validates the
	// merged persisted result (full ValidateWithCustom + content_hash).
	if err := validateTitleUpdate(updates); err != nil {
		return nil, err
	}
	if err := validateEstimatedMinutesUpdate(updates); err != nil {
		return nil, err
	}

	// Read old issue inside the transaction for consistency.
	oldIssue, err := GetIssueInTx(ctx, tx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get issue for update: %w", err)
	}

	// Validate issue_type against built-in + custom types (GH#3030).
	// This mirrors the create path (PrepareIssueForInsert → ValidateWithCustom)
	// and reads custom types from the same transaction, so it works reliably
	// even in subprocess contexts where the CLI-level store may be unavailable.
	if rawType, ok := updates["issue_type"]; ok {
		if issueType, ok := rawType.(string); ok {
			customTypes, err := ResolveCustomTypesInTx(ctx, tx)
			if err != nil {
				return nil, fmt.Errorf("failed to get custom types for validation: %w", err)
			}
			if !types.IssueType(issueType).IsValidWithCustom(customTypes) {
				return nil, fmt.Errorf("invalid issue type: %s", issueType)
			}
		}
	}

	// Build SET clauses.
	setClauses := []string{"updated_at = ?"}
	args := []interface{}{time.Now().UTC()}

	for key, value := range updates {
		if !IsAllowedUpdateField(key) {
			return nil, fmt.Errorf("invalid field for update: %s", key)
		}

		columnName := key
		if key == "wisp" {
			columnName = "ephemeral"
		}
		setClauses = append(setClauses, fmt.Sprintf("`%s` = ?", columnName))

		// Handle JSON serialization for array fields stored as TEXT.
		if key == "waiters" {
			waitersJSON, _ := json.Marshal(value)
			args = append(args, string(waitersJSON))
		} else if key == "metadata" {
			metadataStr, err := storage.NormalizeMetadataValue(value)
			if err != nil {
				return nil, fmt.Errorf("invalid metadata: %w", err)
			}
			args = append(args, metadataStr)
		} else {
			args = append(args, value)
		}
	}

	// Auto-clear pinned column when status transitions away from "pinned".
	if rawStatus, ok := updates["status"]; ok {
		var statusStr string
		switch v := rawStatus.(type) {
		case string:
			statusStr = v
		case types.Status:
			statusStr = string(v)
		}
		if oldIssue.Pinned && statusStr != string(types.StatusPinned) {
			if _, alreadySet := updates["pinned"]; !alreadySet {
				setClauses = append(setClauses, "`pinned` = ?")
				args = append(args, false)
			}
		}
	}

	// Auto-manage closed_at (set on close, clear on reopen).
	setClauses, args = ManageClosedAt(oldIssue, updates, setClauses, args)

	// Auto-manage started_at (set on transition to in_progress). (GH#2796)
	setClauses, args = ManageStartedAt(oldIssue, updates, setClauses, args)

	args = append(args, id)

	//nolint:gosec // G201: issueTable comes from WispTableRouting (hardcoded constants)
	query := fmt.Sprintf("UPDATE %s SET %s WHERE id = ?", issueTable, strings.Join(setClauses, ", "))
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return nil, fmt.Errorf("failed to update issue: %w", err)
	}

	// Finalize the update the same way create does (beads-2p6x): re-run the full
	// Issue.ValidateWithCustom on the merged post-update state and recompute the
	// content hash. Before this, updateIssueInTx guarded only priority + issue_type,
	// so the shared write path (batch/graph-apply/proxied-server/domain/programmatic
	// — everything that doesn't route through the CLI's own guards) accepted an
	// empty/>500 title (beads-25k6), a negative estimated_minutes (beads-25k6), and
	// a non-object metadata (beads-lsbu), and left content_hash stale after any
	// content change (beads-rzx8). Re-reading inside the tx reflects auto-managed
	// columns (closed_at/started_at/pinned) so the validation and the hash match
	// exactly what was persisted; a validation failure returns an error and the
	// caller's transaction rolls back, so the invalid write never commits.
	if err := finalizeUpdatedIssueInTx(ctx, tx, issueTable, id); err != nil {
		return nil, err
	}

	if recordEvent {
		oldData, _ := json.Marshal(oldIssue)
		newData, _ := json.Marshal(updates)
		eventType := DetermineEventType(oldIssue, updates)

		if err := RecordFullEventInTable(ctx, tx, eventTable, id, eventType, actor, string(oldData), string(newData)); err != nil {
			return nil, fmt.Errorf("failed to record event: %w", err)
		}
	}

	if rawStatus, hasStatus := updates["status"]; hasStatus {
		var newStatus string
		switch v := rawStatus.(type) {
		case string:
			newStatus = v
		case types.Status:
			newStatus = string(v)
		}
		oldActive := oldIssue.Status != types.StatusClosed && oldIssue.Status != types.StatusPinned
		newActive := newStatus != string(types.StatusClosed) && newStatus != string(types.StatusPinned)
		if oldActive != newActive {
			var affectedIssues, affectedWisps []string
			var aerr error
			if isWisp {
				affectedIssues, affectedWisps, aerr = AffectedByStatusChangeForWispInTx(ctx, tx, id)
			} else {
				affectedIssues, affectedWisps, aerr = AffectedByStatusChangeInTx(ctx, tx, id)
			}
			if aerr != nil {
				return nil, fmt.Errorf("affected by status change for %s: %w", id, aerr)
			}
			if err := RecomputeIsBlockedInTx(ctx, tx, affectedIssues, affectedWisps); err != nil {
				return nil, fmt.Errorf("recompute is_blocked after status change for %s: %w", id, err)
			}
		}
	}

	return &UpdateResult{OldIssue: oldIssue, IsWisp: isWisp}, nil
}

// RecordFullEventInTable records an event with both old and new values.
//
//nolint:gosec // G201: table is from WispTableRouting ("events" or "wisp_events")
func RecordFullEventInTable(ctx context.Context, tx DBTX, table, issueID string, eventType types.EventType, actor, oldValue, newValue string) error {
	_, err := tx.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (id, issue_id, event_type, actor, old_value, new_value)
		VALUES (?, ?, ?, ?, ?, ?)
	`, table), NewEventID(), issueID, eventType, actor, oldValue, newValue)
	if err != nil {
		return fmt.Errorf("record event in %s: %w", table, err)
	}
	return nil
}
