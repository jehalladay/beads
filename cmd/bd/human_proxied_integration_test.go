//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerHumanDismiss covers beads-ivje (dismiss leg): `bd human
// dismiss` must work for hub-connected (proxied-server) crew. Previously
// human.go's dismiss handler called resolveAndGetIssueForMutation(nil store)
// with no usesProxiedServer() branch, and `human` is in noDbCommands so the
// store/UOW are never initialized — the command hit "storage is nil".
//
// The `bd human respond` leg is covered by TestProxiedServerHumanRespond below;
// it became buildable once CommentUseCase.AddComment landed (beads-m4vx), which
// unblocked the interface-extension the respond leg needed.
func TestProxiedServerHumanDismiss(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("dismiss_closes_human_bead", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "hdz1")
		iss := bdProxiedCreate(t, bd, p.dir, "Needs human input", "--type", "task", "--label", "human")

		out, err := bdProxiedRun(t, bd, p.dir, "human", "dismiss", iss.ID)
		if err != nil {
			t.Fatalf("proxied bd human dismiss failed: %v\n%s", err, out)
		}
		if strings.Contains(string(out), "storage is nil") {
			t.Fatalf("proxied human dismiss hit nil-store path (beads-ivje regression): %s", out)
		}
		if !strings.Contains(string(out), "dismissed") {
			t.Errorf("expected 'dismissed' confirmation, got: %s", out)
		}

		// Verify the issue is actually closed via a proxied show.
		show, err := bdProxiedRun(t, bd, p.dir, "show", iss.ID)
		if err != nil {
			t.Fatalf("proxied bd show failed: %v\n%s", err, show)
		}
		if !strings.Contains(strings.ToLower(string(show)), "closed") {
			t.Errorf("expected issue %s to be closed after dismiss, got:\n%s", iss.ID, show)
		}
	})

	t.Run("dismiss_already_closed_reports_error", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "hdz2")
		iss := bdProxiedCreate(t, bd, p.dir, "Already handled", "--type", "task", "--label", "human")
		// Close it first via the (proxied-aware) close path.
		if closeOut, closeErr := bdProxiedRun(t, bd, p.dir, "close", iss.ID, "--reason", "done"); closeErr != nil {
			t.Fatalf("proxied bd close failed: %v\n%s", closeErr, closeOut)
		}

		out, err := bdProxiedRun(t, bd, p.dir, "human", "dismiss", iss.ID)
		if err == nil {
			t.Fatalf("dismiss of an already-closed bead should fail; got:\n%s", out)
		}
		if strings.Contains(string(out), "storage is nil") {
			t.Fatalf("proxied human dismiss hit nil-store path (beads-ivje regression): %s", out)
		}
		if !strings.Contains(string(out), "already closed") {
			t.Errorf("expected 'already closed' message, got: %s", out)
		}
	})
}

// TestProxiedServerHumanRespond covers beads-ivje (respond leg): `bd human
// respond` must work for hub-connected (proxied-server) crew. Like dismiss,
// human.go's respond handler called resolveAndGetIssueForMutation(nil store)
// with no usesProxiedServer() branch, so it hit "storage is nil". The respond
// leg additionally records a "Response: <text>" comment (via
// CommentUseCase.AddComment, landed in beads-m4vx) before closing the bead.
func TestProxiedServerHumanRespond(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("respond_comments_and_closes_human_bead", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "hrz1")
		iss := bdProxiedCreate(t, bd, p.dir, "Needs human input", "--type", "task", "--label", "human")

		out, err := bdProxiedRun(t, bd, p.dir, "human", "respond", iss.ID, "--response", "Use OAuth2")
		if err != nil {
			t.Fatalf("proxied bd human respond failed: %v\n%s", err, out)
		}
		if strings.Contains(string(out), "storage is nil") {
			t.Fatalf("proxied human respond hit nil-store path (beads-ivje regression): %s", out)
		}
		if !strings.Contains(string(out), "closed with response") {
			t.Errorf("expected 'closed with response' confirmation, got: %s", out)
		}

		// Verify the issue is actually closed via a proxied show.
		show, err := bdProxiedRun(t, bd, p.dir, "show", iss.ID)
		if err != nil {
			t.Fatalf("proxied bd show failed: %v\n%s", err, show)
		}
		if !strings.Contains(strings.ToLower(string(show)), "closed") {
			t.Errorf("expected issue %s to be closed after respond, got:\n%s", iss.ID, show)
		}
		// Verify the response comment was recorded.
		if !strings.Contains(string(show), "Use OAuth2") {
			t.Errorf("expected response comment 'Use OAuth2' recorded on %s, got:\n%s", iss.ID, show)
		}
	})

	t.Run("respond_missing_response_reports_error", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "hrz2")
		iss := bdProxiedCreate(t, bd, p.dir, "Needs input", "--type", "task", "--label", "human")

		// No --response flag: cobra's MarkFlagRequired guard runs before any store
		// use, so this must error the same way in proxied mode (never "storage is
		// nil").
		out, err := bdProxiedRun(t, bd, p.dir, "human", "respond", iss.ID)
		if err == nil {
			t.Fatalf("respond without --response should fail; got:\n%s", out)
		}
		if strings.Contains(string(out), "storage is nil") {
			t.Fatalf("proxied human respond hit nil-store path (beads-ivje regression): %s", out)
		}
		if !strings.Contains(string(out), `required flag(s) "response" not set`) {
			t.Errorf(`expected 'required flag(s) "response" not set', got: %s`, out)
		}
	})

	t.Run("respond_already_closed_reports_error", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "hrz3")
		iss := bdProxiedCreate(t, bd, p.dir, "Already handled", "--type", "task", "--label", "human")
		if closeOut, closeErr := bdProxiedRun(t, bd, p.dir, "close", iss.ID, "--reason", "done"); closeErr != nil {
			t.Fatalf("proxied bd close failed: %v\n%s", closeErr, closeOut)
		}

		out, err := bdProxiedRun(t, bd, p.dir, "human", "respond", iss.ID, "--response", "late")
		if err == nil {
			t.Fatalf("respond to an already-closed bead should fail; got:\n%s", out)
		}
		if strings.Contains(string(out), "storage is nil") {
			t.Fatalf("proxied human respond hit nil-store path (beads-ivje regression): %s", out)
		}
		if !strings.Contains(string(out), "already closed") {
			t.Errorf("expected 'already closed' message, got: %s", out)
		}
	})
}
