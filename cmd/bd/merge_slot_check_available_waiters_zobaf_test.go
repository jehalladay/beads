//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestMergeSlotCheckAvailableShowsWaiters_zobaf is the teeth for beads-zobaf:
// `bd merge-slot check` (HUMAN render) dropped the queued-waiters list on the
// AVAILABLE branch — it printed only "✓ Merge slot available: <id>" — while the
// --json leg always emitted "waiters". MergeSlotCheckImpl computes Available
// (status==Open) and Waiters (meta.Waiters) as INDEPENDENT fields, and release
// prunes only the HOLDER from the waiters (beads-vmwzn), so an "available slot
// WITH a non-empty FIFO waiters queue" is a normal reachable state after any
// release that had ≥1 non-holder waiter. A merge driver polling the human check
// then saw a bare "available" and could barge in ahead of already-queued waiters,
// defeating the queue's serialization. Fix renders the waiters in both branches.
//
// Repro (from the bead): create; alice acquire; bob/carol acquire --wait (queued);
// alice release (holder pruned, bob+carol RETAINED). Now the slot is Available but
// bob+carol are still queued — the human check must surface them.
func TestMergeSlotCheckAvailableShowsWaiters_zobaf(t *testing.T) {
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ms")

	if out, err := bdRunAsActor(t, bd, dir, "alice", "merge-slot", "create"); err != nil {
		t.Fatalf("merge-slot create should succeed: %v\n%s", err, out)
	}
	if out, err := bdRunAsActor(t, bd, dir, "alice", "merge-slot", "acquire"); err != nil {
		t.Fatalf("alice acquire should succeed: %v\n%s", err, out)
	}
	// bob + carol queue behind alice (--wait returns immediately after queueing).
	// NOTE: `acquire --wait` on a held slot exits NON-ZERO via SilentExit (the
	// caller did not acquire) — that is expected, so assert on the "added to
	// waiters queue" output rather than the exit code.
	if out, _ := bdRunAsActor(t, bd, dir, "bob", "merge-slot", "acquire", "--wait"); !strings.Contains(out, "added to waiters queue") {
		t.Fatalf("bob acquire --wait should queue him; got:\n%s", out)
	}
	if out, _ := bdRunAsActor(t, bd, dir, "carol", "merge-slot", "acquire", "--wait"); !strings.Contains(out, "added to waiters queue") {
		t.Fatalf("carol acquire --wait should queue her; got:\n%s", out)
	}
	// alice releases: holder pruned → slot Open, but bob+carol retained as waiters.
	if out, err := bdRunAsActor(t, bd, dir, "alice", "merge-slot", "release"); err != nil {
		t.Fatalf("alice release should succeed: %v\n%s", err, out)
	}

	// HUMAN check: the slot is now available AND has queued waiters.
	out, err := bdRunAsActor(t, bd, dir, "dave", "merge-slot", "check")
	if err != nil {
		t.Fatalf("merge-slot check should succeed: %v\n%s", err, out)
	}
	// Sanity: it IS reported available (the state that used to hide waiters).
	if !strings.Contains(out, "available") {
		t.Fatalf("slot should be reported available after release; got:\n%s", out)
	}
	// THE TEETH: the queued waiters bob + carol must be visible in the HUMAN
	// render on the available branch (not only via --json). Before the fix this
	// branch printed only the header and both names were absent.
	if !strings.Contains(out, "bob") || !strings.Contains(out, "carol") {
		t.Errorf("zobaf: available-with-queue human check must surface queued waiters bob+carol; got:\n%s", out)
	}
	if !strings.Contains(out, "Waiters: 2") {
		t.Errorf("zobaf: available human check should show the waiters count 'Waiters: 2'; got:\n%s", out)
	}

	// --json parity control: the JSON leg has always shown the waiters; confirm
	// it still does (guards against a fix that accidentally regressed it).
	jout, jerr := bdRunAsActor(t, bd, dir, "dave", "merge-slot", "check", "--json")
	if jerr != nil {
		t.Fatalf("merge-slot check --json should succeed: %v\n%s", jerr, jout)
	}
	if !strings.Contains(jout, "bob") || !strings.Contains(jout, "carol") {
		t.Errorf("--json check should include queued waiters; got:\n%s", jout)
	}
}
