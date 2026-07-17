package issueops

import (
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

func TestDetermineEventType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		oldStatus types.Status
		updates   map[string]interface{}
		want      types.EventType
	}{
		{
			name:      "no status change is a plain update",
			oldStatus: types.StatusOpen,
			updates:   map[string]interface{}{"title": "new"},
			want:      types.EventUpdated,
		},
		{
			name:      "closing yields closed",
			oldStatus: types.StatusOpen,
			updates:   map[string]interface{}{"status": string(types.StatusClosed)},
			want:      types.EventClosed,
		},
		{
			name:      "closing via types.Status value yields closed",
			oldStatus: types.StatusInProgress,
			updates:   map[string]interface{}{"status": types.StatusClosed},
			want:      types.EventClosed,
		},
		{
			name:      "reopening a closed issue yields reopened",
			oldStatus: types.StatusClosed,
			updates:   map[string]interface{}{"status": string(types.StatusOpen)},
			want:      types.EventReopened,
		},
		{
			name:      "open to in_progress is a status change",
			oldStatus: types.StatusOpen,
			updates:   map[string]interface{}{"status": string(types.StatusInProgress)},
			want:      types.EventStatusChanged,
		},
		{
			name:      "non-string/non-Status status falls back to update",
			oldStatus: types.StatusOpen,
			updates:   map[string]interface{}{"status": 42},
			want:      types.EventUpdated,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			old := &types.Issue{Status: tt.oldStatus}
			if got := DetermineEventType(old, tt.updates); got != tt.want {
				t.Fatalf("DetermineEventType(%s, %v) = %q, want %q",
					tt.oldStatus, tt.updates, got, tt.want)
			}
		})
	}
}

func TestIsAllowedUpdateField(t *testing.T) {
	t.Parallel()

	allowed := []string{
		"status", "priority", "title", "assignee", "description", "design",
		"acceptance_criteria", "notes", "issue_type", "estimated_minutes",
		"external_ref", "spec_id", "started_at", "closed_at", "close_reason",
		"closed_by_session", "source_repo", "sender", "wisp", "wisp_type",
		"no_history", "pinned", "mol_type", "event_category", "event_actor",
		"event_target", "event_payload", "due_at", "defer_until", "await_id",
		"waiters", "metadata",
	}
	for _, k := range allowed {
		if !IsAllowedUpdateField(k) {
			t.Errorf("IsAllowedUpdateField(%q) = false, want true", k)
		}
	}

	denied := []string{"id", "created_at", "", "STATUS", "unknown_field", "drop table"}
	for _, k := range denied {
		if IsAllowedUpdateField(k) {
			t.Errorf("IsAllowedUpdateField(%q) = true, want false", k)
		}
	}
}

func TestManageClosedAt(t *testing.T) {
	t.Parallel()

	t.Run("no status update leaves clauses untouched", func(t *testing.T) {
		t.Parallel()
		old := &types.Issue{Status: types.StatusOpen}
		clauses, args := ManageClosedAt(old, map[string]interface{}{"title": "x"}, nil, nil)
		if len(clauses) != 0 || len(args) != 0 {
			t.Fatalf("expected no clauses, got clauses=%v args=%v", clauses, args)
		}
	})

	t.Run("explicit closed_at is not overridden", func(t *testing.T) {
		t.Parallel()
		old := &types.Issue{Status: types.StatusOpen}
		updates := map[string]interface{}{"status": string(types.StatusClosed), "closed_at": time.Now()}
		clauses, _ := ManageClosedAt(old, updates, nil, nil)
		if len(clauses) != 0 {
			t.Fatalf("expected no auto clause when closed_at explicit, got %v", clauses)
		}
	})

	t.Run("closing sets closed_at", func(t *testing.T) {
		t.Parallel()
		old := &types.Issue{Status: types.StatusOpen}
		updates := map[string]interface{}{"status": string(types.StatusClosed)}
		clauses, args := ManageClosedAt(old, updates, nil, nil)
		if len(clauses) != 1 || clauses[0] != "closed_at = ?" {
			t.Fatalf("expected [closed_at = ?], got %v", clauses)
		}
		if len(args) != 1 {
			t.Fatalf("expected one arg (now), got %v", args)
		}
		if _, ok := args[0].(time.Time); !ok {
			t.Fatalf("expected time.Time arg, got %T", args[0])
		}
	})

	t.Run("reopening a closed issue clears closed_at and reason", func(t *testing.T) {
		t.Parallel()
		old := &types.Issue{Status: types.StatusClosed}
		updates := map[string]interface{}{"status": string(types.StatusOpen)}
		clauses, args := ManageClosedAt(old, updates, nil, nil)
		if len(clauses) != 2 || clauses[0] != "closed_at = ?" || clauses[1] != "close_reason = ?" {
			t.Fatalf("expected [closed_at = ?, close_reason = ?], got %v", clauses)
		}
		if len(args) != 2 || args[0] != nil || args[1] != "" {
			t.Fatalf("expected [nil, \"\"], got %v", args)
		}
	})

	t.Run("non-string/non-Status status returns unchanged", func(t *testing.T) {
		t.Parallel()
		old := &types.Issue{Status: types.StatusOpen}
		updates := map[string]interface{}{"status": 7}
		clauses, args := ManageClosedAt(old, updates, nil, nil)
		if len(clauses) != 0 || len(args) != 0 {
			t.Fatalf("expected no clauses for bad status type, got clauses=%v args=%v", clauses, args)
		}
	})
}

func TestManageStartedAt(t *testing.T) {
	t.Parallel()

	t.Run("transition to in_progress sets started_at", func(t *testing.T) {
		t.Parallel()
		old := &types.Issue{Status: types.StatusOpen}
		updates := map[string]interface{}{"status": string(types.StatusInProgress)}
		clauses, args := ManageStartedAt(old, updates, nil, nil)
		if len(clauses) != 1 || clauses[0] != "started_at = ?" {
			t.Fatalf("expected [started_at = ?], got %v", clauses)
		}
		if _, ok := args[0].(time.Time); !ok {
			t.Fatalf("expected time.Time arg, got %T", args[0])
		}
	})

	t.Run("existing started_at is preserved", func(t *testing.T) {
		t.Parallel()
		prev := time.Now().Add(-time.Hour)
		old := &types.Issue{Status: types.StatusOpen, StartedAt: &prev}
		updates := map[string]interface{}{"status": string(types.StatusInProgress)}
		clauses, args := ManageStartedAt(old, updates, nil, nil)
		if len(clauses) != 0 || len(args) != 0 {
			t.Fatalf("expected started_at preserved (no clause), got clauses=%v", clauses)
		}
	})

	t.Run("explicit started_at is not overridden", func(t *testing.T) {
		t.Parallel()
		old := &types.Issue{Status: types.StatusOpen}
		updates := map[string]interface{}{"status": string(types.StatusInProgress), "started_at": time.Now()}
		clauses, _ := ManageStartedAt(old, updates, nil, nil)
		if len(clauses) != 0 {
			t.Fatalf("expected no auto clause when started_at explicit, got %v", clauses)
		}
	})

	t.Run("no status change leaves clauses untouched", func(t *testing.T) {
		t.Parallel()
		old := &types.Issue{Status: types.StatusOpen}
		clauses, args := ManageStartedAt(old, map[string]interface{}{"title": "x"}, nil, nil)
		if len(clauses) != 0 || len(args) != 0 {
			t.Fatalf("expected no clauses, got clauses=%v args=%v", clauses, args)
		}
	})

	t.Run("transition to a non-in_progress status does nothing", func(t *testing.T) {
		t.Parallel()
		old := &types.Issue{Status: types.StatusOpen}
		updates := map[string]interface{}{"status": string(types.StatusClosed)}
		clauses, _ := ManageStartedAt(old, updates, nil, nil)
		if len(clauses) != 0 {
			t.Fatalf("expected no clause for close transition, got %v", clauses)
		}
	})
}
