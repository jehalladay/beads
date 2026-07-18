//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerRename proves bd rename is proxied-server-aware
// (beads-lh54): before the fix the direct path called store.UpdateIssueID with
// a nil `store` in proxiedServerMode → "storage is nil". UpdateIssueID lived
// only on DoltStore, not the domain IssueUseCase, so the fix is an
// interface-extension leg — RenameIssueID added to IssueUseCase (backed by
// issueops.UpdateIssueIDInTx widened *sql.Tx→DBTX) + proxied CLI routing.
func TestProxiedServerRename(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("rename_updates_id", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "rpr")
		issue := bdProxiedCreate(t, bd, p.dir, "Rename me", "--type", "task")

		newID := issue.ID + "-renamed"
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "rename", issue.ID, newID)
		if err != nil {
			t.Fatalf("bd rename %s %s failed: %v\nstdout:\n%s\nstderr:\n%s", issue.ID, newID, err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd rename hit 'storage is nil' in proxied mode:\n%s\n%s", stdout, stderr)
		}
		// The new ID must now resolve; the old one must not.
		shown := bdProxiedShowRaw(t, bd, p.dir, newID)
		if !strings.Contains(shown, "Rename me") {
			t.Errorf("renamed issue %s not found after rename:\n%s", newID, shown)
		}
		oldOut, _ := bdProxiedShowFail(t, bd, p.dir, issue.ID)
		_ = oldOut
	})

	t.Run("rename_json", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "rpj")
		issue := bdProxiedCreate(t, bd, p.dir, "Rename json", "--type", "task")

		newID := issue.ID + "-j"
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "rename", issue.ID, newID, "--json")
		if err != nil {
			t.Fatalf("bd rename --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd rename --json hit 'storage is nil':\n%s\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, `"renamed"`) || !strings.Contains(stdout, newID) {
			t.Errorf("expected JSON rename payload with new_id %s:\n%s", newID, stdout)
		}
	})

	t.Run("rename_nonexistent_fails", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "rpn")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "rename", "rpn-nope999", "rpn-target")
		if err == nil {
			t.Fatalf("expected rename of a nonexistent id to fail; got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("nonexistent-id path hit 'storage is nil' rather than not-found:\n%s\n%s", stdout, stderr)
		}
	})
}
