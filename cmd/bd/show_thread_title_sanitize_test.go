//go:build cgo

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestShowMessageThread_SanitizesTitle_s3qhv is the sanitize teeth for
// beads-s3qhv (7n9y sink-class slice). showMessageThread ('bd show <id>
// --thread') printed the thread's message titles (Subjects) RAW via bare
// fmt.Printf — line 90 (rootMsg.Title, the "Thread: <subject>" header) and
// line 117 (msg.Title, each message's "Subject: <subject>"). A message Title
// is its mail Subject, which per the mail protocol can be set from --stdin or
// another actor's message (untrusted), so it can carry OSC/CSI terminal-control
// escapes (OSC 52 clipboard / OSC 0 window-title). The fix routes both through
// displayTitle (ui.SanitizeForTerminal); display-only — the --json path
// (outputJSON(threadMessages)) stays raw.
//
// showMessageThread reads the package-global store, so this installs a seeded
// test store and calls the func directly (in-process), matching the sibling
// show_thread_json_error_test.go precedent.
func TestShowMessageThread_SanitizesTitle_s3qhv(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"
	rawTitle := "Danger" + csi + osc + "Title"

	tmpDir := t.TempDir()
	testStore := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()
	now := time.Now()

	msg := &types.Issue{
		ID: "msg-evil", Title: rawTitle,
		Description: "body", Status: types.StatusOpen, Priority: 2,
		IssueType: "message", Sender: "attacker", Assignee: "victim",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := testStore.CreateIssue(ctx, msg, "test"); err != nil {
		t.Fatalf("seed message create failed: %v", err)
	}

	// Install the seeded store as the package global showMessageThread reads,
	// with --json off (human render path). Restore on cleanup.
	oldStore, oldJSON := store, jsonOutput
	store, jsonOutput = testStore, false
	t.Cleanup(func() { store, jsonOutput = oldStore, oldJSON })

	stdout, _ := captureThreadStdoutStderr(t, func() {
		if err := showMessageThread(ctx, "msg-evil", false); err != nil {
			t.Errorf("showMessageThread returned error: %v", err)
		}
	})

	if strings.ContainsRune(stdout, '\x1b') {
		t.Errorf("show --thread leaked a raw ESC (\\x1b) — message title not sanitized (beads-s3qhv):\n%q", stdout)
	}
	if strings.ContainsRune(stdout, '\x07') {
		t.Errorf("show --thread leaked a raw BEL (\\x07) — message title not sanitized (beads-s3qhv):\n%q", stdout)
	}
	// Visible title text must survive sanitize (escapes stripped, text kept):
	// both the "Thread:" header (L90) and the "Subject:" line (L117) render it.
	if !strings.Contains(stdout, "DangerTitle") {
		t.Errorf("show --thread dropped visible title text (beads-s3qhv):\n%q", stdout)
	}
	if !strings.Contains(stdout, "Thread:") || !strings.Contains(stdout, "Subject") {
		t.Errorf("show --thread dropped structural output:\n%q", stdout)
	}
}
