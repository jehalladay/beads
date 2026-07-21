//go:build cgo

package main

import (
	"strings"
	"testing"
	"time"
)

// TestProxiedUpdateScalarNoOp is the teeth for beads-nfr6b: the proxied-server
// long-form `bd update` twin of the direct-path scalar-no-op honesty/write-skip
// (bdy2 honest "no change" + absq1 no updated_at bump), both of which landed
// DIRECT-path only ("CLI-layer only; internal/storage untouched").
//
// Once beads-j91h made a proxied no-op update succeed (instead of returning
// ErrNoRows), the shared proxied core (applyUpdateProxiedOne → ApplyUpdate →
// UpdateIssue) ran unconditionally on a scalar-only no-op — printing a fake
// "✓ Updated issue" AND bumping updated_at. That is absq1's exact integrity
// harm on the hub path: `bd stale` orders by updated_at ASC and derives
// daysStale from it, so a self-reported no-op silently reset the staleness
// clock and hid a stale issue.
//
// The fix guards in the LEAF handler (runUpdateProxiedServer), mirroring the
// landed proxied-twin family (helt4 priority / mpkza assign+tag): pre-resolve
// current and, when the update is scalar-only AND every set scalar already
// equals the current value, print an honest "already matches (no change)" and
// SKIP the write — leaving the shared core untouched. updated_at is asserted
// via `bd show --json` (UpdatedAt), the direct field the defect resets and the
// integrity signal for bd stale, matching the mpkza teeth.
func TestProxiedUpdateScalarNoOp(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// The core integrity leg: a scalar-only no-op (priority already at value)
	// must report "no change" and must NOT bump updated_at.
	t.Run("priority_noop_reports_no_change_and_does_not_bump_updated_at", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "upn1")
		a := bdProxiedCreate(t, bd, p.dir, "No-op priority", "--type", "task", "--priority", "2")

		before := bdProxiedShow(t, bd, p.dir, a.ID)
		if before.Priority != 2 {
			t.Fatalf("setup: priority = %d, want 2", before.Priority)
		}
		time.Sleep(1100 * time.Millisecond)

		out, err := bdProxiedRun(t, bd, p.dir, "update", a.ID, "--priority", "2")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied no-op update failed: %v\n%s", err, s)
		}
		if strings.Contains(s, "Updated issue") {
			t.Errorf("re-setting --priority to the current value must NOT print '✓ Updated issue'; got:\n%s", s)
		}
		if !strings.Contains(strings.ToLower(s), "no change") {
			t.Errorf("expected an honest 'already matches (no change)' line on a scalar-only no-op; got:\n%s", s)
		}

		after := bdProxiedShow(t, bd, p.dir, a.ID)
		if !after.UpdatedAt.Equal(before.UpdatedAt) {
			t.Errorf("a scalar-only no-op proxied update bumped updated_at (spurious write/commit, resets the bd-stale clock, beads-nfr6b): before=%v after=%v", before.UpdatedAt, after.UpdatedAt)
		}
		if after.Priority != 2 {
			t.Errorf("no-op update changed priority: want 2, got %d", after.Priority)
		}
	})

	// The same for --status on an already-open issue — the other common no-op.
	t.Run("status_noop_reports_no_change_and_does_not_bump_updated_at", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "upn2")
		a := bdProxiedCreate(t, bd, p.dir, "No-op status", "--type", "task")

		before := bdProxiedShow(t, bd, p.dir, a.ID)
		if string(before.Status) != "open" {
			t.Fatalf("setup: status = %q, want open", before.Status)
		}
		time.Sleep(1100 * time.Millisecond)

		out, err := bdProxiedRun(t, bd, p.dir, "update", a.ID, "--status", "open")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied no-op status update failed: %v\n%s", err, s)
		}
		if strings.Contains(s, "Updated issue") {
			t.Errorf("re-setting --status to the current value must NOT print '✓ Updated issue'; got:\n%s", s)
		}
		if !strings.Contains(strings.ToLower(s), "no change") {
			t.Errorf("expected an honest 'no change' line on a status no-op; got:\n%s", s)
		}

		after := bdProxiedShow(t, bd, p.dir, a.ID)
		if !after.UpdatedAt.Equal(before.UpdatedAt) {
			t.Errorf("a scalar-only status no-op proxied update bumped updated_at (beads-nfr6b): before=%v after=%v", before.UpdatedAt, after.UpdatedAt)
		}
	})

	// GUARD 1: a real scalar change MUST still write and bump updated_at — the
	// skip must not swallow a genuine mutation.
	t.Run("real_change_still_updates_and_bumps_updated_at", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "upn3")
		a := bdProxiedCreate(t, bd, p.dir, "Real change", "--type", "task", "--priority", "2")

		before := bdProxiedShow(t, bd, p.dir, a.ID)
		time.Sleep(1100 * time.Millisecond)

		out, err := bdProxiedRun(t, bd, p.dir, "update", a.ID, "--priority", "0")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied real update failed: %v\n%s", err, s)
		}
		if !strings.Contains(s, "Updated issue") {
			t.Errorf("a real scalar change (priority 2->0) must print '✓ Updated issue'; got:\n%s", s)
		}

		after := bdProxiedShow(t, bd, p.dir, a.ID)
		if after.UpdatedAt.Equal(before.UpdatedAt) {
			t.Errorf("a real scalar change MUST bump updated_at: before=%v after=%v", before.UpdatedAt, after.UpdatedAt)
		}
		if after.Priority != 0 {
			t.Errorf("real change did not persist: want priority 0, got %d", after.Priority)
		}
	})

	// GUARD 2: a same-value scalar combined with --append-notes is a real
	// mutation (append is non-idempotent) — it must fall through to the write,
	// report "✓ Updated", and bump updated_at. The skip fires ONLY on a
	// scalar-only no-op, never on a mixed/non-scalar update.
	t.Run("noop_scalar_with_append_notes_still_updates", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "upn4")
		a := bdProxiedCreate(t, bd, p.dir, "No-op scalar with notes", "--type", "task")

		before := bdProxiedShow(t, bd, p.dir, a.ID)
		time.Sleep(1100 * time.Millisecond)

		out, err := bdProxiedRun(t, bd, p.dir, "update", a.ID, "--status", "open", "--append-notes", "a real note")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied update with append-notes failed: %v\n%s", err, s)
		}
		if !strings.Contains(s, "Updated issue") {
			t.Errorf("a same-value scalar with --append-notes must print '✓ Updated issue' (the note is real); got:\n%s", s)
		}

		after := bdProxiedShow(t, bd, p.dir, a.ID)
		if after.UpdatedAt.Equal(before.UpdatedAt) {
			t.Errorf("--append-notes is a real mutation and MUST bump updated_at even with a same-value scalar: before=%v after=%v", before.UpdatedAt, after.UpdatedAt)
		}
	})

	// GUARD 3: the honest no-op must hold under --json — an array-shaped payload
	// with the (unchanged) issue, matching the change path and the direct update
	// path's --json no-op, and still no updated_at bump.
	t.Run("json_noop_emits_array_and_does_not_bump_updated_at", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "upn5")
		a := bdProxiedCreate(t, bd, p.dir, "JSON no-op", "--type", "task", "--priority", "1")

		before := bdProxiedShow(t, bd, p.dir, a.ID)
		time.Sleep(1100 * time.Millisecond)

		out, err := bdProxiedRun(t, bd, p.dir, "update", a.ID, "--priority", "1", "--json")
		s := strings.TrimSpace(string(out))
		if err != nil {
			t.Fatalf("proxied --json no-op update failed: %v\n%s", err, s)
		}
		if !strings.HasPrefix(s, "[") {
			t.Errorf("--json no-op update must emit an array (matching the change path); got:\n%s", s)
		}
		if !strings.Contains(s, a.ID) {
			t.Errorf("--json no-op array should contain the (unchanged) issue %s; got:\n%s", a.ID, s)
		}

		after := bdProxiedShow(t, bd, p.dir, a.ID)
		if !after.UpdatedAt.Equal(before.UpdatedAt) {
			t.Errorf("a --json scalar-only no-op must not bump updated_at (beads-nfr6b): before=%v after=%v", before.UpdatedAt, after.UpdatedAt)
		}
	})
}
