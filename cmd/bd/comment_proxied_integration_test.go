//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerComment proves bd comment (singular shorthand) is
// proxied-server-aware (beads-xjtk). Like its m4vx sibling `bd comments add`,
// the direct path called resolveAndGetIssueForMutation(nil store) →
// result.Store.AddIssueComment with no usesProxiedServer() branch, so
// hub-connected crew hit "storage is nil". It reuses CommentUseCase.AddComment
// (added by m4vx), plus the singular path's validateIssueUpdatable +
// SetLastTouchedID.
func TestProxiedServerComment(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("comment_then_show", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cshz1")
		issue := bdProxiedCreate(t, bd, p.dir, "Comment target", "--type", "task")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "comment", issue.ID, "hello via shorthand")
		if err != nil {
			t.Fatalf("bd comment failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd comment hit 'storage is nil' in proxied mode (beads-xjtk regression):\n%s\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, "Comment added") {
			t.Errorf("expected 'Comment added' output:\n%s", stdout)
		}
		// The comment must be readable back via proxied-routed show.
		out, _, rerr := bdProxiedRunBuffers(t, bd, p.dir, "show", issue.ID, "--include-comments")
		if rerr != nil {
			t.Fatalf("bd show --include-comments failed: %v", rerr)
		}
		if !strings.Contains(out, "hello via shorthand") {
			t.Errorf("expected the added comment in show output:\n%s", out)
		}
	})

	t.Run("comment_json", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cshz2")
		issue := bdProxiedCreate(t, bd, p.dir, "Comment json", "--type", "task")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "comment", issue.ID, "jshorthand", "--json")
		if err != nil {
			t.Fatalf("bd comment --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd comment --json hit 'storage is nil':\n%s\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, "jshorthand") {
			t.Errorf("expected the comment text in JSON output:\n%s", stdout)
		}
	})

	t.Run("comment_nonexistent_fails", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cshz3")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "comment", "cshz3-nope999", "text")
		if err == nil {
			t.Fatalf("expected comment on a nonexistent id to fail; got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("nonexistent-id path hit 'storage is nil' rather than not-found:\n%s\n%s", stdout, stderr)
		}
	})
}
