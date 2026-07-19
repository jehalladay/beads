//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedServerStale proves `bd stale` is proxied-server-aware (beads-1xs1).
//
// Before the fix, runStale called the global `store` (store.GetStaleIssues),
// which is NIL in proxiedServerMode — `stale` is not a noDbCommand and had no
// ensureDirectMode guard — so it nil-panicked ("storage is nil") for
// hub-connected crew, unlike bd list/ready which route to the UOW. The fix
// widened issueops.GetStaleIssuesInTx (*sql.Tx→DBTX), exposed GetStaleIssues on
// the UOW IssueUseCase (mh3e/history precedent), and routes bd stale through a
// proxied fetch helper.
func TestProxiedServerStale(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("stale_does_not_nil_panic", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "stl")
		// A freshly-created issue is not stale at 30 days; --days 1 with no old
		// issues yields an empty stale set. The point is that the proxied path
		// executes the query without a nil-store panic.
		bdProxiedCreate(t, bd, p.dir, "Fresh issue", "--type", "task")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "stale")
		combined := stdout + stderr
		if strings.Contains(combined, "storage is nil") {
			t.Fatalf("bd stale hit 'storage is nil' in proxied mode (not proxied-server-aware):\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if strings.Contains(combined, "nil pointer dereference") || strings.Contains(combined, "panic:") {
			t.Fatalf("bd stale PANICKED in proxied mode (nil store deref):\nerr:%v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if err != nil {
			t.Fatalf("bd stale failed in proxied mode: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
	})

	t.Run("stale_json_does_not_nil_panic_and_is_array", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "slj")
		bdProxiedCreate(t, bd, p.dir, "Another fresh issue", "--type", "task")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "stale", "--json")
		combined := stdout + stderr
		if strings.Contains(combined, "storage is nil") || strings.Contains(combined, "nil pointer dereference") || strings.Contains(combined, "panic:") {
			t.Fatalf("bd stale --json hit nil store in proxied mode:\nerr:%v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if err != nil {
			t.Fatalf("bd stale --json failed in proxied mode: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		// Empty stale set must still serialize as a JSON array (not null), the
		// same contract the direct path honors (issues==nil → []).
		out := strings.TrimSpace(stdout)
		var arr []map[string]any
		if jerr := json.Unmarshal([]byte(out), &arr); jerr != nil {
			t.Fatalf("bd stale --json must emit a JSON array in proxied mode: %v\nstdout:\n%s", jerr, out)
		}
	})

	t.Run("stale_limit_does_not_nil_panic", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "sll")
		bdProxiedCreate(t, bd, p.dir, "Limit path issue", "--type", "task")

		// --limit exercises the separate fetch-one-extra truncation branch, which
		// also called the bare store.
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "stale", "--limit", "5")
		combined := stdout + stderr
		if strings.Contains(combined, "storage is nil") || strings.Contains(combined, "nil pointer dereference") || strings.Contains(combined, "panic:") {
			t.Fatalf("bd stale --limit hit nil store in proxied mode:\nerr:%v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if err != nil {
			t.Fatalf("bd stale --limit failed in proxied mode: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
	})
}
