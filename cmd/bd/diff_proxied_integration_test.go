//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerDiff proves bd diff is proxied-server-aware (beads-mh3e).
// Before the fix, diff.go called store.Diff() with a nil global `store` in
// proxiedServerMode (diff is not in noDbCommands) → "storage is nil" on
// hub-connected crew. Diff() lived on the storage HistoryViewer + concrete
// stores but NOT on the domain IssueUseCase, so the fix is an
// interface-extension leg (t3wg/lh54-class): add Diff() to IssueSQLRepository +
// IssueUseCase (delegating to the existing issueops.DiffInTx) and route bd diff
// through the proxied UOW stack via runDiffProxiedServer.
func TestProxiedServerDiff(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("diff_head_main_does_not_nil_panic", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "dpd")
		// Create + update so HEAD differs from an earlier commit; the exact diff
		// content is not asserted (dolt ref availability varies) — the teeth are
		// that the proxied path RESOLVES a store and does not nil-panic on the
		// nil global `store` (pre-fix: `store.Diff()` → nil pointer dereference).
		issue := bdProxiedCreate(t, bd, p.dir, "Diff proxied", "--type", "task", "--priority", "3")
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--title", "Diff proxied updated")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "diff", "HEAD", "main")
		combined := stdout + stderr
		if strings.Contains(combined, "storage is nil") {
			t.Fatalf("bd diff hit 'storage is nil' in proxied mode (not proxied-server-aware):\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		// Pre-fix, the nil global store is dereferenced → a raw Go panic.
		if strings.Contains(combined, "nil pointer dereference") || strings.Contains(combined, "panic:") {
			t.Fatalf("bd diff PANICKED in proxied mode (nil store deref) — not proxied-server-aware:\nerr:%v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
	})

	t.Run("diff_json_does_not_nil_panic", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "dpj")
		issue := bdProxiedCreate(t, bd, p.dir, "Diff json proxied", "--type", "task")
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "-s", "in_progress")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "diff", "HEAD", "main", "--json")
		combined := stdout + stderr
		if strings.Contains(combined, "storage is nil") {
			t.Fatalf("bd diff --json hit 'storage is nil' in proxied mode:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if strings.Contains(combined, "nil pointer dereference") || strings.Contains(combined, "panic:") {
			t.Fatalf("bd diff --json PANICKED in proxied mode (nil store deref):\nerr:%v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
	})
}
