//go:build cgo

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

func TestProxiedServerUpdate(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("no_ids_errors", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "un1")
		out := bdProxiedUpdateFail(t, bd, p.dir)
		if !strings.Contains(out, "no issue ID provided") {
			t.Errorf("expected no-id error, got: %s", out)
		}
	})

	// beads-j43d: a wholly-failed --json batch (no issue updated) must emit a
	// parseable JSON error object on stdout, not a bare os.Exit(1) with empty
	// stdout — matching the direct path (update.go, beads-fg6/tx70).
	t.Run("all_failed_json_emits_stdout_error", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "unj")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "update", "unj-nope", "--priority", "1", "--json")
		if err == nil {
			t.Fatalf("expected non-zero exit on all-failed --json update\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		assertJSONErrorObject(t, stdout, err)
	})

	t.Run("no_flags_is_noop_message", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "un2")
		issue := bdProxiedCreate(t, bd, p.dir, "Seed")
		out, err := bdProxiedRun(t, bd, p.dir, "update", issue.ID)
		if err != nil {
			t.Fatalf("update with no flags should succeed, got: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), "No updates specified") {
			t.Errorf("expected 'No updates specified', got: %s", out)
		}
	})

	t.Run("field_updates_round_trip", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uf")
		issue := bdProxiedCreate(t, bd, p.dir, "Original")
		updated := bdProxiedUpdateOne(t, bd, p.dir, issue.ID,
			"--title", "Renamed",
			"-p", "0",
			"--assignee", "alice",
			"--description", "new body")
		if updated.Title != "Renamed" {
			t.Errorf("title: got %q, want %q", updated.Title, "Renamed")
		}
		if updated.Priority != 0 {
			t.Errorf("priority: got %d, want 0", updated.Priority)
		}
		if updated.Assignee != "alice" {
			t.Errorf("assignee: got %q, want %q", updated.Assignee, "alice")
		}
		if updated.Description != "new body" {
			t.Errorf("description: got %q, want %q", updated.Description, "new body")
		}
	})

	t.Run("audit_logs_field_changes", func(t *testing.T) {
		// beads-jffu: the proxied UPDATE path must emit the GC-survivable
		// audit-file field-change trail for status/assignee/priority, mirroring
		// the CLI update path and the proxied close/reopen handlers. Before the
		// fix, proxied update alone dropped this trail.
		p := bdProxiedInit(t, bd, "uau")
		issue := bdProxiedCreate(t, bd, p.dir, "Audit me")
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID,
			"--status", "in_progress",
			"--assignee", "alice",
			"-p", "0")

		auditPath := filepath.Join(p.beadsDir, "interactions.jsonl")
		data, err := os.ReadFile(auditPath)
		if err != nil {
			t.Fatalf("proxied update wrote no audit trail (beads-jffu): %v", err)
		}
		seen := map[string]bool{}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			var e struct {
				Kind  string         `json:"kind"`
				Extra map[string]any `json:"extra"`
			}
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				t.Fatalf("bad audit line %q: %v", line, err)
			}
			if e.Kind == "field_change" {
				if f, ok := e.Extra["field"].(string); ok {
					seen[f] = true
				}
			}
		}
		for _, want := range []string{"status", "assignee", "priority"} {
			if !seen[want] {
				t.Errorf("proxied update dropped audit field_change for %q (beads-jffu); saw %v", want, seen)
			}
		}
	})

	t.Run("claim_sets_assignee_and_in_progress", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uc")
		issue := bdProxiedCreate(t, bd, p.dir, "To claim")
		updated := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--claim")
		if updated.Status != types.StatusInProgress {
			t.Errorf("status: got %q, want in_progress", updated.Status)
		}
		if updated.Assignee == "" {
			t.Errorf("assignee should be set after --claim, got empty")
		}
	})

	t.Run("claim_then_other_user_conflicts", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ucc")
		issue := bdProxiedCreate(t, bd, p.dir, "Contested")
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--claim", "--assignee", "alice")

		out := bdProxiedUpdateFail(t, bd, p.dir, issue.ID, "--claim", "--assignee", "bob")
		if !strings.Contains(out, "already claimed") {
			t.Errorf("expected 'already claimed' error, got: %s", out)
		}
	})

	t.Run("add_remove_labels", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ul")
		issue := bdProxiedCreate(t, bd, p.dir, "Labeled")
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--add-label", "perf,tech-debt")
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--remove-label", "perf")

		db := openProxiedDB(t, p)
		labels := getProxiedLabels(t, db, issue.ID)
		if len(labels) != 1 || labels[0] != "tech-debt" {
			t.Errorf("labels after add+remove: got %v, want [tech-debt]", labels)
		}
	})

	t.Run("set_labels_diffs", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "usl")
		issue := bdProxiedCreate(t, bd, p.dir, "Set labels")
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--add-label", "a,b,c")
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--set-labels", "b,d")

		db := openProxiedDB(t, p)
		labels := getProxiedLabels(t, db, issue.ID)
		got := strings.Join(labels, ",")
		if got != "b,d" && got != "d,b" {
			t.Errorf("labels after --set-labels: got %v, want [b d] (any order)", labels)
		}
	})

	t.Run("reparent_replaces_existing_parent", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "urp")
		oldParent := bdProxiedCreate(t, bd, p.dir, "Old parent", "-t", "epic")
		newParent := bdProxiedCreate(t, bd, p.dir, "New parent", "-t", "epic")
		child := bdProxiedCreate(t, bd, p.dir, "Child", "--parent", oldParent.ID)

		bdProxiedUpdateOne(t, bd, p.dir, child.ID, "--parent", newParent.ID)

		db := openProxiedDB(t, p)
		assertProxiedDepExistsWithType(t, db, child.ID, newParent.ID, string(types.DepParentChild))
		var oldRowCount int
		if err := db.QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM dependencies WHERE issue_id = ? AND depends_on_issue_id = ?",
			child.ID, oldParent.ID).Scan(&oldRowCount); err != nil {
			t.Fatalf("count old parent dep: %v", err)
		}
		if oldRowCount != 0 {
			t.Errorf("old parent dep should be gone, got %d rows", oldRowCount)
		}
	})

	t.Run("reparent_empty_unparents", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uup")
		parent := bdProxiedCreate(t, bd, p.dir, "Parent", "-t", "epic")
		child := bdProxiedCreate(t, bd, p.dir, "Child", "--parent", parent.ID)

		bdProxiedUpdateOne(t, bd, p.dir, child.ID, "--parent", "")

		db := openProxiedDB(t, p)
		var count int
		if err := db.QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM dependencies WHERE issue_id = ? AND type = 'parent-child'",
			child.ID).Scan(&count); err != nil {
			t.Fatalf("count parent dep: %v", err)
		}
		if count != 0 {
			t.Errorf("expected no parent-child dep after unparent, got %d", count)
		}
	})

	// beads-a8a1b: refuse reparenting an OPEN child under a CLOSED epic over the
	// PROXIED server (parent-assignment axis of the closed-epic-with-open-child
	// invariant), mirroring the direct path. The proxied guard lives in
	// checkProxiedUpdateCloseGuards, not update.go's RunE.
	t.Run("reparent_open_child_under_closed_epic_refused", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "urce")
		epic := bdProxiedCreate(t, bd, p.dir, "Closed epic reparent", "-t", "epic")
		child := bdProxiedCreate(t, bd, p.dir, "Loose task")
		bdProxiedUpdateOne(t, bd, p.dir, epic.ID, "-s", "closed") // no children yet, close allowed
		out := bdProxiedUpdateFail(t, bd, p.dir, child.ID, "--parent", epic.ID)
		if !strings.Contains(out, "closed epic") {
			t.Errorf("expected a 'closed epic' guard error on proxied reparent, got: %s", out)
		}
		db := openProxiedDB(t, p)
		// The child must not have been reparented under the closed epic.
		var count int
		if err := db.QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM dependencies WHERE issue_id = ? AND depends_on_issue_id = ? AND type = 'parent-child'",
			child.ID, epic.ID).Scan(&count); err != nil {
			t.Fatalf("count parent dep: %v", err)
		}
		if count != 0 {
			t.Errorf("open child must not be reparented under a closed epic (a8a1b), got %d parent edges", count)
		}
	})

	t.Run("reparent_open_child_under_closed_epic_force_overrides", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "urcf")
		epic := bdProxiedCreate(t, bd, p.dir, "Closed epic reparent force", "-t", "epic")
		child := bdProxiedCreate(t, bd, p.dir, "Loose task force")
		bdProxiedUpdateOne(t, bd, p.dir, epic.ID, "-s", "closed")
		bdProxiedUpdateOne(t, bd, p.dir, child.ID, "--parent", epic.ID, "--force")
		db := openProxiedDB(t, p)
		assertProxiedDepExistsWithType(t, db, child.ID, epic.ID, string(types.DepParentChild))
	})

	t.Run("close_unblocks_dependents", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ucb")
		blocker := bdProxiedCreate(t, bd, p.dir, "Blocker")
		blocked := bdProxiedCreate(t, bd, p.dir, "Dependent", "--deps", "depends-on:"+blocker.ID)

		db := openProxiedDB(t, p)
		if !readIsBlocked(t, db, blocked.ID) {
			t.Fatalf("dependent should be blocked before blocker closes")
		}

		bdProxiedUpdateOne(t, bd, p.dir, blocker.ID, "-s", "closed")

		if readIsBlocked(t, db, blocked.ID) {
			t.Errorf("dependent should be unblocked after blocker closes")
		}
	})

	t.Run("invalid_status_rejected", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uis")
		issue := bdProxiedCreate(t, bd, p.dir, "Status test")
		out := bdProxiedUpdateFail(t, bd, p.dir, issue.ID, "-s", "not-a-real-status")
		if !strings.Contains(out, "invalid status") {
			t.Errorf("expected 'invalid status' error, got: %s", out)
		}
	})

	t.Run("multiple_ids_update_all", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "umu")
		a := bdProxiedCreate(t, bd, p.dir, "A")
		b := bdProxiedCreate(t, bd, p.dir, "B")
		issues := bdProxiedUpdate(t, bd, p.dir, a.ID, b.ID, "--assignee", "team")
		if len(issues) != 2 {
			t.Fatalf("expected 2 updated issues, got %d", len(issues))
		}
		for _, iss := range issues {
			if iss.Assignee != "team" {
				t.Errorf("%s assignee: got %q, want team", iss.ID, iss.Assignee)
			}
		}
	})

	t.Run("defer_clear_restores_open", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "udf")
		issue := bdProxiedCreate(t, bd, p.dir, "Deferred")

		deferred := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--defer", "+1d")
		if deferred.Status != types.StatusDeferred {
			t.Fatalf("expected deferred status, got %q", deferred.Status)
		}

		restored := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--defer", "")
		if restored.Status != types.StatusOpen {
			t.Errorf("--defer='' should restore open, got %q", restored.Status)
		}
	})

	t.Run("update_type", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ut")
		issue := bdProxiedCreate(t, bd, p.dir, "Type test")
		updated := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--type", "bug")
		if updated.IssueType != types.TypeBug {
			t.Errorf("type: got %q, want %q", updated.IssueType, types.TypeBug)
		}
	})

	t.Run("update_type_invalid_rejected", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uti")
		issue := bdProxiedCreate(t, bd, p.dir, "Bad type test")
		out := bdProxiedUpdateFail(t, bd, p.dir, issue.ID, "--type", "not-a-real-type")
		if !strings.Contains(strings.ToLower(out), "invalid") &&
			!strings.Contains(strings.ToLower(out), "unknown") {
			t.Errorf("expected invalid/unknown type error, got: %s", out)
		}
	})

	t.Run("update_type_custom", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "utc")
		issue := bdProxiedCreate(t, bd, p.dir, "Custom type test")

		db := openProxiedDB(t, p)
		if _, err := db.ExecContext(context.Background(),
			"INSERT INTO config (`key`, value) VALUES ('types.custom', ?) "+
				"ON DUPLICATE KEY UPDATE value = VALUES(value)",
			"molecule"); err != nil {
			t.Fatalf("seed types.custom: %v", err)
		}

		updated := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--type", "molecule")
		if string(updated.IssueType) != "molecule" {
			t.Errorf("type: got %q, want %q", updated.IssueType, "molecule")
		}
	})

	t.Run("description_from_file", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ubf")
		issue := bdProxiedCreate(t, bd, p.dir, "Body file test")

		bodyPath := filepath.Join(p.dir, "body.txt")
		if err := os.WriteFile(bodyPath, []byte("from file"), 0o600); err != nil {
			t.Fatalf("write body file: %v", err)
		}

		updated := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--body-file", bodyPath)
		if updated.Description != "from file" {
			t.Errorf("description: got %q, want %q", updated.Description, "from file")
		}
	})

	t.Run("description_body_alias", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uba")
		issue := bdProxiedCreate(t, bd, p.dir, "Body alias test")
		updated := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--body", "via body flag")
		if updated.Description != "via body flag" {
			t.Errorf("description: got %q, want %q", updated.Description, "via body flag")
		}
	})

	t.Run("update_creates_dolt_commit", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "udc")
		issue := bdProxiedCreate(t, bd, p.dir, "Dolt commit test")

		db := openProxiedDB(t, p)
		var before string
		if err := db.QueryRowContext(context.Background(),
			"SELECT HASHOF('HEAD')").Scan(&before); err != nil {
			t.Fatalf("read HEAD before: %v", err)
		}

		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--title", "Renamed for commit")

		var after string
		if err := db.QueryRowContext(context.Background(),
			"SELECT HASHOF('HEAD')").Scan(&after); err != nil {
			t.Fatalf("read HEAD after: %v", err)
		}
		if after == before {
			t.Errorf("HEAD did not advance: before=%s after=%s (uw.Commit should produce a Dolt commit)",
				before, after)
		}
	})

	t.Run("reparent_from_orphan", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "urpo")
		parent := bdProxiedCreate(t, bd, p.dir, "New parent", "-t", "epic")
		orphan := bdProxiedCreate(t, bd, p.dir, "Orphan child")

		bdProxiedUpdateOne(t, bd, p.dir, orphan.ID, "--parent", parent.ID)

		db := openProxiedDB(t, p)
		assertProxiedDepExistsWithType(t, db, orphan.ID, parent.ID, string(types.DepParentChild))
	})

	t.Run("close_with_session_flag", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ucws")
		issue := bdProxiedCreate(t, bd, p.dir, "Close-with-session flag test")

		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "-s", "closed", "--session", "sess-flag")

		db := openProxiedDB(t, p)
		var got sql.NullString
		if err := db.QueryRowContext(context.Background(),
			"SELECT closed_by_session FROM issues WHERE id = ?", issue.ID).Scan(&got); err != nil {
			t.Fatalf("read closed_by_session: %v", err)
		}
		if !got.Valid || got.String != "sess-flag" {
			t.Errorf("closed_by_session: got %q (valid=%v), want %q", got.String, got.Valid, "sess-flag")
		}
	})

	t.Run("close_with_session_env", func(t *testing.T) {
		t.Setenv("CLAUDE_SESSION_ID", "sess-env")
		p := bdProxiedInit(t, bd, "ucwe")
		issue := bdProxiedCreate(t, bd, p.dir, "Close-with-session env test")

		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "-s", "closed")

		db := openProxiedDB(t, p)
		var got sql.NullString
		if err := db.QueryRowContext(context.Background(),
			"SELECT closed_by_session FROM issues WHERE id = ?", issue.ID).Scan(&got); err != nil {
			t.Fatalf("read closed_by_session: %v", err)
		}
		if !got.Valid || got.String != "sess-env" {
			t.Errorf("closed_by_session: got %q (valid=%v), want %q (from CLAUDE_SESSION_ID)",
				got.String, got.Valid, "sess-env")
		}
	})

	t.Run("ephemeral_persistent_conflict", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uepc")
		issue := bdProxiedCreate(t, bd, p.dir, "Ephemeral/persistent conflict test")
		out := bdProxiedUpdateFail(t, bd, p.dir, issue.ID, "--ephemeral", "--persistent")
		if !strings.Contains(out, "cannot specify both --ephemeral and --persistent") {
			t.Errorf("expected conflict error, got: %s", out)
		}
	})

	t.Run("update_history", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uh")
		issue := bdProxiedCreate(t, bd, p.dir, "History test")

		db := openProxiedDB(t, p)
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--no-history")
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--history")

		var noHistory int
		if err := db.QueryRowContext(context.Background(),
			"SELECT no_history FROM issues WHERE id = ?", issue.ID).Scan(&noHistory); err != nil {
			t.Fatalf("read no_history: %v", err)
		}
		if noHistory != 0 {
			t.Errorf("no_history: got %d, want 0 after --history", noHistory)
		}
	})

	t.Run("update_no_history", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "unh")
		issue := bdProxiedCreate(t, bd, p.dir, "No history test")

		db := openProxiedDB(t, p)
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--no-history")

		var noHistory int
		if err := db.QueryRowContext(context.Background(),
			"SELECT no_history FROM issues WHERE id = ?", issue.ID).Scan(&noHistory); err != nil {
			t.Fatalf("read no_history: %v", err)
		}
		if noHistory != 1 {
			t.Errorf("no_history: got %d, want 1 after --no-history", noHistory)
		}
	})

	t.Run("update_persistent", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ups")
		issue := bdProxiedCreate(t, bd, p.dir, "Persistent test")

		db := openProxiedDB(t, p)
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--ephemeral")
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--persistent")

		var ephemeral int
		if err := db.QueryRowContext(context.Background(),
			"SELECT ephemeral FROM issues WHERE id = ?", issue.ID).Scan(&ephemeral); err != nil {
			t.Fatalf("read ephemeral: %v", err)
		}
		if ephemeral != 0 {
			t.Errorf("ephemeral: got %d, want 0 after --persistent", ephemeral)
		}
	})

	t.Run("update_ephemeral", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uep")
		issue := bdProxiedCreate(t, bd, p.dir, "Ephemeral test")

		db := openProxiedDB(t, p)
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--ephemeral")

		var ephemeral int
		if err := db.QueryRowContext(context.Background(),
			"SELECT ephemeral FROM issues WHERE id = ?", issue.ID).Scan(&ephemeral); err != nil {
			t.Fatalf("read ephemeral: %v", err)
		}
		if ephemeral != 1 {
			t.Errorf("ephemeral: got %d, want 1 after --ephemeral", ephemeral)
		}
	})

	t.Run("reopen_reblocks_dependents", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "urb")
		blocker := bdProxiedCreate(t, bd, p.dir, "Reopen blocker")
		blocked := bdProxiedCreate(t, bd, p.dir, "Reopen dependent", "--deps", "depends-on:"+blocker.ID)

		db := openProxiedDB(t, p)
		if !readIsBlocked(t, db, blocked.ID) {
			t.Fatalf("dependent should be blocked before close")
		}

		bdProxiedUpdateOne(t, bd, p.dir, blocker.ID, "-s", "closed")
		if readIsBlocked(t, db, blocked.ID) {
			t.Fatalf("dependent should be unblocked after blocker closes")
		}

		bdProxiedUpdateOne(t, bd, p.dir, blocker.ID, "-s", "open")
		if !readIsBlocked(t, db, blocked.ID) {
			t.Errorf("dependent should be re-blocked after blocker reopens")
		}
	})

	t.Run("update_nonexistent_id", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "unx")
		out := bdProxiedUpdateFail(t, bd, p.dir, "unx-doesnotexist", "--title", "x")
		if !strings.Contains(strings.ToLower(out), "not found") &&
			!strings.Contains(strings.ToLower(out), "no rows") &&
			!strings.Contains(strings.ToLower(out), "error") {
			t.Errorf("expected a not-found / error message, got: %s", out)
		}
	})

	t.Run("update_invalid_priority", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uip")
		issue := bdProxiedCreate(t, bd, p.dir, "Invalid priority test")
		out := bdProxiedUpdateFail(t, bd, p.dir, issue.ID, "-p", "99")
		if !strings.Contains(out, "invalid priority") {
			t.Errorf("expected 'invalid priority' error, got: %s", out)
		}
	})

	t.Run("update_metadata_invalid_json", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "umij")
		issue := bdProxiedCreate(t, bd, p.dir, "Metadata invalid JSON test")
		out := bdProxiedUpdateFail(t, bd, p.dir, issue.ID, "--metadata", "not json at all")
		if !strings.Contains(out, "invalid JSON") {
			t.Errorf("expected 'invalid JSON' error, got: %s", out)
		}
	})

	t.Run("update_metadata_at_file", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "umaf")
		issue := bdProxiedCreate(t, bd, p.dir, "Metadata @file test")

		metaPath := filepath.Join(p.dir, "meta.json")
		if err := os.WriteFile(metaPath, []byte(`{"src":"file","n":7}`), 0o600); err != nil {
			t.Fatalf("write metadata file: %v", err)
		}

		updated := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--metadata", "@"+metaPath)
		var got map[string]any
		if err := json.Unmarshal(updated.Metadata, &got); err != nil {
			t.Fatalf("parse metadata %q: %v", updated.Metadata, err)
		}
		if got["src"] != "file" {
			t.Errorf("metadata[src]: got %v, want %q", got["src"], "file")
		}
		if got["n"] != float64(7) {
			t.Errorf("metadata[n]: got %v, want 7", got["n"])
		}
	})

	t.Run("metadata_and_set_conflict", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "umsc")
		issue := bdProxiedCreate(t, bd, p.dir, "Metadata conflict test")
		out := bdProxiedUpdateFail(t, bd, p.dir, issue.ID,
			"--metadata", `{"a":1}`,
			"--set-metadata", "x=y")
		if !strings.Contains(out, "cannot combine --metadata with --set-metadata") {
			t.Errorf("expected conflict error, got: %s", out)
		}
	})

	t.Run("update_unset_metadata", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uum")
		issue := bdProxiedCreate(t, bd, p.dir, "Unset metadata test")
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--metadata", `{"keep":"yes","drop":"yes"}`)

		updated := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--unset-metadata", "drop")

		var got map[string]any
		if err := json.Unmarshal(updated.Metadata, &got); err != nil {
			t.Fatalf("parse metadata %q: %v", updated.Metadata, err)
		}
		if _, present := got["drop"]; present {
			t.Errorf("metadata[drop]: still present after --unset-metadata, got %v", got)
		}
		if got["keep"] != "yes" {
			t.Errorf("metadata[keep]: got %v, want %q", got["keep"], "yes")
		}
	})

	t.Run("update_set_metadata", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "usm")
		issue := bdProxiedCreate(t, bd, p.dir, "Set metadata test")
		updated := bdProxiedUpdateOne(t, bd, p.dir, issue.ID,
			"--set-metadata", "tier=gold",
			"--set-metadata", "score=99")

		var got map[string]any
		if err := json.Unmarshal(updated.Metadata, &got); err != nil {
			t.Fatalf("parse metadata %q: %v", updated.Metadata, err)
		}
		if got["tier"] != "gold" {
			t.Errorf("metadata[tier]: got %v, want %q", got["tier"], "gold")
		}
		if got["score"] != float64(99) {
			t.Errorf("metadata[score]: got %v, want 99 (number-typed via toJSONValue)", got["score"])
		}
	})

	t.Run("update_metadata_merge", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "umm")
		issue := bdProxiedCreate(t, bd, p.dir, "Metadata merge test")
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--metadata", `{"a":1,"b":2}`)
		merged := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--metadata", `{"b":3,"c":4}`)

		var got map[string]any
		if err := json.Unmarshal(merged.Metadata, &got); err != nil {
			t.Fatalf("parse metadata %q: %v", merged.Metadata, err)
		}
		want := map[string]float64{"a": 1, "b": 3, "c": 4}
		for k, v := range want {
			gv, ok := got[k].(float64)
			if !ok {
				t.Errorf("metadata[%s]: got %v (%T), want %v", k, got[k], got[k], v)
				continue
			}
			if gv != v {
				t.Errorf("metadata[%s]: got %v, want %v", k, gv, v)
			}
		}
		if len(got) != 3 {
			t.Errorf("metadata: got %d keys (%v), want 3 (a,b,c)", len(got), got)
		}
	})

	t.Run("update_metadata_json", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "umd")
		issue := bdProxiedCreate(t, bd, p.dir, "Metadata json test")
		updated := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--metadata", `{"k":"v","n":42}`)
		var got map[string]any
		if err := json.Unmarshal(updated.Metadata, &got); err != nil {
			t.Fatalf("parse metadata %q: %v", updated.Metadata, err)
		}
		if got["k"] != "v" {
			t.Errorf("metadata[k]: got %v, want %q", got["k"], "v")
		}
		if got["n"] != float64(42) {
			t.Errorf("metadata[n]: got %v, want 42", got["n"])
		}
	})

	t.Run("update_await_id", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uaw")
		issue := bdProxiedCreate(t, bd, p.dir, "Await id test")
		updated := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--await-id", "gate-1")
		if updated.AwaitID != "gate-1" {
			t.Errorf("await_id: got %q, want %q", updated.AwaitID, "gate-1")
		}
	})

	t.Run("update_defer_clear_preserves_non_deferred_status", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "udfp2")
		issue := bdProxiedCreate(t, bd, p.dir, "Defer clear preserve test")

		seeded := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--defer", "+1d", "-s", "blocked")
		if seeded.Status != types.StatusBlocked {
			t.Fatalf("seed: expected blocked, got %q", seeded.Status)
		}

		cleared := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--defer", "")
		if cleared.Status != types.StatusBlocked {
			t.Errorf("status: got %q, want %q (defer='' should not flip non-deferred status to open)",
				cleared.Status, types.StatusBlocked)
		}
		if cleared.DeferUntil != nil {
			t.Errorf("defer_until: expected nil after clear, got %s", cleared.DeferUntil)
		}
	})

	t.Run("update_defer_past_date_keeps_status_open", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "udfp")
		issue := bdProxiedCreate(t, bd, p.dir, "Defer past test")

		updated := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--defer", "2020-01-01")
		if updated.Status != types.StatusOpen {
			t.Errorf("status: got %q, want %q (past defer date must not flip to deferred)",
				updated.Status, types.StatusOpen)
		}
		if updated.DeferUntil == nil {
			t.Errorf("defer_until: got nil, want past timestamp set")
		}
	})

	t.Run("update_defer_respects_explicit_status", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "udfe")
		issue := bdProxiedCreate(t, bd, p.dir, "Defer explicit status test")

		updated := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--defer", "+1d", "-s", "blocked")
		if updated.Status != types.StatusBlocked {
			t.Errorf("status: got %q, want %q (explicit --status must win over defer auto-set)",
				updated.Status, types.StatusBlocked)
		}
		if updated.DeferUntil == nil {
			t.Errorf("defer_until: got nil, want non-nil (defer still applied)")
		}
	})

	t.Run("update_defer_set", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "udfs")
		issue := bdProxiedCreate(t, bd, p.dir, "Defer set test")

		now := time.Now()
		set := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--defer", "+1d")
		if set.Status != types.StatusDeferred {
			t.Errorf("status: got %q, want %q", set.Status, types.StatusDeferred)
		}
		if set.DeferUntil == nil {
			t.Fatalf("defer_until: got nil, want a future time")
		}
		if !set.DeferUntil.After(now) {
			t.Errorf("defer_until: got %s, expected after %s", set.DeferUntil, now)
		}
	})

	t.Run("update_due", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "udu")
		issue := bdProxiedCreate(t, bd, p.dir, "Due test")

		now := time.Now()
		set := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--due", "+6h")
		if set.DueAt == nil {
			t.Fatalf("due_at: got nil, want a future time")
		}
		if !set.DueAt.After(now) {
			t.Errorf("due_at: got %s, expected after %s", set.DueAt, now)
		}

		cleared := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--due", "")
		if cleared.DueAt != nil {
			t.Errorf("due_at: expected nil after clear, got %s", cleared.DueAt)
		}
	})

	t.Run("update_estimate", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ues")
		issue := bdProxiedCreate(t, bd, p.dir, "Estimate test")

		updated := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--estimate", "60")
		if updated.EstimatedMinutes == nil {
			t.Fatalf("estimate: got nil, want 60")
		}
		if *updated.EstimatedMinutes != 60 {
			t.Errorf("estimate: got %d, want 60", *updated.EstimatedMinutes)
		}

		out := bdProxiedUpdateFail(t, bd, p.dir, issue.ID, "--estimate", "-1")
		if !strings.Contains(out, "non-negative") {
			t.Errorf("expected 'non-negative' error, got: %s", out)
		}
	})

	t.Run("update_spec_id", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "usp")
		issue := bdProxiedCreate(t, bd, p.dir, "Spec id test")
		updated := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--spec-id", "spec-42")
		if updated.SpecID != "spec-42" {
			t.Errorf("spec_id: got %q, want %q", updated.SpecID, "spec-42")
		}
	})

	t.Run("update_external_ref_clear", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uerc")
		issue := bdProxiedCreate(t, bd, p.dir, "External ref clear test", "--external-ref", "gh-9")
		if issue.ExternalRef == nil || *issue.ExternalRef != "gh-9" {
			t.Fatalf("seed: external_ref not set as expected, got %v", issue.ExternalRef)
		}

		cleared := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--external-ref", "")
		if cleared.ExternalRef != nil && *cleared.ExternalRef != "" {
			t.Errorf("external_ref: expected nil or empty after clear, got %q", *cleared.ExternalRef)
		}
	})

	t.Run("update_external_ref", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uer")
		issue := bdProxiedCreate(t, bd, p.dir, "External ref test")
		updated := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--external-ref", "gh-9")
		if updated.ExternalRef == nil {
			t.Fatalf("external_ref: got nil, want pointer to %q", "gh-9")
		}
		if *updated.ExternalRef != "gh-9" {
			t.Errorf("external_ref: got %q, want %q", *updated.ExternalRef, "gh-9")
		}
	})

	t.Run("update_acceptance", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uac")
		issue := bdProxiedCreate(t, bd, p.dir, "Acceptance test")

		shortFlag := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--acceptance", "via short")
		if shortFlag.AcceptanceCriteria != "via short" {
			t.Errorf("--acceptance: got %q, want %q", shortFlag.AcceptanceCriteria, "via short")
		}

		longFlag := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--acceptance-criteria", "via long")
		if longFlag.AcceptanceCriteria != "via long" {
			t.Errorf("--acceptance-criteria: got %q, want %q", longFlag.AcceptanceCriteria, "via long")
		}
	})

	t.Run("notes_and_append_conflict", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "una")
		issue := bdProxiedCreate(t, bd, p.dir, "Notes conflict test")
		out := bdProxiedUpdateFail(t, bd, p.dir, issue.ID, "--notes", "a", "--append-notes", "b")
		if !strings.Contains(out, "cannot specify both --notes and --append-notes") {
			t.Errorf("expected conflict error, got: %s", out)
		}
	})

	t.Run("update_notes", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "un")
		issue := bdProxiedCreate(t, bd, p.dir, "Notes test", "--notes", "first")
		updated := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--notes", "replacement")
		if updated.Notes != "replacement" {
			t.Errorf("notes: got %q, want %q", updated.Notes, "replacement")
		}
	})

	t.Run("update_design", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ud")
		issue := bdProxiedCreate(t, bd, p.dir, "Design test")

		flagUpdated := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--design", "via flag")
		if flagUpdated.Design != "via flag" {
			t.Errorf("--design: got %q, want %q", flagUpdated.Design, "via flag")
		}

		designFile := filepath.Join(p.dir, "design.txt")
		if err := os.WriteFile(designFile, []byte("via file"), 0o600); err != nil {
			t.Fatalf("write design file: %v", err)
		}
		fileUpdated := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--design-file", designFile)
		if fileUpdated.Design != "via file" {
			t.Errorf("--design-file: got %q, want %q", fileUpdated.Design, "via file")
		}
	})

	t.Run("update_type_custom_table", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "utt")
		issue := bdProxiedCreate(t, bd, p.dir, "Custom type table test")

		db := openProxiedDB(t, p)
		if _, err := db.ExecContext(context.Background(),
			"INSERT INTO custom_types (name) VALUES (?)", "swarm"); err != nil {
			t.Fatalf("seed custom_types row: %v", err)
		}

		updated := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--type", "swarm")
		if string(updated.IssueType) != "swarm" {
			t.Errorf("type: got %q, want %q", updated.IssueType, "swarm")
		}
	})

	t.Run("append_notes_concatenates", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uan")
		issue := bdProxiedCreate(t, bd, p.dir, "Notes", "--notes", "first")
		updated := bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--append-notes", "second")
		want := "first\nsecond"
		if updated.Notes != want {
			t.Errorf("notes: got %q, want %q", updated.Notes, want)
		}
	})

	// beads-iu9f Phase B / beads-25k6: the proxied/domain update path must enforce
	// the same post-update invariants as the shared seam (issueops.updateIssueInTx),
	// not leak a raw Dolt column error. Before the reroute, a >500-char title hit
	// the raw "UPDATE ... SET title=?" and returned Error 1105 "string ... too large
	// for column 'title'"; it must instead fail with the domain title-length
	// validation ("title must be 500 characters or less").
	t.Run("title_too_long_rejected_cleanly", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "utl")
		issue := bdProxiedCreate(t, bd, p.dir, "Original")
		longTitle := strings.Repeat("x", 501)
		out := bdProxiedUpdateFail(t, bd, p.dir, issue.ID, "--title", longTitle)
		if strings.Contains(out, "too large for column") || strings.Contains(out, "Error 1105") {
			t.Errorf("title-too-long leaked a raw Dolt column error instead of clean domain validation:\n%s", out)
		}
		if !strings.Contains(out, "500") {
			t.Errorf("expected a title-length (500) validation error, got:\n%s", out)
		}
		// The failed update must not have partially applied: title unchanged.
		reloaded := bdProxiedShow(t, bd, p.dir, issue.ID)
		if reloaded.Title != "Original" {
			t.Errorf("failed title update leaked through: title=%q, want Original", reloaded.Title)
		}
	})

	// beads-u8zw: `bd update --status closed` (and the demote / child-reopen
	// transitions) must enforce, on the proxied path, the same close-integrity
	// guards the direct path enforces (zgku/2hkd/b0tw). Before the fix the
	// proxied handler applied the field update with no guard, so these all
	// silently succeeded via the LIVE proxied path where the direct path refuses.
	t.Run("update_close_epic_open_children_refuses", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uceor")
		epic := bdProxiedCreate(t, bd, p.dir, "Epic", "-t", "epic")
		_ = bdProxiedCreate(t, bd, p.dir, "Child", "--parent", epic.ID)
		out := bdProxiedUpdateFail(t, bd, p.dir, epic.ID, "-s", "closed")
		if !strings.Contains(out, "open child") {
			t.Errorf("expected 'open child' guard error, got: %s", out)
		}
		db := openProxiedDB(t, p)
		if got := readStatus(t, db, epic.ID); got != types.StatusOpen {
			t.Errorf("epic must stay open after a guarded update-close, got %q", got)
		}
	})

	t.Run("update_close_epic_open_children_force_overrides", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uceof")
		epic := bdProxiedCreate(t, bd, p.dir, "Epic force", "-t", "epic")
		_ = bdProxiedCreate(t, bd, p.dir, "Child force", "--parent", epic.ID)
		bdProxiedUpdateOne(t, bd, p.dir, epic.ID, "-s", "closed", "--force")
		db := openProxiedDB(t, p)
		if got := readStatus(t, db, epic.ID); got != types.StatusClosed {
			t.Errorf("--force should override the epic-open-children guard, got %q", got)
		}
	})

	t.Run("update_close_blocked_refuses", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ucbr")
		blocker := bdProxiedCreate(t, bd, p.dir, "Blocker")
		blocked := bdProxiedCreate(t, bd, p.dir, "Dependent", "--deps", "depends-on:"+blocker.ID)
		out := bdProxiedUpdateFail(t, bd, p.dir, blocked.ID, "-s", "closed")
		if !strings.Contains(out, "blocked by open issues") {
			t.Errorf("expected 'blocked by open issues' guard error, got: %s", out)
		}
		db := openProxiedDB(t, p)
		if got := readStatus(t, db, blocked.ID); got == types.StatusClosed {
			t.Errorf("blocked issue must not close via a guarded update, got %q", got)
		}
	})

	t.Run("update_close_blocked_force_overrides", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ucbf")
		blocker := bdProxiedCreate(t, bd, p.dir, "Blocker force")
		blocked := bdProxiedCreate(t, bd, p.dir, "Dependent force", "--deps", "depends-on:"+blocker.ID)
		bdProxiedUpdateOne(t, bd, p.dir, blocked.ID, "-s", "closed", "--force")
		db := openProxiedDB(t, p)
		if got := readStatus(t, db, blocked.ID); got != types.StatusClosed {
			t.Errorf("--force should override the blocked guard, got %q", got)
		}
	})

	t.Run("update_close_unblocked_still_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ucus")
		issue := bdProxiedCreate(t, bd, p.dir, "Plain")
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "-s", "closed")
		db := openProxiedDB(t, p)
		if got := readStatus(t, db, issue.ID); got != types.StatusClosed {
			t.Errorf("an unblocked non-epic must still close via update, got %q", got)
		}
	})

	// beads-6qo8t: `bd update --status closed` over the PROXIED server must
	// default close_reason to "Closed", parity with `bd close` and the direct
	// path. The proxied path flows through domain/db/issue.go (not update.go's
	// RunE), so the fix lives at the storage seam to cover BOTH paths. Before
	// the fix the proxied path set closed_at but left close_reason NULL.
	t.Run("update_close_defaults_close_reason", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ucdcr")
		issue := bdProxiedCreate(t, bd, p.dir, "Proxied reason parity")
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "-s", "closed")
		db := openProxiedDB(t, p)
		if got := readCloseReason(t, db, issue.ID); got != "Closed" {
			t.Errorf("update --status closed (proxied) must default close_reason='Closed' (beads-6qo8t parity with bd close), got %q", got)
		}
	})

	t.Run("update_demote_epic_open_children_refuses", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "udeor")
		epic := bdProxiedCreate(t, bd, p.dir, "Demote epic", "-t", "epic")
		_ = bdProxiedCreate(t, bd, p.dir, "Demote child", "--parent", epic.ID)
		out := bdProxiedUpdateFail(t, bd, p.dir, epic.ID, "--type", "task")
		// beads-l7l3j generalized the error text "cannot demote epic" -> "cannot demote".
		if !strings.Contains(out, "cannot demote") {
			t.Errorf("expected 'cannot demote' guard error, got: %s", out)
		}
		db := openProxiedDB(t, p)
		if got := readIssueType(t, db, epic.ID); got != types.TypeEpic {
			t.Errorf("epic must stay an epic after a guarded demote, got %q", got)
		}
	})

	// beads-l7l3j: MOLECULE root demote (molecule->task) with an open child must
	// be refused too — the demote-axis widening the 2hkd guard had missed (it was
	// bare TypeEpic). Mutation: reverting update_proxied_server.go's predicate to
	// bare TypeEpic turns this RED (molecule demoted, rc=0).
	t.Run("update_demote_molecule_open_children_refuses", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "udmor")
		mol := bdProxiedCreate(t, bd, p.dir, "Demote molecule", "-t", "molecule")
		_ = bdProxiedCreate(t, bd, p.dir, "Demote mol child", "--parent", mol.ID)
		out := bdProxiedUpdateFail(t, bd, p.dir, mol.ID, "--type", "task")
		if !strings.Contains(out, "cannot demote") {
			t.Errorf("expected 'cannot demote' guard error for a molecule root, got: %s", out)
		}
		db := openProxiedDB(t, p)
		if got := readIssueType(t, db, mol.ID); got != types.TypeMolecule {
			t.Errorf("molecule must stay a molecule after a guarded demote, got %q", got)
		}
	})

	// beads-l7l3j control: molecule->EPIC (both auto-closing) is NOT a demote —
	// must succeed even with an open child (no false-positive from the widening).
	t.Run("update_molecule_to_epic_with_open_child_still_allowed", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "umte")
		mol := bdProxiedCreate(t, bd, p.dir, "Molecule to epic", "-t", "molecule")
		_ = bdProxiedCreate(t, bd, p.dir, "Mol child", "--parent", mol.ID)
		bdProxiedUpdateOne(t, bd, p.dir, mol.ID, "--type", "epic")
		db := openProxiedDB(t, p)
		if got := readIssueType(t, db, mol.ID); got != types.TypeEpic {
			t.Errorf("molecule->epic (both auto-closing, not a demote) should succeed, got %q", got)
		}
	})

	// beads-hfb4: `bd update --status in_progress` over the proxied server must
	// auto-set started_at (mirroring the shared seam's ManageStartedAt) — before
	// the fix the domain update path left it NULL, a silent data-fidelity gap.
	t.Run("update_in_progress_sets_started_at", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uipsa")
		issue := bdProxiedCreate(t, bd, p.dir, "Start me")
		db := openProxiedDB(t, p)
		var before sql.NullTime
		if err := db.QueryRowContext(context.Background(),
			"SELECT started_at FROM issues WHERE id = ?", issue.ID).Scan(&before); err != nil {
			t.Fatalf("read started_at before: %v", err)
		}
		if before.Valid {
			t.Fatalf("started_at should be NULL before in_progress, got %v", before.Time)
		}
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "-s", "in_progress")
		var after sql.NullTime
		if err := db.QueryRowContext(context.Background(),
			"SELECT started_at FROM issues WHERE id = ?", issue.ID).Scan(&after); err != nil {
			t.Fatalf("read started_at after: %v", err)
		}
		if !after.Valid {
			t.Errorf("update --status in_progress must set started_at, got NULL")
		}
	})

	t.Run("update_in_progress_preserves_existing_started_at", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uipse")
		issue := bdProxiedCreate(t, bd, p.dir, "Started already")
		// First transition sets started_at.
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "-s", "in_progress")
		db := openProxiedDB(t, p)
		var first sql.NullTime
		if err := db.QueryRowContext(context.Background(),
			"SELECT started_at FROM issues WHERE id = ?", issue.ID).Scan(&first); err != nil {
			t.Fatalf("read started_at after first: %v", err)
		}
		if !first.Valid {
			t.Fatalf("started_at should be set after first in_progress")
		}
		// Bounce to blocked and back to in_progress — the original started_at must survive.
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "-s", "blocked")
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "-s", "in_progress")
		var second sql.NullTime
		if err := db.QueryRowContext(context.Background(),
			"SELECT started_at FROM issues WHERE id = ?", issue.ID).Scan(&second); err != nil {
			t.Fatalf("read started_at after re-transition: %v", err)
		}
		if !second.Valid || !second.Time.Equal(first.Time) {
			t.Errorf("started_at must be preserved across re-transition: first=%v second=%v", first.Time, second.Time)
		}
	})

	// beads-n79c (root): `bd update --pinned` / `--no-pinned` over the proxied
	// server must set/clear the pinned marker — before the fix gatherUpdateInput
	// (the shared input gatherer the proxied server uses) never read these flags,
	// so they were a silent no-op over the proxied path.
	t.Run("update_pinned_flag_sets_marker", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "upfs")
		issue := bdProxiedCreate(t, bd, p.dir, "Pin via flag")
		db := openProxiedDB(t, p)
		if readPinnedCol(t, db, issue.ID) {
			t.Fatalf("pinned should be false on a fresh issue")
		}
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--pinned")
		if !readPinnedCol(t, db, issue.ID) {
			t.Errorf("bd update --pinned must set the marker over the proxied path (beads-n79c)")
		}
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--no-pinned")
		if readPinnedCol(t, db, issue.ID) {
			t.Errorf("bd update --no-pinned must clear the marker over the proxied path")
		}
	})

	// beads-y20w2 (was beads-n79c, INVERTED): moving status off "pinned" must NOT
	// auto-clear the independent pinned marker. Entering the pinned STATUS never
	// sets the column (only --pinned does; orthogonal per beads-9ynk), so the
	// former status-pinned-EXIT auto-clear could only strip a legitimate --pinned
	// shield = silent prune/purge data-loss. y20w2 removed the EXIT-leg auto-clear
	// in both the issueops seam and this domain/db proxied twin. The marker is now
	// managed solely by --pinned/--no-pinned.
	t.Run("update_status_off_pinned_keeps_pinned_marker", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uspin")
		issue := bdProxiedCreate(t, bd, p.dir, "Pinned marker")
		// Set the pinned bool marker and status=pinned.
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--pinned")
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "-s", "pinned")
		db := openProxiedDB(t, p)
		if !readPinnedCol(t, db, issue.ID) {
			t.Fatalf("pinned marker should be set before the status change")
		}
		// Move status off "pinned" with no --no-pinned → the shield must survive.
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "-s", "open")
		if !readPinnedCol(t, db, issue.ID) {
			t.Errorf("pinned marker was silently stripped when status moved off pinned (beads-y20w2); the column is orthogonal to status and must survive")
		}
		// But an explicit --no-pinned during a status change still clears it.
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "-s", "closed", "--no-pinned")
		if readPinnedCol(t, db, issue.ID) {
			t.Errorf("explicit --no-pinned must clear the marker even during a status change")
		}
	})

	t.Run("update_pinned_marker_survives_explicit_reset", func(t *testing.T) {
		// A caller who explicitly passes --pinned alongside a status change keeps
		// the marker, matching the shared seam. (Post-y20w2 the marker also survives
		// WITHOUT an explicit --pinned; this case pins the explicit-set path too.)
		p := bdProxiedInit(t, bd, "uspinx")
		issue := bdProxiedCreate(t, bd, p.dir, "Explicit pin")
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "-s", "pinned", "--pinned")
		db := openProxiedDB(t, p)
		if !readPinnedCol(t, db, issue.ID) {
			t.Fatalf("pinned marker should be set")
		}
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "-s", "open", "--pinned")
		if !readPinnedCol(t, db, issue.ID) {
			t.Errorf("an explicit --pinned alongside the status change must keep the marker")
		}
	})

	// beads-u3la5: a status-changing update to a COLUMN-pinned issue whose
	// STATUS is not "pinned" must NOT strip the pinned column. The pinned column
	// (--pinned) is an independent prune/purge shield orthogonal to the pinned
	// STATUS (beads-9ynk); before the fix the domain/proxied guard keyed on the
	// column (oldPinned) so `bd update --defer` silently cleared the shield.
	t.Run("update_defer_preserves_column_pin", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uspind")
		issue := bdProxiedCreate(t, bd, p.dir, "Shielded")
		// Set ONLY the pinned column — status stays open (orthogonal marker).
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--pinned")
		db := openProxiedDB(t, p)
		if !readPinnedCol(t, db, issue.ID) {
			t.Fatalf("pinned column should be set after --pinned")
		}
		// A status change that is NOT to "pinned" status must keep the shield.
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "-s", "deferred")
		if !readPinnedCol(t, db, issue.ID) {
			t.Errorf("pinned column was silently cleared by a status change (beads-u3la5); prune/purge shield lost")
		}
	})

	// beads-cwl8: a PARTIAL-failure update (one real id + one ghost id) must
	// exit non-zero, matching the direct path (cmd/bd/update.go returns
	// SilentExit when processedCount < len(args), beads-4i20). Previously the
	// proxied handler only exited non-zero when NO id succeeded, so a partial
	// batch returned rc=0 and a `bd update a b || fail` gate read false-clean.
	// The successful id's update must still be applied.
	t.Run("partial_failure_exits_nonzero", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "upe")
		real := bdProxiedCreate(t, bd, p.dir, "Real for partial", "--type", "task")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir,
			"update", real.ID, "upe-nonexistent999", "--assignee", "team")
		if err == nil {
			t.Fatalf("partial-failure update must exit non-zero; stdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		got := bdProxiedShow(t, bd, p.dir, real.ID)
		if got.Assignee != "team" {
			t.Errorf("the resolvable id should still update on a partial batch; assignee=%q, want team", got.Assignee)
		}
	})

	// beads-1d32: --append-notes is non-idempotent, so on a mixed valid/invalid
	// batch the proxied path must pre-resolve every id and bail before any write
	// — otherwise the good id gets the note, the batch exits non-zero, and the
	// retry double-appends. Contrast the partial_failure_exits_nonzero case
	// above: idempotent flags (--assignee) keep the cwl8 best-effort
	// partial-apply; this all-or-nothing guard is scoped to --append-notes only.
	t.Run("append_notes_bad_id_is_atomic", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uan")
		good := bdProxiedCreate(t, bd, p.dir, "Append atomicity", "--type", "task")
		missing := good.ID + "-nope-does-not-exist"

		out := bdProxiedUpdateFail(t, bd, p.dir, good.ID, missing, "--append-notes", "LOG-ENTRY")
		if strings.Contains(out, "storage is nil") {
			t.Fatalf("proxied update hit the nil-store path: %s", out)
		}
		got := bdProxiedShow(t, bd, p.dir, good.ID)
		if strings.Contains(got.Notes, "LOG-ENTRY") {
			t.Fatalf("--append-notes must NOT write the good id when a sibling id is bad "+
				"(non-idempotent partial-apply => double-append on retry); notes=%q", got.Notes)
		}

		// Retry with only the good id appends exactly once.
		bdProxiedUpdateOne(t, bd, p.dir, good.ID, "--append-notes", "LOG-ENTRY")
		got = bdProxiedShow(t, bd, p.dir, good.ID)
		if n := strings.Count(got.Notes, "LOG-ENTRY"); n != 1 {
			t.Fatalf("expected exactly one LOG-ENTRY after retry, got %d; notes=%q", n, got.Notes)
		}
	})

	// beads-6b9pz: `bd update --status closed` over the PROXIED server must
	// refuse closing an auto-closing parent (epic OR molecule/wisp root) that
	// still has open children — the forward-close twin of the guard bigro
	// (@cf791b036) widened only for `bd close`. checkProxiedUpdateCloseGuards
	// (update_proxied_server.go) previously gated on bare TypeEpic, so a
	// molecule/wisp root closed cleanly here while `bd close` refused it.
	// Mutation: restore `current.IssueType == types.TypeEpic` → the molecule
	// case goes rc=0 (RED); the epic control and the plain-task precision
	// control stay unchanged.
	t.Run("update_close_molecule_with_open_child_refused", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ucmol")
		root := bdProxiedCreate(t, bd, p.dir, "Molecule root", "-t", "molecule")
		bdProxiedCreate(t, bd, p.dir, "Open step", "--parent", root.ID)
		out := bdProxiedUpdateFail(t, bd, p.dir, root.ID, "-s", "closed")
		if !strings.Contains(out, "open child") {
			t.Errorf("expected an open-child close guard on proxied update-close of a molecule root, got: %s", out)
		}
		if got := bdProxiedShow(t, bd, p.dir, root.ID); got.Status != types.StatusOpen {
			t.Errorf("molecule root must remain open after a refused proxied update-close, got %s", got.Status)
		}
	})

	t.Run("update_close_molecule_with_open_child_force_overrides", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ucmolf")
		root := bdProxiedCreate(t, bd, p.dir, "Molecule root force", "-t", "molecule")
		bdProxiedCreate(t, bd, p.dir, "Open step force", "--parent", root.ID)
		bdProxiedUpdateOne(t, bd, p.dir, root.ID, "-s", "closed", "--force")
		if got := bdProxiedShow(t, bd, p.dir, root.ID); got.Status != types.StatusClosed {
			t.Errorf("--force must close a molecule root with open children over proxied, got %s", got.Status)
		}
	})

	t.Run("update_close_epic_with_open_child_still_refused", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ucepic")
		root := bdProxiedCreate(t, bd, p.dir, "Epic root", "-t", "epic")
		bdProxiedCreate(t, bd, p.dir, "Open child", "--parent", root.ID)
		out := bdProxiedUpdateFail(t, bd, p.dir, root.ID, "-s", "closed")
		if !strings.Contains(out, "open child") {
			t.Errorf("expected an open-child close guard on proxied update-close of an epic root (unchanged), got: %s", out)
		}
		if got := bdProxiedShow(t, bd, p.dir, root.ID); got.Status != types.StatusOpen {
			t.Errorf("epic root must remain open after a refused proxied update-close, got %s", got.Status)
		}
	})

	t.Run("update_close_plain_task_parent_with_open_child_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uctask")
		root := bdProxiedCreate(t, bd, p.dir, "Plain task parent", "-t", "task")
		bdProxiedCreate(t, bd, p.dir, "Open child", "--parent", root.ID)
		bdProxiedUpdateOne(t, bd, p.dir, root.ID, "-s", "closed")
		if got := bdProxiedShow(t, bd, p.dir, root.ID); got.Status != types.StatusClosed {
			t.Errorf("a plain task parent with an open child must update-close freely over proxied, got %s", got.Status)
		}
	})
}

// readPinnedCol reads the stored pinned bool directly from the proxied DB.
func readPinnedCol(t *testing.T, db *sql.DB, id string) bool {
	t.Helper()
	var pinned bool
	if err := db.QueryRowContext(context.Background(),
		"SELECT pinned FROM issues WHERE id = ?", id).Scan(&pinned); err != nil {
		t.Fatalf("read pinned for %s: %v", id, err)
	}
	return pinned
}

// readIssueType reads the stored issue_type directly from the proxied DB, for
// asserting a demote guard did (not) apply.
func readIssueType(t *testing.T, db *sql.DB, id string) types.IssueType {
	t.Helper()
	var it string
	if err := db.QueryRow("SELECT issue_type FROM issues WHERE id = ?", id).Scan(&it); err != nil {
		t.Fatalf("read issue_type for %s: %v", id, err)
	}
	return types.IssueType(it)
}

func TestProxiedServerUpdateHooks(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("on_update_fires_on_field_change", func(t *testing.T) {
		dir := t.TempDir()
		markerPath := filepath.Join(dir, "on_update_marker")
		hookBody := "#!/bin/sh\necho \"$1\" > " + markerPath + "\n"

		p := bdProxiedInitWithHooks(t, bd, "uph", map[string]string{
			"on_update": hookBody,
		})
		issue := bdProxiedCreate(t, bd, p.dir, "Hook test")

		_ = os.Remove(markerPath)
		stdout, stderr, runErr := bdProxiedUpdateRaw(t, bd, p.dir, issue.ID, "--title", "After update")
		if runErr != nil {
			t.Fatalf("bd update failed: %v\nstdout:\n%s\nstderr:\n%s", runErr, stdout, stderr)
		}

		gotID, err := waitForMarker(markerPath, 5*time.Second)
		if err != nil {
			t.Fatalf("on_update hook did not fire within timeout: %v\nstdout:\n%s\nstderr:\n%s",
				err, stdout, stderr)
		}
		if strings.TrimSpace(gotID) != issue.ID {
			t.Errorf("on_update marker: got %q, want issue ID %q", strings.TrimSpace(gotID), issue.ID)
		}
	})

	t.Run("on_close_fires_when_status_transitions_to_closed", func(t *testing.T) {
		dir := t.TempDir()
		markerPath := filepath.Join(dir, "on_close_marker")
		hookBody := "#!/bin/sh\necho \"$1\" > " + markerPath + "\n"

		p := bdProxiedInitWithHooks(t, bd, "uphc", map[string]string{
			"on_close": hookBody,
		})
		issue := bdProxiedCreate(t, bd, p.dir, "Hook close test")

		_ = os.Remove(markerPath)
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "-s", "closed")

		gotID, err := waitForMarker(markerPath, 5*time.Second)
		if err != nil {
			t.Fatalf("on_close hook did not fire within timeout: %v", err)
		}
		if strings.TrimSpace(gotID) != issue.ID {
			t.Errorf("on_close marker: got %q, want issue ID %q", strings.TrimSpace(gotID), issue.ID)
		}
	})
}

func waitForMarker(path string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			return string(data), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("marker %s not found after %s", path, timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestProxiedServerUpdateWisp(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("wisp_field_update_routes_to_wisps_table", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uwf")
		wisp := bdProxiedCreate(t, bd, p.dir, "Wisp field test", "--ephemeral")
		db := openProxiedDB(t, p)
		assertRowExists(t, db, "wisps", wisp.ID)
		assertRowAbsent(t, db, "issues", wisp.ID)

		updated := bdProxiedUpdateOne(t, bd, p.dir, wisp.ID, "--title", "wisp renamed", "-p", "0")
		if updated.Title != "wisp renamed" {
			t.Errorf("title: got %q, want %q", updated.Title, "wisp renamed")
		}
		if updated.Priority != 0 {
			t.Errorf("priority: got %d, want 0", updated.Priority)
		}

		var wispTitle string
		var wispPriority int
		s := db.QueryRowContext(context.Background(),
			"SELECT title, priority FROM wisps WHERE id = ?", wisp.ID).Scan(&wispTitle, &wispPriority)
		if s != nil {
			t.Fatalf("read wisp row: %v", s)
		}
		if wispTitle != "wisp renamed" || wispPriority != 0 {
			t.Errorf("wisps row: title=%q priority=%d, want (wisp renamed, 0)", wispTitle, wispPriority)
		}
	})

	t.Run("wisp_status_close_routes_to_wisps_table", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uws")
		wisp := bdProxiedCreate(t, bd, p.dir, "Wisp close test", "--ephemeral")

		bdProxiedUpdateOne(t, bd, p.dir, wisp.ID, "-s", "closed")

		db := openProxiedDB(t, p)
		var status string
		if err := db.QueryRowContext(context.Background(),
			"SELECT status FROM wisps WHERE id = ?", wisp.ID).Scan(&status); err != nil {
			t.Fatalf("read wisp status: %v", err)
		}
		if status != "closed" {
			t.Errorf("wisp status: got %q, want closed", status)
		}
	})

	t.Run("wisp_labels_route_to_wisp_labels", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uwl")
		wisp := bdProxiedCreate(t, bd, p.dir, "Wisp labels test", "--ephemeral")

		bdProxiedUpdateOne(t, bd, p.dir, wisp.ID, "--add-label", "alpha,beta")

		db := openProxiedDB(t, p)
		wispLabels := readLabels(t, db, "wisp_labels", wisp.ID)
		if got := strings.Join(wispLabels, ","); got != "alpha,beta" && got != "beta,alpha" {
			t.Errorf("wisp_labels: got %v, want [alpha beta] (any order)", wispLabels)
		}
		issueLabels := readLabels(t, db, "labels", wisp.ID)
		if len(issueLabels) != 0 {
			t.Errorf("labels table must be empty for wisp ids, got %v", issueLabels)
		}
	})

	t.Run("wisp_set_labels_diffs_against_wisp_labels", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uwsl")
		wisp := bdProxiedCreate(t, bd, p.dir, "Wisp set-labels test", "--ephemeral")

		bdProxiedUpdateOne(t, bd, p.dir, wisp.ID, "--add-label", "keep,drop")
		bdProxiedUpdateOne(t, bd, p.dir, wisp.ID, "--set-labels", "keep,add")

		db := openProxiedDB(t, p)
		got := strings.Join(readLabels(t, db, "wisp_labels", wisp.ID), ",")
		if got != "add,keep" && got != "keep,add" {
			t.Errorf("wisp_labels after set-labels: got %s, want [add keep] (any order)", got)
		}
	})

	t.Run("wisp_claim_routes_to_wisps_table", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uwc")
		wisp := bdProxiedCreate(t, bd, p.dir, "Wisp claim test", "--ephemeral")

		updated := bdProxiedUpdateOne(t, bd, p.dir, wisp.ID, "--claim", "--actor", "alice")
		if updated.Assignee != "alice" {
			t.Errorf("assignee: got %q, want alice", updated.Assignee)
		}
		if updated.Status != types.StatusInProgress {
			t.Errorf("status: got %q, want in_progress", updated.Status)
		}

		db := openProxiedDB(t, p)
		var assignee, status string
		if err := db.QueryRowContext(context.Background(),
			"SELECT assignee, status FROM wisps WHERE id = ?", wisp.ID).Scan(&assignee, &status); err != nil {
			t.Fatalf("read wisps row: %v", err)
		}
		if assignee != "alice" || status != "in_progress" {
			t.Errorf("wisps row: assignee=%q status=%q, want (alice, in_progress)", assignee, status)
		}
	})

	t.Run("wisp_reparent_routes_to_wisp_dependencies", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uwr")
		parent := bdProxiedCreate(t, bd, p.dir, "Wisp parent", "--ephemeral")
		child := bdProxiedCreate(t, bd, p.dir, "Wisp child", "--ephemeral")

		bdProxiedUpdateOne(t, bd, p.dir, child.ID, "--parent", parent.ID)

		db := openProxiedDB(t, p)
		var wispCount, permCount int
		if err := db.QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM wisp_dependencies WHERE issue_id = ? AND depends_on_wisp_id = ? AND type = 'parent-child'",
			child.ID, parent.ID).Scan(&wispCount); err != nil {
			t.Fatalf("read wisp_dependencies: %v", err)
		}
		if wispCount != 1 {
			t.Errorf("wisp_dependencies: got %d parent-child rows, want 1", wispCount)
		}
		if err := db.QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM dependencies WHERE issue_id = ?", child.ID).Scan(&permCount); err != nil {
			t.Fatalf("read dependencies: %v", err)
		}
		if permCount != 0 {
			t.Errorf("dependencies (issues table) must be empty for wisp reparent, got %d", permCount)
		}
	})

	t.Run("wisp_metadata_routes_to_wisps_table", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uwm")
		wisp := bdProxiedCreate(t, bd, p.dir, "Wisp metadata test", "--ephemeral")

		bdProxiedUpdateOne(t, bd, p.dir, wisp.ID, "--metadata", `{"src":"wisp"}`)

		db := openProxiedDB(t, p)
		var raw sql.NullString
		if err := db.QueryRowContext(context.Background(),
			"SELECT metadata FROM wisps WHERE id = ?", wisp.ID).Scan(&raw); err != nil {
			t.Fatalf("read wisp metadata: %v", err)
		}
		if !raw.Valid {
			t.Fatalf("wisp metadata: NULL, want JSON")
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(raw.String), &got); err != nil {
			t.Fatalf("parse wisp metadata %q: %v", raw.String, err)
		}
		if got["src"] != "wisp" {
			t.Errorf("metadata[src]: got %v, want %q", got["src"], "wisp")
		}
	})
}

func assertRowExists(t *testing.T, db *sql.DB, table, id string) {
	t.Helper()
	var count int
	q := "SELECT COUNT(*) FROM " + table + " WHERE id = ?"
	if err := db.QueryRowContext(context.Background(), q, id).Scan(&count); err != nil {
		t.Fatalf("read %s for %s: %v", table, id, err)
	}
	if count != 1 {
		t.Fatalf("%s row %s: count=%d, want 1", table, id, count)
	}
}

func assertRowAbsent(t *testing.T, db *sql.DB, table, id string) {
	t.Helper()
	var count int
	q := "SELECT COUNT(*) FROM " + table + " WHERE id = ?"
	if err := db.QueryRowContext(context.Background(), q, id).Scan(&count); err != nil {
		t.Fatalf("read %s for %s: %v", table, id, err)
	}
	if count != 0 {
		t.Fatalf("%s row %s: count=%d, want 0", table, id, count)
	}
}

func readLabels(t *testing.T, db *sql.DB, table, id string) []string {
	t.Helper()
	q := "SELECT label FROM " + table + " WHERE issue_id = ? ORDER BY label"
	rows, err := db.QueryContext(context.Background(), q, id)
	if err != nil {
		t.Fatalf("query %s for %s: %v", table, id, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			t.Fatalf("scan %s: %v", table, err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iter %s: %v", table, err)
	}
	return out
}

func TestProxiedServerUpdateConcurrentClaim(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	p := bdProxiedInit(t, bd, "ucc")
	issue := bdProxiedCreate(t, bd, p.dir, "Concurrent claim contest")

	const n = 5
	type result struct {
		actor    string
		exitErr  error
		stderr   string
		combined string
	}
	results := make([]result, n)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			actorName := fmt.Sprintf("alice-%d", i)
			stdout, stderr, err := bdProxiedUpdateRaw(t, bd, p.dir,
				issue.ID, "--claim", "--actor", actorName)
			results[i] = result{
				actor:    actorName,
				exitErr:  err,
				stderr:   stderr,
				combined: stdout + stderr,
			}
		}()
	}
	wg.Wait()

	var winners []string
	var conflicts int
	for _, r := range results {
		if r.exitErr == nil {
			winners = append(winners, r.actor)
			continue
		}
		isClaimedConflict := strings.Contains(r.combined, "already claimed")
		isSerializationFailure := strings.Contains(r.combined, "serialization failure") ||
			strings.Contains(r.combined, "Error 1213")
		if isClaimedConflict || isSerializationFailure {
			conflicts++
			continue
		}
		t.Errorf("unexpected failure for %s: err=%v combined=%s", r.actor, r.exitErr, r.combined)
	}

	if len(winners) != 1 {
		t.Errorf("expected exactly one winner, got %d: %v", len(winners), winners)
	}
	if conflicts != n-1 {
		t.Errorf("expected %d conflicts (claim or serialization), got %d", n-1, conflicts)
	}

	db := openProxiedDB(t, p)
	var assignee string
	var status string
	if err := db.QueryRowContext(context.Background(),
		"SELECT assignee, status FROM issues WHERE id = ?", issue.ID).Scan(&assignee, &status); err != nil {
		t.Fatalf("read final issue state: %v", err)
	}
	if status != "in_progress" {
		t.Errorf("final status: got %q, want in_progress", status)
	}
	if len(winners) == 1 && assignee != winners[0] {
		t.Errorf("final assignee: got %q, want %q (the actor that won the CAS)", assignee, winners[0])
	}
}

func readIsBlocked(t *testing.T, db *sql.DB, id string) bool {
	t.Helper()
	var v int
	if err := db.QueryRowContext(context.Background(),
		"SELECT is_blocked FROM issues WHERE id = ?", id).Scan(&v); err != nil {
		t.Fatalf("read is_blocked for %s: %v", id, err)
	}
	return v != 0
}
