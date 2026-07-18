//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerCommentsList proves the `bd comments <id>` SHOW/list parent
// is proxied-server-aware (beads-f2vu): the direct path uses
// result.Store.GetIssueComments, nil in proxiedServerMode → "storage is nil".
// This is a CLEAN-MIRROR read leg — CommentUseCase.GetCommentsForIssue already
// exists on the UOW, so no interface extension is needed, just routing.
func TestProxiedServerCommentsList(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("list_shows_added_comment", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cla")
		issue := bdProxiedCreate(t, bd, p.dir, "Comment list target", "--type", "task")
		// add is already proxied-routed (beads-m4vx, landed/held); use it to seed.
		if _, _, aerr := bdProxiedRunBuffers(t, bd, p.dir, "comments", "add", issue.ID, "listable comment"); aerr != nil {
			t.Fatalf("seed comment failed: %v", aerr)
		}

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "comments", issue.ID)
		if err != nil {
			t.Fatalf("bd comments (list) failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd comments list hit 'storage is nil' in proxied mode:\n%s\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, "listable comment") {
			t.Errorf("expected the comment in the listing:\n%s", stdout)
		}
	})

	t.Run("list_json", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "clj")
		issue := bdProxiedCreate(t, bd, p.dir, "Comment list json", "--type", "task")
		if _, _, aerr := bdProxiedRunBuffers(t, bd, p.dir, "comments", "add", issue.ID, "jsonc"); aerr != nil {
			t.Fatalf("seed comment failed: %v", aerr)
		}

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "comments", issue.ID, "--json")
		if err != nil {
			t.Fatalf("bd comments --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd comments --json hit 'storage is nil':\n%s\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, "[") || !strings.Contains(stdout, "jsonc") {
			t.Errorf("expected a JSON array with the comment:\n%s", stdout)
		}
	})

	t.Run("list_empty_no_comments", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cle")
		issue := bdProxiedCreate(t, bd, p.dir, "No comments", "--type", "task")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "comments", issue.ID)
		if err != nil {
			t.Fatalf("bd comments (empty) failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("empty-comments path hit 'storage is nil':\n%s\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, "No comments") {
			t.Errorf("expected 'No comments' output:\n%s", stdout)
		}
	})

	t.Run("list_nonexistent_fails", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cln")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "comments", "cln-nope999")
		if err == nil {
			t.Fatalf("expected comments list on a nonexistent id to fail; got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("nonexistent-id path hit 'storage is nil' rather than not-found:\n%s\n%s", stdout, stderr)
		}
	})
}
