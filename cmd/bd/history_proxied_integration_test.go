//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedServerHistory proves bd history is proxied-server-aware
// (beads-t3wg): before the fix the direct path called
// resolveAndGetIssueWithRouting(store, ...) with a nil `store` in
// proxiedServerMode → "storage is nil". History() was only on DoltStore, not
// the domain IssueUseCase, so the fix is an interface-extension leg (lh54-class)
// adding History() to IssueSQLRepository + IssueUseCase and routing bd history
// through the proxied UOW stack.
func TestProxiedServerHistory(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("history_after_update_lists_entries", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "hpu")
		issue := bdProxiedCreate(t, bd, p.dir, "History proxied", "--type", "task", "--priority", "3")
		// A second commit so history has >1 entry.
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--title", "History proxied updated")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "history", issue.ID)
		if err != nil {
			t.Fatalf("bd history %s failed: %v\nstdout:\n%s\nstderr:\n%s", issue.ID, err, stdout, stderr)
		}
		// The pre-fix failure mode was exactly this string.
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd history hit 'storage is nil' in proxied mode:\n%s\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, issue.ID) {
			t.Errorf("expected issue ID %s in history output:\n%s", issue.ID, stdout)
		}
		if !strings.Contains(stdout, "History for") {
			t.Errorf("expected 'History for' header in output:\n%s", stdout)
		}
	})

	t.Run("history_json_emits_array", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "hpj")
		issue := bdProxiedCreate(t, bd, p.dir, "History json proxied", "--type", "task")
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "-s", "in_progress")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "history", issue.ID, "--json")
		if err != nil {
			t.Fatalf("bd history --json %s failed: %v\nstdout:\n%s\nstderr:\n%s", issue.ID, err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd history --json hit 'storage is nil' in proxied mode:\n%s\n%s", stdout, stderr)
		}
		start := strings.Index(stdout, "[")
		if start < 0 {
			t.Fatalf("no JSON array in history --json output:\n%s", stdout)
		}
		var entries []map[string]interface{}
		if err := json.Unmarshal([]byte(stdout[start:]), &entries); err != nil {
			t.Fatalf("parse history JSON: %v\nraw: %s", err, stdout[start:])
		}
		if len(entries) == 0 {
			t.Errorf("expected at least one history entry, got 0:\n%s", stdout)
		}
	})

	t.Run("history_nonexistent_id_exits_nonzero", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "hpn")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "history", "hpn-nonexistent999")
		if err == nil {
			t.Fatalf("expected bd history on a nonexistent id to fail; got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("nonexistent-id path hit 'storage is nil' rather than a not-found error:\n%s\n%s", stdout, stderr)
		}
	})
}
