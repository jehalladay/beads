//go:build cgo

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func bdProxiedClose(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"close"}, args...)
	stdout, stderr, err := bdProxiedRunBuffers(t, bd, dir, fullArgs...)
	if err != nil {
		t.Fatalf("bd close %s failed: %v\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "), err, stdout, stderr)
	}
	return stdout
}

func bdProxiedCloseFail(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"close"}, args...)
	stdout, stderr, err := bdProxiedRunBuffers(t, bd, dir, fullArgs...)
	if err == nil {
		t.Fatalf("expected bd close %s to fail, got:\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "), stdout, stderr)
	}
	return stdout + stderr
}

func bdProxiedCloseJSON(t *testing.T, bd, dir string, args ...string) []*types.Issue {
	t.Helper()
	fullArgs := append([]string{"close", "--json"}, args...)
	stdout, stderr, err := bdProxiedRunBuffers(t, bd, dir, fullArgs...)
	if err != nil {
		t.Fatalf("bd close --json %s failed: %v\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "), err, stdout, stderr)
	}
	start := strings.Index(stdout, "[")
	if start < 0 {
		t.Fatalf("no JSON array in close output:\n%s", stdout)
	}
	var issues []*types.Issue
	if err := json.Unmarshal([]byte(stdout[start:]), &issues); err != nil {
		t.Fatalf("parse close JSON: %v\nraw: %s", err, stdout[start:])
	}
	return issues
}

func bdProxiedCloseJSONEnvelope(t *testing.T, bd, dir string, args ...string) map[string]json.RawMessage {
	t.Helper()
	fullArgs := append([]string{"close", "--json"}, args...)
	stdout, stderr, err := bdProxiedRunBuffers(t, bd, dir, fullArgs...)
	if err != nil {
		t.Fatalf("bd close --json %s failed: %v\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "), err, stdout, stderr)
	}
	start := strings.Index(stdout, "{")
	if start < 0 {
		t.Fatalf("no JSON object in close output:\n%s", stdout)
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout[start:]), &env); err != nil {
		t.Fatalf("parse close JSON envelope: %v\nraw: %s", err, stdout[start:])
	}
	return env
}

func readClosedBySession(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var got sql.NullString
	err := db.QueryRowContext(context.Background(),
		"SELECT closed_by_session FROM issues WHERE id = ?", id).Scan(&got)
	if err == sql.ErrNoRows {
		if err := db.QueryRowContext(context.Background(),
			"SELECT closed_by_session FROM wisps WHERE id = ?", id).Scan(&got); err != nil {
			t.Fatalf("read closed_by_session for %s: %v", id, err)
		}
	} else if err != nil {
		t.Fatalf("read closed_by_session for %s: %v", id, err)
	}
	if !got.Valid {
		return ""
	}
	return got.String
}

func readCloseReason(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var got sql.NullString
	err := db.QueryRowContext(context.Background(),
		"SELECT close_reason FROM issues WHERE id = ?", id).Scan(&got)
	if err == sql.ErrNoRows {
		if err := db.QueryRowContext(context.Background(),
			"SELECT close_reason FROM wisps WHERE id = ?", id).Scan(&got); err != nil {
			t.Fatalf("read close_reason for %s: %v", id, err)
		}
	} else if err != nil {
		t.Fatalf("read close_reason for %s: %v", id, err)
	}
	if !got.Valid {
		return ""
	}
	return got.String
}

func readStatus(t *testing.T, db *sql.DB, id string) types.Status {
	t.Helper()
	var got string
	err := db.QueryRowContext(context.Background(),
		"SELECT status FROM issues WHERE id = ?", id).Scan(&got)
	if err == sql.ErrNoRows {
		if err := db.QueryRowContext(context.Background(),
			"SELECT status FROM wisps WHERE id = ?", id).Scan(&got); err != nil {
			t.Fatalf("read status for %s: %v", id, err)
		}
	} else if err != nil {
		t.Fatalf("read status for %s: %v", id, err)
	}
	return types.Status(got)
}

func readAssignee(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var got sql.NullString
	err := db.QueryRowContext(context.Background(),
		"SELECT assignee FROM issues WHERE id = ?", id).Scan(&got)
	if err == sql.ErrNoRows {
		if err := db.QueryRowContext(context.Background(),
			"SELECT assignee FROM wisps WHERE id = ?", id).Scan(&got); err != nil {
			t.Fatalf("read assignee for %s: %v", id, err)
		}
	} else if err != nil {
		t.Fatalf("read assignee for %s: %v", id, err)
	}
	if !got.Valid {
		return ""
	}
	return got.String
}

func readDoltHead(t *testing.T, db *sql.DB) string {
	t.Helper()
	var h string
	if err := db.QueryRowContext(context.Background(), "SELECT HASHOF('HEAD')").Scan(&h); err != nil {
		t.Fatalf("read HEAD: %v", err)
	}
	return h
}

func readDoltLogTopMessage(t *testing.T, db *sql.DB) string {
	t.Helper()
	var msg string
	if err := db.QueryRowContext(context.Background(),
		"SELECT message FROM dolt_log ORDER BY date DESC LIMIT 1").Scan(&msg); err != nil {
		t.Fatalf("read latest dolt_log message: %v", err)
	}
	return msg
}

func readDoltLogCountSince(t *testing.T, db *sql.DB, sinceHash string) int {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		"SELECT commit_hash FROM dolt_log ORDER BY date DESC")
	if err != nil {
		t.Fatalf("read dolt_log: %v", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			t.Fatalf("scan dolt_log: %v", err)
		}
		if h == sinceHash {
			return n
		}
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iter dolt_log: %v", err)
	}
	return n
}

func TestProxiedServerClose(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("basic_close", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cb")
		issue := bdProxiedCreate(t, bd, p.dir, "Close me")
		bdProxiedClose(t, bd, p.dir, issue.ID)
		got := bdProxiedShow(t, bd, p.dir, issue.ID)
		if got.Status != types.StatusClosed {
			t.Errorf("status: got %q, want closed", got.Status)
		}
		if got.ClosedAt == nil {
			t.Error("closed_at should be set")
		}
	})

	t.Run("close_default_reason", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cdr")
		issue := bdProxiedCreate(t, bd, p.dir, "Default reason")
		bdProxiedClose(t, bd, p.dir, issue.ID)
		db := openProxiedDB(t, p)
		if got := readCloseReason(t, db, issue.ID); got != "Closed" {
			t.Errorf("close_reason: got %q, want %q", got, "Closed")
		}
	})

	t.Run("close_with_reason", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cwr")
		issue := bdProxiedCreate(t, bd, p.dir, "Reason test")
		bdProxiedClose(t, bd, p.dir, issue.ID, "--reason", "done")
		db := openProxiedDB(t, p)
		if got := readCloseReason(t, db, issue.ID); got != "done" {
			t.Errorf("close_reason: got %q, want %q", got, "done")
		}
	})

	t.Run("close_with_reason_short", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cwrs")
		issue := bdProxiedCreate(t, bd, p.dir, "Short reason")
		bdProxiedClose(t, bd, p.dir, issue.ID, "-r", "fixed")
		db := openProxiedDB(t, p)
		if got := readCloseReason(t, db, issue.ID); got != "fixed" {
			t.Errorf("close_reason: got %q, want %q", got, "fixed")
		}
	})

	t.Run("close_with_message_alias", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cwma")
		issue := bdProxiedCreate(t, bd, p.dir, "Message alias")
		bdProxiedClose(t, bd, p.dir, issue.ID, "-m", "via message")
		db := openProxiedDB(t, p)
		if got := readCloseReason(t, db, issue.ID); got != "via message" {
			t.Errorf("close_reason: got %q, want %q", got, "via message")
		}
	})

	t.Run("close_with_resolution_alias", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cwra")
		issue := bdProxiedCreate(t, bd, p.dir, "Resolution alias")
		bdProxiedClose(t, bd, p.dir, issue.ID, "--resolution", "wontfix")
		db := openProxiedDB(t, p)
		if got := readCloseReason(t, db, issue.ID); got != "wontfix" {
			t.Errorf("close_reason: got %q, want %q", got, "wontfix")
		}
	})

	t.Run("close_with_comment_alias", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cwca")
		issue := bdProxiedCreate(t, bd, p.dir, "Comment alias")
		bdProxiedClose(t, bd, p.dir, issue.ID, "--comment", "duplicate")
		db := openProxiedDB(t, p)
		if got := readCloseReason(t, db, issue.ID); got != "duplicate" {
			t.Errorf("close_reason: got %q, want %q", got, "duplicate")
		}
	})

	t.Run("close_multiple_ids", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cmi")
		a := bdProxiedCreate(t, bd, p.dir, "Multi A")
		b := bdProxiedCreate(t, bd, p.dir, "Multi B")
		bdProxiedClose(t, bd, p.dir, a.ID, b.ID)
		db := openProxiedDB(t, p)
		if got := readStatus(t, db, a.ID); got != types.StatusClosed {
			t.Errorf("a status: got %q, want closed", got)
		}
		if got := readStatus(t, db, b.ID); got != types.StatusClosed {
			t.Errorf("b status: got %q, want closed", got)
		}
	})

	t.Run("close_multiple_ids_with_per_id_reasons", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cmpr")
		a := bdProxiedCreate(t, bd, p.dir, "Multi reason A")
		b := bdProxiedCreate(t, bd, p.dir, "Multi reason B")
		bdProxiedClose(t, bd, p.dir, a.ID, "--reason", "fixed A", b.ID, "--reason", "fixed B")
		db := openProxiedDB(t, p)
		if got := readCloseReason(t, db, a.ID); got != "fixed A" {
			t.Errorf("a reason: got %q, want %q", got, "fixed A")
		}
		if got := readCloseReason(t, db, b.ID); got != "fixed B" {
			t.Errorf("b reason: got %q, want %q", got, "fixed B")
		}
	})

	t.Run("close_already_closed", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cac")
		issue := bdProxiedCreate(t, bd, p.dir, "Double close")
		bdProxiedClose(t, bd, p.dir, issue.ID, "--reason", "first")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "close", issue.ID, "--reason", "second")
		_ = stdout
		_ = stderr
		_ = err
		db := openProxiedDB(t, p)
		if got := readCloseReason(t, db, issue.ID); got != "first" {
			t.Errorf("re-close must not overwrite reason: got %q, want %q", got, "first")
		}
	})

	t.Run("close_nonexistent_id", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cni")
		out := bdProxiedCloseFail(t, bd, p.dir, "cni-does-not-exist")
		if !strings.Contains(out, "not found") {
			t.Errorf("expected 'not found' error, got: %s", out)
		}
	})

	t.Run("close_blocked_refuses_without_force", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cbr")
		blocker := bdProxiedCreate(t, bd, p.dir, "Blocker")
		blocked := bdProxiedCreate(t, bd, p.dir, "Blocked", "--deps", "depends-on:"+blocker.ID)
		out := bdProxiedCloseFail(t, bd, p.dir, blocked.ID)
		if !strings.Contains(out, "blocked by") {
			t.Errorf("expected blocker error, got: %s", out)
		}
		db := openProxiedDB(t, p)
		if got := readStatus(t, db, blocked.ID); got == types.StatusClosed {
			t.Error("blocked issue should remain open without --force")
		}
	})

	t.Run("close_blocked_with_force", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cbf")
		blocker := bdProxiedCreate(t, bd, p.dir, "Blocker force")
		blocked := bdProxiedCreate(t, bd, p.dir, "Blocked force", "--deps", "depends-on:"+blocker.ID)
		bdProxiedClose(t, bd, p.dir, blocked.ID, "--force")
		db := openProxiedDB(t, p)
		if got := readStatus(t, db, blocked.ID); got != types.StatusClosed {
			t.Errorf("status with --force: got %q, want closed", got)
		}
	})

	t.Run("close_pinned_refuses_without_force", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cpr")
		issue := bdProxiedCreate(t, bd, p.dir, "Pinned")
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "-s", "pinned")
		bdProxiedCloseFail(t, bd, p.dir, issue.ID)
		db := openProxiedDB(t, p)
		if got := readStatus(t, db, issue.ID); got == types.StatusClosed {
			t.Error("pinned issue should remain pinned without --force")
		}
	})

	t.Run("close_pinned_with_force", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cpf")
		issue := bdProxiedCreate(t, bd, p.dir, "Pinned force")
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "-s", "pinned")
		bdProxiedClose(t, bd, p.dir, issue.ID, "--force")
		db := openProxiedDB(t, p)
		if got := readStatus(t, db, issue.ID); got != types.StatusClosed {
			t.Errorf("status: got %q, want closed", got)
		}
	})

	t.Run("close_epic_open_children_refuses", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ceor")
		epic := bdProxiedCreate(t, bd, p.dir, "Epic", "-t", "epic")
		_ = bdProxiedCreate(t, bd, p.dir, "Child", "--parent", epic.ID)
		out := bdProxiedCloseFail(t, bd, p.dir, epic.ID)
		if !strings.Contains(out, "open child") {
			t.Errorf("expected 'open child' error, got: %s", out)
		}
	})

	t.Run("close_epic_open_children_force", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ceof")
		epic := bdProxiedCreate(t, bd, p.dir, "Epic force", "-t", "epic")
		_ = bdProxiedCreate(t, bd, p.dir, "Child force", "--parent", epic.ID)
		bdProxiedClose(t, bd, p.dir, epic.ID, "--force")
		db := openProxiedDB(t, p)
		if got := readStatus(t, db, epic.ID); got != types.StatusClosed {
			t.Errorf("status: got %q, want closed", got)
		}
	})

	t.Run("close_last_child_keeps_regular_epic_open", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "clce")
		epic := bdProxiedCreate(t, bd, p.dir, "Regular epic", "-t", "epic")
		child := bdProxiedCreate(t, bd, p.dir, "Last child", "--parent", epic.ID)
		bdProxiedClose(t, bd, p.dir, child.ID)
		db := openProxiedDB(t, p)
		if got := readStatus(t, db, epic.ID); got != types.StatusOpen {
			t.Errorf("regular epic should stay open after last child closes, got %q", got)
		}
	})

	t.Run("close_unblocks_dependent", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cud")
		blocker := bdProxiedCreate(t, bd, p.dir, "Unblock blocker")
		blocked := bdProxiedCreate(t, bd, p.dir, "Unblock blocked", "--deps", "depends-on:"+blocker.ID)
		db := openProxiedDB(t, p)
		if !readIsBlocked(t, db, blocked.ID) {
			t.Fatal("dependent should be blocked before blocker closes")
		}
		bdProxiedClose(t, bd, p.dir, blocker.ID)
		if readIsBlocked(t, db, blocked.ID) {
			t.Error("dependent should be unblocked after blocker closes")
		}
	})

	t.Run("close_suggest_next", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "csn")
		blocker := bdProxiedCreate(t, bd, p.dir, "Suggest blocker")
		blocked := bdProxiedCreate(t, bd, p.dir, "Suggest blocked", "--deps", "depends-on:"+blocker.ID)
		out := bdProxiedClose(t, bd, p.dir, blocker.ID, "--suggest-next")
		if !strings.Contains(out, blocked.ID) {
			t.Errorf("suggest-next output missing unblocked id %s:\n%s", blocked.ID, out)
		}
	})

	t.Run("close_suggest_next_json", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "csnj")
		blocker := bdProxiedCreate(t, bd, p.dir, "Suggest JSON blocker")
		blocked := bdProxiedCreate(t, bd, p.dir, "Suggest JSON blocked", "--deps", "depends-on:"+blocker.ID)
		env := bdProxiedCloseJSONEnvelope(t, bd, p.dir, blocker.ID, "--suggest-next")
		raw, ok := env["unblocked"]
		if !ok {
			t.Fatalf("envelope missing 'unblocked' key: %v", env)
		}
		var unblocked []*types.Issue
		if err := json.Unmarshal(raw, &unblocked); err != nil {
			t.Fatalf("parse unblocked: %v\n%s", err, string(raw))
		}
		if len(unblocked) != 1 || unblocked[0].ID != blocked.ID {
			t.Errorf("unblocked: got %+v, want [%s]", unblocked, blocked.ID)
		}
	})

	t.Run("close_claim_next", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ccn")
		toClose := bdProxiedCreate(t, bd, p.dir, "Claim next close")
		nextIssue := bdProxiedCreate(t, bd, p.dir, "Claim next target")
		bdProxiedClose(t, bd, p.dir, toClose.ID, "--claim-next")
		db := openProxiedDB(t, p)
		if got := readStatus(t, db, nextIssue.ID); got != types.StatusInProgress {
			t.Errorf("next issue status: got %q, want in_progress", got)
		}
		if got := readAssignee(t, db, nextIssue.ID); got == "" {
			t.Error("next issue assignee should be set after --claim-next")
		}
	})

	t.Run("close_claim_next_no_ready", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ccnr")
		issue := bdProxiedCreate(t, bd, p.dir, "Only issue")
		out := bdProxiedClose(t, bd, p.dir, issue.ID, "--claim-next")
		if !strings.Contains(out, "No ready issues") {
			t.Errorf("expected 'No ready issues', got: %s", out)
		}
	})

	t.Run("close_claim_next_json", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ccnj")
		toClose := bdProxiedCreate(t, bd, p.dir, "Claim JSON close")
		_ = bdProxiedCreate(t, bd, p.dir, "Claim JSON target")
		env := bdProxiedCloseJSONEnvelope(t, bd, p.dir, toClose.ID, "--claim-next")
		if _, ok := env["closed"]; !ok {
			t.Errorf("envelope missing 'closed' key: %v", env)
		}
		if _, ok := env["claimed"]; !ok {
			t.Errorf("envelope missing 'claimed' key: %v", env)
		}
	})

	t.Run("close_with_session", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cws")
		issue := bdProxiedCreate(t, bd, p.dir, "Session flag")
		bdProxiedClose(t, bd, p.dir, issue.ID, "--session", "sess-456")
		db := openProxiedDB(t, p)
		if got := readClosedBySession(t, db, issue.ID); got != "sess-456" {
			t.Errorf("closed_by_session: got %q, want %q", got, "sess-456")
		}
	})

	t.Run("close_session_from_env", func(t *testing.T) {
		t.Setenv("CLAUDE_SESSION_ID", "sess-env")
		p := bdProxiedInit(t, bd, "cse")
		issue := bdProxiedCreate(t, bd, p.dir, "Session env")
		bdProxiedClose(t, bd, p.dir, issue.ID)
		db := openProxiedDB(t, p)
		if got := readClosedBySession(t, db, issue.ID); got != "sess-env" {
			t.Errorf("closed_by_session: got %q, want %q", got, "sess-env")
		}
	})

	t.Run("close_json_output", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cjo")
		issue := bdProxiedCreate(t, bd, p.dir, "JSON close")
		issues := bdProxiedCloseJSON(t, bd, p.dir, issue.ID)
		if len(issues) != 1 || issues[0].ID != issue.ID {
			t.Errorf("close JSON: got %+v, want [%s]", issues, issue.ID)
		}
		if issues[0].Status != types.StatusClosed {
			t.Errorf("returned issue status: got %q, want closed", issues[0].Status)
		}
	})

	t.Run("done_alias", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "da")
		issue := bdProxiedCreate(t, bd, p.dir, "Done alias")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "done", issue.ID)
		if err != nil {
			t.Fatalf("bd done failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		db := openProxiedDB(t, p)
		if got := readStatus(t, db, issue.ID); got != types.StatusClosed {
			t.Errorf("status via done alias: got %q, want closed", got)
		}
	})

	t.Run("done_positional_reason", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "dpr")
		issue := bdProxiedCreate(t, bd, p.dir, "Done reason")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "done", issue.ID, "the reason")
		if err != nil {
			t.Fatalf("bd done with reason failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		db := openProxiedDB(t, p)
		if got := readCloseReason(t, db, issue.ID); got != "the reason" {
			t.Errorf("close_reason: got %q, want %q", got, "the reason")
		}
	})

	t.Run("close_continue_multiple_ids_fails", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ccmi")
		a := bdProxiedCreate(t, bd, p.dir, "Continue multi A")
		b := bdProxiedCreate(t, bd, p.dir, "Continue multi B")
		out := bdProxiedCloseFail(t, bd, p.dir, a.ID, b.ID, "--continue")
		if !strings.Contains(out, "single issue") {
			t.Errorf("expected single-issue error, got: %s", out)
		}
	})

	t.Run("close_suggest_next_multiple_ids_fails", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "csmi")
		a := bdProxiedCreate(t, bd, p.dir, "Suggest multi A")
		b := bdProxiedCreate(t, bd, p.dir, "Suggest multi B")
		out := bdProxiedCloseFail(t, bd, p.dir, a.ID, b.ID, "--suggest-next")
		if !strings.Contains(out, "single issue") {
			t.Errorf("expected single-issue error, got: %s", out)
		}
	})

	t.Run("single_transaction_dolt_commit", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "stdc")
		a := bdProxiedCreate(t, bd, p.dir, "Tx A")
		b := bdProxiedCreate(t, bd, p.dir, "Tx B")
		c := bdProxiedCreate(t, bd, p.dir, "Tx C")
		db := openProxiedDB(t, p)
		before := readDoltHead(t, db)
		bdProxiedClose(t, bd, p.dir, a.ID, b.ID, c.ID)
		count := readDoltLogCountSince(t, db, before)
		if count != 1 {
			t.Errorf("expected exactly 1 new dolt commit for batch close, got %d", count)
		}
		msg := readDoltLogTopMessage(t, db)
		for _, id := range []string{a.ID, b.ID, c.ID} {
			if !strings.Contains(msg, id) {
				t.Errorf("dolt commit message %q should contain id %s", msg, id)
			}
		}
		if !strings.HasPrefix(msg, "bd: close ") {
			t.Errorf("dolt commit message should begin with 'bd: close ', got: %q", msg)
		}
	})

	t.Run("no_ids_errors", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "nie")
		out := bdProxiedCloseFail(t, bd, p.dir)
		if !strings.Contains(out, "no issue ID provided") {
			t.Errorf("expected 'no issue ID provided', got: %s", out)
		}
	})

	t.Run("last_touched_not_supported", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ltns")
		_ = bdProxiedCreate(t, bd, p.dir, "Recent create")
		out := bdProxiedCloseFail(t, bd, p.dir)
		if !strings.Contains(out, "no issue ID provided") {
			t.Errorf("proxied mode must not fall back to last-touched; got: %s", out)
		}
	})

	t.Run("close_wisp_issue", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cwi")
		wisp := bdProxiedCreate(t, bd, p.dir, "Wisp close", "--ephemeral")
		bdProxiedClose(t, bd, p.dir, wisp.ID)
		db := openProxiedDB(t, p)
		var status string
		if err := db.QueryRowContext(context.Background(),
			"SELECT status FROM wisps WHERE id = ?", wisp.ID).Scan(&status); err != nil {
			t.Fatalf("read wisp status: %v", err)
		}
		if types.Status(status) != types.StatusClosed {
			t.Errorf("wisp status: got %q, want closed", status)
		}
	})

	t.Run("close_wisp_epic_open_children", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cweoc")
		wispEpic := bdProxiedCreate(t, bd, p.dir, "Wisp epic", "-t", "epic", "--ephemeral")
		_ = bdProxiedCreate(t, bd, p.dir, "Wisp child", "--ephemeral", "--parent", wispEpic.ID)
		out := bdProxiedCloseFail(t, bd, p.dir, wispEpic.ID)
		if !strings.Contains(out, "open child") {
			t.Errorf("expected 'open child' error for wisp epic, got: %s", out)
		}
	})

	t.Run("close_wisp_blocked_refuses", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cwbr")
		blocker := bdProxiedCreate(t, bd, p.dir, "Blocker for wisp")
		wisp := bdProxiedCreate(t, bd, p.dir, "Blocked wisp", "--ephemeral", "--deps", "depends-on:"+blocker.ID)
		out := bdProxiedCloseFail(t, bd, p.dir, wisp.ID)
		if !strings.Contains(out, "blocked by") {
			t.Errorf("expected wisp blocker error, got: %s", out)
		}
	})

	t.Run("continue_advances_to_next_ready_step", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cantrs")
		root := bdProxiedCreate(t, bd, p.dir, "Molecule root", "-t", "epic", "--labels", "template")
		step1 := bdProxiedCreate(t, bd, p.dir, "Step 1", "--parent", root.ID)
		step2 := bdProxiedCreate(t, bd, p.dir, "Step 2", "--parent", root.ID, "--deps", "depends-on:"+step1.ID)
		_ = bdProxiedCreate(t, bd, p.dir, "Step 3", "--parent", root.ID, "--deps", "depends-on:"+step2.ID)
		bdProxiedClose(t, bd, p.dir, step1.ID, "--continue")
		db := openProxiedDB(t, p)
		if got := readStatus(t, db, step2.ID); got != types.StatusInProgress {
			t.Errorf("step2 status after --continue: got %q, want in_progress", got)
		}
		if readAssignee(t, db, step2.ID) == "" {
			t.Error("step2 assignee should be set after --continue auto-claim")
		}
	})

	t.Run("continue_no_auto_does_not_claim", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cnac")
		root := bdProxiedCreate(t, bd, p.dir, "Molecule root", "-t", "epic", "--labels", "template")
		step1 := bdProxiedCreate(t, bd, p.dir, "Step 1", "--parent", root.ID)
		step2 := bdProxiedCreate(t, bd, p.dir, "Step 2", "--parent", root.ID, "--deps", "depends-on:"+step1.ID)
		bdProxiedClose(t, bd, p.dir, step1.ID, "--continue", "--no-auto")
		db := openProxiedDB(t, p)
		if got := readStatus(t, db, step2.ID); got != types.StatusOpen {
			t.Errorf("step2 status with --no-auto: got %q, want open", got)
		}
		if readAssignee(t, db, step2.ID) != "" {
			t.Error("step2 assignee should NOT be set with --no-auto")
		}
	})

	t.Run("auto_close_completed_molecule", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "accm")
		root := bdProxiedCreate(t, bd, p.dir, "Molecule root", "-t", "epic", "--labels", "template")
		s1 := bdProxiedCreate(t, bd, p.dir, "Step 1", "--parent", root.ID)
		s2 := bdProxiedCreate(t, bd, p.dir, "Step 2", "--parent", root.ID)
		bdProxiedClose(t, bd, p.dir, s1.ID, s2.ID)
		db := openProxiedDB(t, p)
		if got := readStatus(t, db, root.ID); got != types.StatusClosed {
			t.Errorf("template-labeled molecule root should auto-close after all steps complete, got %q", got)
		}
		if got := readCloseReason(t, db, root.ID); got != "all steps complete" {
			t.Errorf("auto-close reason: got %q, want %q", got, "all steps complete")
		}
	})

	t.Run("hooks_fire_on_close", func(t *testing.T) {
		marker := filepath.Join(t.TempDir(), "on_close_marker")
		script := "#!/bin/sh\nprintf '%s\\n' \"$1\" > " + shellQuote(marker) + "\n"
		if runtime.GOOS == "windows" {
			t.Skip("hook script form is POSIX shell")
		}
		p := bdProxiedInitWithHooks(t, bd, "hfc", map[string]string{"on_close": script})
		issue := bdProxiedCreate(t, bd, p.dir, "Hook fire")
		bdProxiedClose(t, bd, p.dir, issue.ID)
		data, err := os.ReadFile(marker)
		if err != nil {
			t.Fatalf("hook marker not written: %v", err)
		}
		if !strings.Contains(string(data), issue.ID) {
			t.Errorf("hook marker missing issue ID; got: %q", string(data))
		}
	})

	// beads-8l5t: re-closing an ALREADY-CLOSED issue must report the no-op
	// honestly ("already closed (no change)"), NOT a false "✓ Closed" transition,
	// and must NOT emit a second open→closed audit event. Mirrors the direct path
	// (cmd/bd/close.go) + the sibling proxied reopen guard.
	t.Run("close_already_closed_is_honest_noop", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cac")
		issue := bdProxiedCreate(t, bd, p.dir, "Double close")
		bdProxiedClose(t, bd, p.dir, issue.ID)
		// Second close: no-op. Must not falsely claim a transition.
		out := bdProxiedClose(t, bd, p.dir, issue.ID)
		if strings.Contains(out, "Closed "+issue.ID) || strings.Contains(out, "✓ Closed") {
			t.Errorf("re-close of already-closed falsely reported a transition:\n%s", out)
		}
		if !strings.Contains(out, "already closed") {
			t.Errorf("re-close should report 'already closed (no change)', got:\n%s", out)
		}
		// Audit trail must NOT contain a SECOND status open→closed field_change
		// from the no-op re-close.
		auditPath := filepath.Join(p.beadsDir, "interactions.jsonl")
		data, err := os.ReadFile(auditPath)
		if err == nil {
			statusCloses := 0
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				if line == "" {
					continue
				}
				var e struct {
					Kind  string         `json:"kind"`
					Extra map[string]any `json:"extra"`
				}
				if json.Unmarshal([]byte(line), &e) != nil {
					continue
				}
				if e.Kind == "field_change" {
					if f, _ := e.Extra["field"].(string); f == "status" {
						if nv, _ := e.Extra["new_value"].(string); nv == "closed" {
							statusCloses++
						}
					}
				}
			}
			if statusCloses > 1 {
				t.Errorf("re-close emitted a spurious duplicate open→closed audit event: got %d status→closed field_changes, want 1", statusCloses)
			}
		}
	})

	// beads-gt5p: a PARTIAL batch (some ids close, one is unresolvable) must
	// exit non-zero, matching the direct path (close.go — genuine failures trip
	// a non-zero exit alongside successes) and the update/cwl8 partial-exit
	// contract. Before the fix runCloseProxiedServer exited non-zero only when
	// ALL ids failed, so `bd close good-id ghost-id` returned rc=0 — a
	// false-clean read for a proxied crew scripting `bd close a b c || fail`.
	t.Run("partial_batch_failure_exits_nonzero", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cpe")
		good := bdProxiedCreate(t, bd, p.dir, "Good close", "--type", "task")

		out := bdProxiedCloseFail(t, bd, p.dir, good.ID, "ghost-9999", "--reason", "partial")
		if strings.Contains(out, "storage is nil") {
			t.Fatalf("proxied close hit the nil-store path: %s", out)
		}
		// The good id still closed (proxied processes the resolvable ids); the
		// non-zero exit comes from the unresolvable one.
		got := bdProxiedShow(t, bd, p.dir, good.ID)
		if got.Status != types.StatusClosed {
			t.Errorf("good id status after partial close = %q, want closed", got.Status)
		}
	})

	t.Run("all_bad_ids_exits_nonzero", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cae")
		out := bdProxiedCloseFail(t, bd, p.dir, "ghost-1", "ghost-2", "--reason", "none")
		if strings.Contains(out, "storage is nil") {
			t.Fatalf("proxied close hit the nil-store path: %s", out)
		}
	})
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func TestProxiedServerCloseConcurrent(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	p := bdProxiedInit(t, bd, "cxc")

	const (
		numWorkers      = 10
		issuesPerWorker = 5
	)

	type ws struct {
		closed int
		errs   []string
	}
	results := make([]ws, numWorkers)
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for w := 0; w < numWorkers; w++ {
		w := w
		go func() {
			defer wg.Done()
			r := &results[w]
			for i := 0; i < issuesPerWorker; i++ {
				title := fmt.Sprintf("worker-%d-issue-%d", w, i)
				cmd := exec.Command(bd, "create", "--json", title)
				cmd.Dir = p.dir
				cmd.Env = bdProxiedEnv(p.dir)
				var stdout, stderr bytes.Buffer
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				if err := cmd.Run(); err != nil {
					r.errs = append(r.errs, fmt.Sprintf("create %s: %v\n%s", title, err, stderr.String()))
					continue
				}
				out := stdout.Bytes()
				start := bytes.Index(out, []byte("{"))
				if start < 0 {
					r.errs = append(r.errs, "no JSON in create output")
					continue
				}
				var issue types.Issue
				if err := json.Unmarshal(out[start:], &issue); err != nil {
					r.errs = append(r.errs, fmt.Sprintf("parse create JSON: %v", err))
					continue
				}

				closeCmd := exec.Command(bd, "close", issue.ID)
				closeCmd.Dir = p.dir
				closeCmd.Env = bdProxiedEnv(p.dir)
				var cstdout, cstderr bytes.Buffer
				closeCmd.Stdout = &cstdout
				closeCmd.Stderr = &cstderr
				if err := closeCmd.Run(); err != nil {
					r.errs = append(r.errs, fmt.Sprintf("close %s: %v\n%s", issue.ID, err, cstderr.String()))
					continue
				}
				r.closed++
			}
		}()
	}
	wg.Wait()

	totalClosed := 0
	for w, r := range results {
		totalClosed += r.closed
		for _, e := range r.errs {
			t.Errorf("worker %d: %s", w, e)
		}
	}
	want := numWorkers * issuesPerWorker
	if totalClosed != want {
		t.Errorf("closed count: got %d, want %d", totalClosed, want)
	}

	db := openProxiedDB(t, p)
	var openCount int
	if err := db.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM issues WHERE status != 'closed'").Scan(&openCount); err != nil {
		t.Fatalf("query open count: %v", err)
	}
	if openCount != 0 {
		t.Errorf("open issues remain after concurrent close: %d", openCount)
	}
}

// TestProxiedServerCloseClaimNextConcurrent exercises the double-claim vector in
// `bd close --claim-next`: N workers each close their OWN decoy issue with
// --claim-next while exactly ONE shared target issue is the only claimable ready
// work. closeProxiedClaimNext does GetReadyWork -> ClaimIssue inside a
// UnitOfWork; on the shared Dolt sql-server each worker's transaction snapshot
// sees the same target open, both conditional-UPDATEs succeed in their own
// working set, and DOLT_COMMIT auto-merges without a serialization conflict ->
// the SAME target is handed to two actors (double-claim / lost update). This is
// the same class as TestProxiedServerReadyClaimConcurrent (beads-1i4u) but on a
// distinct, uncovered code path. Asserts exactly one worker wins the target AND
// that a lost claim-next race never rolls back the worker's primary close
// (beads-iq8zr — the close and claim-next used to share one commit).
func TestProxiedServerCloseClaimNextConcurrent(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "ccn")

	const numWorkers = 8
	// One decoy per worker, pre-claimed to that worker so it is in_progress (NOT
	// in the ready/open set) — each worker closes its OWN distinct decoy. Plus
	// one shared target that is the ONLY ready/open issue, so every worker's
	// --claim-next races for the SAME target row. A correct implementation lets
	// exactly one win; the double-claim bug hands it to two.
	decoys := make([]string, numWorkers)
	for w := 0; w < numWorkers; w++ {
		id := bdProxiedCreate(t, bd, p.dir, fmt.Sprintf("decoy-%d", w)).ID
		decoys[w] = id
		if _, err := bdProxiedRun(t, bd, p.dir, "update", id, "--claim",
			"--actor", fmt.Sprintf("worker-%d@test", w)); err != nil {
			t.Fatalf("pre-claim decoy %s: %v", id, err)
		}
	}
	target := bdProxiedCreate(t, bd, p.dir, "shared claim-next target")

	type result struct {
		claimedID string
		stdout    string
		stderr    string
		err       error
	}
	results := make([]result, numWorkers)
	var wg sync.WaitGroup
	wg.Add(numWorkers)
	for w := 0; w < numWorkers; w++ {
		go func(worker int) {
			defer wg.Done()
			cmd := exec.Command(bd, "close", decoys[worker], "--claim-next", "--json",
				"--actor", fmt.Sprintf("worker-%d@test", worker))
			cmd.Dir = p.dir
			cmd.Env = bdProxiedEnv(p.dir)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			r := result{stdout: stdout.String(), stderr: stderr.String(), err: err}
			s := strings.TrimSpace(r.stdout)
			if start := strings.Index(s, "{"); start >= 0 {
				var env struct {
					Claimed *types.Issue `json:"claimed"`
				}
				if jerr := json.Unmarshal([]byte(s[start:]), &env); jerr == nil && env.Claimed != nil {
					r.claimedID = env.Claimed.ID
				}
			}
			results[worker] = r
		}(w)
	}
	wg.Wait()

	winners := 0
	for i, r := range results {
		if r.claimedID == target.ID {
			winners++
		} else if r.claimedID != "" {
			t.Errorf("worker %d claimed unexpected issue %s (want target %s or none)", i, r.claimedID, target.ID)
		}
	}
	if winners != 1 {
		for i, r := range results {
			t.Logf("worker %d: claimed=%q err=%v\nstdout=%s\nstderr=%s", i, r.claimedID, r.err, r.stdout, r.stderr)
		}
		t.Fatalf("expected exactly one worker to claim the shared target, got %d", winners)
	}

	final := bdProxiedShow(t, bd, p.dir, target.ID)
	if final.Status != types.StatusInProgress {
		t.Errorf("target Status = %s, want in_progress", final.Status)
	}
	if final.Assignee == "" {
		t.Error("target Assignee empty after claim-next")
	}

	// Collateral-loss guard: close and claim-next share ONE UnitOfWork commit, so
	// a claim-next conflict must not silently roll back the worker's PRIMARY close.
	// Every decoy the worker was asked to close MUST end up closed, win or lose.
	for w, id := range decoys {
		got := bdProxiedShow(t, bd, p.dir, id)
		if got.Status != types.StatusClosed {
			t.Errorf("worker %d decoy %s Status = %s, want closed (primary close must survive a claim-next race)", w, id, got.Status)
		}
	}
}
