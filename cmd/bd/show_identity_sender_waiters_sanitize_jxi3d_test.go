package main

import (
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestPrintProxiedThread_SanitizesSenderTo_jxi3d is the sanitize teeth for the
// From/To leg of beads-jxi3d (i8dsb identity-sink axis). The proxied thread
// view (printProxiedThread, show_proxied_server.go:433) printed
// "From: <msg.Sender>  To: <msg.Assignee>" RAW — the Subject one line below was
// covered by beads-u88a3 but the From/To identity line was missed. A mail
// Sender/Assignee is an untrusted actor-set identity that can carry OSC/CSI
// terminal-control escapes. printProxiedThread is a pure helper, so this drives
// the actual print site in-process (mirrors the u88a3 proxied precedent).
func TestPrintProxiedThread_SanitizesSenderTo_jxi3d(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	root := &types.Issue{
		ID: "msg-root", Title: "clean subject", IssueType: "message",
		Sender: "evilFrom" + csi + osc + "userA", Assignee: "evilTo" + osc + "userB",
	}
	threadMessages := []*types.Issue{root}
	repliesTo := map[string]string{}

	out := captureStdout(t, func() error {
		printProxiedThread(threadMessages, repliesTo, root)
		return nil
	})

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("proxied thread leaked a raw ESC (\\x1b) — From/To not sanitized (beads-jxi3d):\n%q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("proxied thread leaked a raw BEL (\\x07) — From/To not sanitized (beads-jxi3d):\n%q", out)
	}
	// Visible identity text must survive sanitize (escapes stripped, text kept),
	// and the From:/To: framing must remain.
	for _, want := range []string{"From:", "To:", "evilFromuserA", "evilTouserB"} {
		if !strings.Contains(out, want) {
			t.Errorf("proxied thread dropped visible From/To text %q (beads-jxi3d):\n%q", want, out)
		}
	}
}

// TestFormatIssueLongExtras_SanitizesWaiters_jxi3d is the sanitize teeth for the
// Waiters leg of beads-jxi3d. formatIssueLongExtras (show_format.go:326) printed
// "Waiters: <strings.Join(issue.Waiters, ", ")>" RAW. Gate waiter identities are
// mail addresses that can be import/proxied-sourced (untrusted), so an escape in
// a waiter reached the terminal. The fix sanitizes per-element before the Join.
// formatIssueLongExtras is a pure helper (see show_format_test.go).
func TestFormatIssueLongExtras_SanitizesWaiters_jxi3d(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"
	fmtTime := func(tm time.Time) string { return tm.Format("2006-01-02") }

	issue := &types.Issue{
		ID: "bd-gate", Status: types.StatusOpen,
		AwaitType: "gh:run", AwaitID: "release.yml", Timeout: 30 * time.Minute,
		Waiters: []string{"cleanWaiter", "evilW" + csi + osc + "userC"},
	}
	out := formatIssueLongExtras(issue, fmtTime)

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("long extras leaked a raw ESC (\\x1b) — Waiters not sanitized (beads-jxi3d):\n%q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("long extras leaked a raw BEL (\\x07) — Waiters not sanitized (beads-jxi3d):\n%q", out)
	}
	// Framing + both waiter identities' visible text survive.
	for _, want := range []string{"Waiters:", "cleanWaiter", "evilWuserC"} {
		if !strings.Contains(out, want) {
			t.Errorf("long extras dropped visible Waiters text %q (beads-jxi3d):\n%q", want, out)
		}
	}
}
