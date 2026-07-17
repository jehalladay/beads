package main

import (
	"fmt"

	"github.com/steveyegge/beads/internal/audit"
	"github.com/steveyegge/beads/internal/types"
)

// auditedUpdateFields are the issue fields whose changes are recorded in the
// GC-survivable audit-file trail (.beads/interactions.jsonl). The trail exists
// specifically because it survives a Dolt GC flatten, which destroys commit
// history — so a status/assignee/priority change stays in the durable record
// even after the DB event rows are flattened away.
//
// This is the SINGLE definition of what the audit-file trail captures. Every
// status/field-mutating command (update, close, reopen, defer) routes its audit
// emission through auditIssueUpdate below, so no handler can silently drop a
// field the way CLI reopen + defer historically did (beads-n4sn). It lives at
// the cmd layer (not the issueops SQL-tx core) on purpose: audit.LogFieldChange
// writes a cwd-based file via beads.FindBeadsDir(), which is a client/cmd
// concern — the tx core is shared by the proxied server and internal
// batch/graph-apply paths where cwd is not the user's repo.

// auditIssueUpdate emits GC-survivable audit-file entries for every audited
// field that actually changed between old and the applied updates map. old is
// the issue state BEFORE the update; updates is the map handed to
// store.UpdateIssue; reason is an optional human note (e.g. a close/defer
// reason). LogFieldChange itself no-ops when old==new, so passing unchanged
// fields is harmless. A nil old (issue not pre-loaded) falls back to empty
// old-values, still recording the new value.
func auditIssueUpdate(id string, old *types.Issue, updates map[string]interface{}, actor, reason string) {
	oldStatus, oldAssignee, oldPriority := "", "", ""
	if old != nil {
		oldStatus = string(old.Status)
		oldAssignee = old.Assignee
		oldPriority = fmt.Sprintf("%d", old.Priority)
	}

	if v, ok := updates["status"]; ok {
		audit.LogFieldChange(id, "status", oldStatus, coerceStatusString(v), actor, reason)
	}
	if v, ok := updates["assignee"]; ok {
		if s, ok := v.(string); ok {
			audit.LogFieldChange(id, "assignee", oldAssignee, s, actor, reason)
		}
	}
	if v, ok := updates["priority"]; ok {
		audit.LogFieldChange(id, "priority", oldPriority, coercePriorityString(v), actor, reason)
	}
}

// auditStatusChange is a convenience wrapper for handlers that perform a single
// status transition (close/reopen/defer) and already know the new status.
func auditStatusChange(id, oldStatus, newStatus, actor, reason string) {
	audit.LogFieldChange(id, "status", oldStatus, newStatus, actor, reason)
}

// coerceStatusString renders a status update value (string or types.Status).
func coerceStatusString(v interface{}) string {
	switch s := v.(type) {
	case string:
		return s
	case types.Status:
		return string(s)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// coercePriorityString renders a priority update value (int or string).
func coercePriorityString(v interface{}) string {
	switch p := v.(type) {
	case int:
		return fmt.Sprintf("%d", p)
	case string:
		return p
	default:
		return fmt.Sprintf("%v", v)
	}
}
