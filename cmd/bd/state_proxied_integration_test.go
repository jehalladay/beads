//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerSetState proves bd set-state is proxied-server-aware
// (beads-nzb7): set-state is a multi-write (GetNextChildID + CreateIssue event
// + AddDependency parent-child + label swap). Before the fix it used the nil
// global `store` in proxiedServerMode → "storage is nil". GetNextChildID lived
// only on DoltStore, not the domain IssueUseCase, so the fix is an
// interface-extension leg (GetNextChildID added to IssueUseCase, backed by
// issueops.GetNextChildIDTx widened *sql.Tx→DBTX) + proxied CLI routing.
func TestProxiedServerSetState(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("set_state_creates_label_and_event", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "sps")
		issue := bdProxiedCreate(t, bd, p.dir, "State target", "--type", "task")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "set-state", issue.ID, "patrol=active", "--reason", "test")
		if err != nil {
			t.Fatalf("bd set-state failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd set-state hit 'storage is nil' in proxied mode:\n%s\n%s", stdout, stderr)
		}
		// The state label must now be queryable.
		out := bdProxiedShowRaw(t, bd, p.dir, issue.ID)
		if !strings.Contains(out, "patrol:active") {
			t.Errorf("expected patrol:active label after set-state:\n%s", out)
		}
	})

	t.Run("set_state_json_changed", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "spj")
		issue := bdProxiedCreate(t, bd, p.dir, "State json", "--type", "task")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "set-state", issue.ID, "mode=degraded", "--json")
		if err != nil {
			t.Fatalf("bd set-state --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd set-state --json hit 'storage is nil':\n%s\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, `"changed"`) || !strings.Contains(stdout, "degraded") {
			t.Errorf("expected JSON payload with changed + new value:\n%s", stdout)
		}
	})

	t.Run("set_state_idempotent_no_change", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "spi")
		issue := bdProxiedCreate(t, bd, p.dir, "State idem", "--type", "task")

		// Set once, then set the same value again — second is a no-op.
		if _, _, err := bdProxiedRunBuffers(t, bd, p.dir, "set-state", issue.ID, "health=healthy"); err != nil {
			t.Fatalf("first set-state failed: %v", err)
		}
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "set-state", issue.ID, "health=healthy")
		if err != nil {
			t.Fatalf("second set-state failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if !strings.Contains(stdout, "no change") {
			t.Errorf("expected 'no change' on re-setting the same value:\n%s", stdout)
		}
	})

	t.Run("set_state_nonexistent_fails", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "spn")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "set-state", "spn-nope999", "patrol=active")
		if err == nil {
			t.Fatalf("expected set-state on a nonexistent id to fail; got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("nonexistent-id path hit 'storage is nil' rather than not-found:\n%s\n%s", stdout, stderr)
		}
	})
}
