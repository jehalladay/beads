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

	// beads-brk7c (proxied twin): set-state must clear ALL same-dimension labels,
	// not just the first — the proxied path had the identical break-on-first bug.
	t.Run("set_state_clears_all_duplicate_dimension_labels", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "spd")
		issue := bdProxiedCreate(t, bd, p.dir, "brk7c proxied multi", "--type", "task")

		// Seed two mode: labels — one via set-state, a sibling via `bd label add`.
		if _, _, err := bdProxiedRunBuffers(t, bd, p.dir, "set-state", issue.ID, "mode=normal"); err != nil {
			t.Fatalf("seed set-state failed: %v", err)
		}
		if _, err := bdProxiedRun(t, bd, p.dir, "label", "add", issue.ID, "mode:degraded"); err != nil {
			t.Fatalf("seed label add failed: %v", err)
		}

		// set-state to a third value must remove BOTH stale siblings.
		if _, _, err := bdProxiedRunBuffers(t, bd, p.dir, "set-state", issue.ID, "mode=failing"); err != nil {
			t.Fatalf("set-state mode=failing failed: %v", err)
		}

		show := bdProxiedShowRaw(t, bd, p.dir, issue.ID)
		if strings.Contains(show, "mode:normal") || strings.Contains(show, "mode:degraded") {
			t.Errorf("proxied set-state left a stale mode sibling (brk7c):\n%s", show)
		}
		if !strings.Contains(show, "mode:failing") {
			t.Errorf("expected mode:failing after proxied set-state:\n%s", show)
		}

		// The two read surfaces must agree on the value.
		single, err := bdProxiedRun(t, bd, p.dir, "state", issue.ID, "mode")
		if err != nil {
			t.Fatalf("proxied bd state read failed: %v", err)
		}
		listOut, err := bdProxiedRun(t, bd, p.dir, "state", "list", issue.ID)
		if err != nil {
			t.Fatalf("proxied bd state list failed: %v", err)
		}
		if !strings.Contains(string(single), "failing") {
			t.Errorf("proxied bd state mode should be 'failing', got: %s", single)
		}
		if strings.Contains(string(listOut), "normal") || strings.Contains(string(listOut), "degraded") {
			t.Errorf("proxied bd state list leaked a stale sibling (brk7c): %s", listOut)
		}
		if !strings.Contains(string(listOut), "failing") {
			t.Errorf("proxied bd state list should show mode=failing, got: %s", listOut)
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

// TestProxiedServerStateRead is the teeth for beads-i3hq: the `bd state <id>
// <dim>` and `bd state list <id>` READ paths must WORK in proxied-server mode.
// Before the fix, both resolved+read via the direct nil global `store` in
// proxiedServerMode (state.go: utils.ResolvePartialID(ctx, store, ...) +
// store.GetLabels) with no usesProxiedServer() routing, so they failed
// "storage is nil" for hub-connected crew. Sibling of beads-nzb7 (the set-state
// WRITE path above); this covers the read handlers. Mirrors show/list proxied
// read handlers (uw.LabelUseCase().GetLabels).
func TestProxiedServerStateRead(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("state_read_single_dimension", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "psr1")
		a := bdProxiedCreate(t, bd, p.dir, "State me", "--type", "task")

		// Seed a state label via `bd label add` (already proxied-aware).
		if _, err := bdProxiedRun(t, bd, p.dir, "label", "add", a.ID, "patrol:active"); err != nil {
			t.Fatalf("seed label add failed: %v", err)
		}

		out, err := bdProxiedRun(t, bd, p.dir, "state", a.ID, "patrol")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied bd state read failed: %v\n%s", err, s)
		}
		if strings.Contains(s, "storage is nil") {
			t.Fatalf("proxied bd state hit the nil-store path (beads-i3hq regression): %s", s)
		}
		if !strings.Contains(s, "active") {
			t.Errorf("expected 'active' from proxied bd state patrol, got: %s", s)
		}
	})

	t.Run("state_read_unset_dimension", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "psr2")
		a := bdProxiedCreate(t, bd, p.dir, "No state", "--type", "task")

		out, err := bdProxiedRun(t, bd, p.dir, "state", a.ID, "mode")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied bd state (unset) failed: %v\n%s", err, s)
		}
		if strings.Contains(s, "storage is nil") {
			t.Fatalf("proxied bd state (unset) hit the nil-store path: %s", s)
		}
		if !strings.Contains(s, "no mode state set") {
			t.Errorf("expected '(no mode state set)' for unset dimension, got: %s", s)
		}
	})

	t.Run("state_list", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "psr3")
		a := bdProxiedCreate(t, bd, p.dir, "Multi state", "--type", "task")

		for _, lbl := range []string{"patrol:muted", "mode:degraded"} {
			if _, err := bdProxiedRun(t, bd, p.dir, "label", "add", a.ID, lbl); err != nil {
				t.Fatalf("seed label add %s failed: %v", lbl, err)
			}
		}

		out, err := bdProxiedRun(t, bd, p.dir, "state", "list", a.ID)
		s := string(out)
		if err != nil {
			t.Fatalf("proxied bd state list failed: %v\n%s", err, s)
		}
		if strings.Contains(s, "storage is nil") {
			t.Fatalf("proxied bd state list hit the nil-store path (beads-i3hq regression): %s", s)
		}
		if !strings.Contains(s, "patrol") || !strings.Contains(s, "muted") {
			t.Errorf("expected patrol:muted in proxied bd state list, got: %s", s)
		}
		if !strings.Contains(s, "mode") || !strings.Contains(s, "degraded") {
			t.Errorf("expected mode:degraded in proxied bd state list, got: %s", s)
		}
	})
}
