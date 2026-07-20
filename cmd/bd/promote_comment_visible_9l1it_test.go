//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// bdCommentsShow runs "bd comments <id>" (the canonical read form) and returns
// stdout. The bdCommentList helper uses "comments list" which this build
// rejects, so read comments the way a user actually does.
func bdCommentsShow(t *testing.T, bd, dir, issueID string) string {
	t.Helper()
	cmd := exec.Command(bd, "comments", issueID)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd comments %s failed: %v\nstdout:\n%s\nstderr:\n%s", issueID, err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

// TestEmbeddedPromoteCommentVisible_9l1it proves the bd promote audit comment
// (and the user-supplied --reason) is READABLE on the same surface a
// hand-added comment is (beads-9l1it). The direct promote path historically
// wrote the record via store.AddComment -> the events table
// (AddCommentEventInTx), which NO read surface (bd show / bd comments) queries
// — it reads the separate comments table (GetIssueCommentsInTx). The fix routes
// promote through store.AddIssueComment (comments table), matching the
// already-correct proxied path (promote_proxied_server.go: CommentUseCase().AddComment).
//
// Teeth: promote a wisp with --reason, then assert the reason text appears in
// `bd comments list`. Mutation-verify: revert promote.go's AddIssueComment back
// to AddComment and this test goes RED (comment invisible).
func TestEmbeddedPromoteCommentVisible_9l1it(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "pcv")

	t.Run("reason_visible_in_comments", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Promote reason visibility", "--ephemeral")

		got := bdShow(t, bd, dir, issue.ID)
		if !got.Ephemeral {
			t.Skip("issue not ephemeral; cannot test promote")
		}

		const reason = "KEEPME-9l1it-audit-reason"
		out := bdPromote(t, bd, dir, issue.ID, "--reason", reason)
		if !strings.Contains(out, "Promoted") {
			t.Fatalf("expected 'Promoted' in output: %s", out)
		}

		// The promotion record must be readable on the comments surface —
		// the whole point of the documented "A comment is added recording the
		// promotion and optional reason." help text.
		comments := bdCommentsShow(t, bd, dir, issue.ID)
		if !strings.Contains(comments, reason) {
			t.Errorf("promotion --reason %q not visible in `bd comments list` (beads-9l1it: promote wrote to the events table, not the readable comments table).\ngot:\n%s", reason, comments)
		}
		if !strings.Contains(comments, "Promoted from wisp to permanent bead") {
			t.Errorf("promotion record body not visible in `bd comments list`.\ngot:\n%s", comments)
		}
	})

	t.Run("no_reason_still_records_promotion", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Promote no reason", "--ephemeral")

		got := bdShow(t, bd, dir, issue.ID)
		if !got.Ephemeral {
			t.Skip("issue not ephemeral; cannot test promote")
		}

		bdPromote(t, bd, dir, issue.ID)

		comments := bdCommentsShow(t, bd, dir, issue.ID)
		if !strings.Contains(comments, "Promoted from wisp to permanent bead") {
			t.Errorf("promotion record (no --reason) not visible in `bd comments list`.\ngot:\n%s", comments)
		}
	})
}
