//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"
)

// TestEmbeddedReopenCommentVisible_bimd0 proves the bd reopen --reason is
// READABLE on the same surface a hand-added comment is (beads-bimd0). The
// direct (embedded/dolt) reopen path historically recorded the reason via
// store.AddComment -> the events table (AddCommentEventInTx), which NO read
// surface (bd show / bd comments) queries — those read the separate comments
// table (GetIssueCommentsInTx). So `bd reopen --reason X` displayed X but
// persisted it only as an invisible EventCommented row: bd comments showed
// nothing. Same class as beads-9l1it (promote). The fix routes the reopen
// reason through AddIssueComment[InTx] (comments table) at all three seams
// (embeddeddolt/dolt ReopenIssue + shared issueops.ReopenIssueInTx).
//
// Teeth: close then reopen --reason, then assert the reason text appears in
// `bd comments <id>`. Mutation-verify: revert the AddIssueComment calls back to
// AddComment and this test goes RED (comment invisible).
func TestEmbeddedReopenCommentVisible_bimd0(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "rcv")

	t.Run("reason_visible_in_comments", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Reopen reason visibility", "--type", "task")
		bdClose(t, bd, dir, issue.ID)

		const reason = "KEEPME-bimd0-qa-found-a-regression"
		out := bdReopen(t, bd, dir, issue.ID, "--reason", reason)
		if !strings.Contains(out, "Reopened") {
			t.Fatalf("expected 'Reopened' in output: %s", out)
		}

		// The reopen reason must be readable on the comments surface — the
		// whole point of the documented "recorded as a comment" behavior. A
		// pre-fix build lands it only in the events table, so this is empty.
		comments := bdCommentsShow(t, bd, dir, issue.ID)
		if !strings.Contains(comments, reason) {
			t.Errorf("reopen --reason %q not visible in `bd comments %s` (beads-bimd0: reopen wrote to the events table, not the readable comments table).\ngot:\n%s", reason, issue.ID, comments)
		}
	})

	t.Run("empty_reason_adds_no_comment", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Reopen no reason", "--type", "task")
		bdClose(t, bd, dir, issue.ID)

		bdReopen(t, bd, dir, issue.ID)

		// No --reason => no comment should be created (the reason != "" guard).
		comments := bdCommentsShow(t, bd, dir, issue.ID)
		if !strings.Contains(comments, "No comments") {
			t.Errorf("reopen without --reason should add no comment.\ngot:\n%s", comments)
		}
	})
}
