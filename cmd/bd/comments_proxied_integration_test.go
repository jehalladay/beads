//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerCommentsAdd proves bd comments add is proxied-server-aware
// (beads-m4vx): the direct path uses store.AddIssueComment, nil in
// proxiedServerMode → "storage is nil". CommentUseCase was read-only, so the fix
// is an interface-extension leg — AddComment added to CommentUseCase (backed by
// issueops.AddIssueCommentInTx widened *sql.Tx→DBTX) + proxied CLI routing.
func TestProxiedServerCommentsAdd(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("add_comment_then_show", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cma")
		issue := bdProxiedCreate(t, bd, p.dir, "Comment target", "--type", "task")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "comments", "add", issue.ID, "hello from proxied")
		if err != nil {
			t.Fatalf("bd comments add failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd comments add hit 'storage is nil' in proxied mode:\n%s\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, "Comment added") {
			t.Errorf("expected 'Comment added' output:\n%s", stdout)
		}
		// The comment must be readable back. Use `bd show --include-comments`
		// (proxied-routed) rather than the `bd comments <id>` SHOW parent, which
		// is a separate not-yet-routed read leg.
		out, _, rerr := bdProxiedRunBuffers(t, bd, p.dir, "show", issue.ID, "--include-comments")
		if rerr != nil {
			t.Fatalf("bd show --include-comments failed: %v", rerr)
		}
		if !strings.Contains(out, "hello from proxied") {
			t.Errorf("expected the added comment in show output:\n%s", out)
		}
	})

	t.Run("add_comment_json", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cmj")
		issue := bdProxiedCreate(t, bd, p.dir, "Comment json", "--type", "task")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "comments", "add", issue.ID, "jcomment", "--json")
		if err != nil {
			t.Fatalf("bd comments add --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd comments add --json hit 'storage is nil':\n%s\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, "jcomment") {
			t.Errorf("expected the comment text in JSON output:\n%s", stdout)
		}
	})

	t.Run("add_comment_nonexistent_fails", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cmn")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "comments", "add", "cmn-nope999", "text")
		if err == nil {
			t.Fatalf("expected comments add on a nonexistent id to fail; got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("nonexistent-id path hit 'storage is nil' rather than not-found:\n%s\n%s", stdout, stderr)
		}
	})
}
