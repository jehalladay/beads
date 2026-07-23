//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// beads-5o5kp (PROXIED on_close / on_update hook parity for the close-VERB family).
//
// The DIRECT path fires on_close (+on_update) after these close verbs via the
// hook decorator (internal/storage/hook_decorator.go:145, CloseIssue →
// EventClose): `bd todo done`, `bd epic close-eligible`, `bd gate resolve`
// (manual + the gate-check auto-resolve leg), and `bd human respond|dismiss`
// all close via the decorated store. Their PROXIED twins close through the UOW
// use-case layer (uw.IssueUseCase().CloseIssue/CloseWisp), which does NOT wrap
// the hook decorator — so a hub-connected (proxied, store==nil) crew's on_close
// automation silently never ran for these verbs, even though the already-fixed
// close/duplicate/supersede proxied handlers DO fire (via fireProxiedUpdateHooks).
//
// Driven END-TO-END through the ACTUAL proxied `bd` subprocess (a UOW-level
// helper would false-green by skipping the CLI hook plumbing — the batch-parity
// family lesson, matching proxied_hook_parity_29tyj_test.go). MUTATION-VERIFIED:
// drop the fireProxiedUpdateHooks call added to each handler and the
// corresponding sub-test goes RED (marker never written).

func TestProxiedCloseVerbFireHooks_5o5kp(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// hookBody writes the fired issue ID (arg $1, per hooks_unix.go
	// exec.CommandContext(ctx, hookPath, issue.ID, event)) to the marker,
	// APPENDING so a multi-id verb records every invocation.
	hookBody := func(markerPath string) string {
		return "#!/bin/sh\necho \"$1\" >> " + markerPath + "\n"
	}

	// assertClosedAndUpdatedFired checks BOTH markers contain wantID after the
	// close verb ran: on_close (the open→closed transition) and on_update (fired
	// unconditionally by fireProxiedUpdateHooks). Distinct markers so a verb that
	// only fired one is caught.
	assertClosedAndUpdatedFired := func(t *testing.T, onClose, onUpdate, wantID string) {
		t.Helper()
		if got, ok := waitForMarkerContains(onClose, wantID, 5*time.Second); !ok {
			t.Errorf("REGRESSION (beads-5o5kp): proxied close verb did NOT fire on_close for %s (the direct path fires it via HookFiringStore.CloseIssue, hook_decorator.go:145); on_close marker=%q", wantID, got)
		}
		if got, ok := waitForMarkerContains(onUpdate, wantID, 5*time.Second); !ok {
			t.Errorf("REGRESSION (beads-5o5kp): proxied close verb did NOT fire on_update for %s (fireProxiedUpdateHooks fires on_update unconditionally, at parity with the close/duplicate proxied handlers); on_update marker=%q", wantID, got)
		}
	}

	// initHooks creates a proxied project with on_close + on_update marker hooks
	// under distinct paths, returning the two marker paths.
	initHooks := func(t *testing.T, prefix string) (proxiedProject, string, string) {
		t.Helper()
		dir := t.TempDir()
		onClose := filepath.Join(dir, "on_close_marker")
		onUpdate := filepath.Join(dir, "on_update_marker")
		p := bdProxiedInitWithHooks(t, bd, prefix, map[string]string{
			"on_close":  hookBody(onClose),
			"on_update": hookBody(onUpdate),
		})
		_ = os.Remove(onClose)
		_ = os.Remove(onUpdate)
		return p, onClose, onUpdate
	}

	// `bd todo done` closes a TODO (task) — the direct path fires on_close via
	// the decorated store; the proxied twin must too.
	t.Run("todo_done_fires_on_close", func(t *testing.T) {
		p, onClose, onUpdate := initHooks(t, "tdc")
		issue := bdProxiedCreate(t, bd, p.dir, "todo done hook test", "--type", "task")

		if out, err := bdProxiedRun(t, bd, p.dir, "todo", "done", issue.ID); err != nil {
			t.Fatalf("proxied bd todo done failed: %v\n%s", err, out)
		}
		assertClosedAndUpdatedFired(t, onClose, onUpdate, issue.ID)
	})

	// `bd epic close-eligible` closes an epic whose children are all closed.
	t.Run("epic_close_eligible_fires_on_close", func(t *testing.T) {
		p, onClose, onUpdate := initHooks(t, "eph")
		epic := bdProxiedCreate(t, bd, p.dir, "Eligible epic", "--type", "epic")
		child := bdProxiedCreate(t, bd, p.dir, "Only child", "--type", "task", "--parent", epic.ID)

		// Close the sole child so the epic becomes eligible. Remove the markers
		// AFTER this close so only the close-eligible close is asserted (the child
		// close would itself fire on_close for the child).
		if out, err := bdProxiedRun(t, bd, p.dir, "close", child.ID); err != nil {
			t.Fatalf("closing child %s failed: %v\n%s", child.ID, err, out)
		}
		_ = os.Remove(onClose)
		_ = os.Remove(onUpdate)

		if out, err := bdProxiedRun(t, bd, p.dir, "epic", "close-eligible"); err != nil {
			t.Fatalf("proxied bd epic close-eligible failed: %v\n%s", err, out)
		}
		assertClosedAndUpdatedFired(t, onClose, onUpdate, epic.ID)
	})

	// `bd gate resolve` closes a gate issue (manual-resolve leg).
	t.Run("gate_resolve_fires_on_close", func(t *testing.T) {
		p, onClose, onUpdate := initHooks(t, "grh")
		target := bdProxiedCreate(t, bd, p.dir, "gate target")

		// Create a human gate blocking the target; `bd gate create --json` emits
		// the gate issue JSON.
		gateOut, err := bdProxiedRun(t, bd, p.dir, "gate", "create", "--json", "--blocks", target.ID, "--type", "human")
		if err != nil {
			t.Fatalf("proxied bd gate create failed: %v\n%s", err, gateOut)
		}
		gateIssue := parseIssueJSON(t, gateOut)

		_ = os.Remove(onClose)
		_ = os.Remove(onUpdate)

		if out, err := bdProxiedRun(t, bd, p.dir, "gate", "resolve", gateIssue.ID); err != nil {
			t.Fatalf("proxied bd gate resolve failed: %v\n%s", err, out)
		}
		assertClosedAndUpdatedFired(t, onClose, onUpdate, gateIssue.ID)
	})

	// `bd human respond` adds a comment and closes a human-needed bead.
	t.Run("human_respond_fires_on_close", func(t *testing.T) {
		p, onClose, onUpdate := initHooks(t, "hrh")
		issue := bdProxiedCreate(t, bd, p.dir, "needs a human", "--label", "human")

		if out, err := bdProxiedRun(t, bd, p.dir, "human", "respond", issue.ID, "--response", "approved"); err != nil {
			t.Fatalf("proxied bd human respond failed: %v\n%s", err, out)
		}
		assertClosedAndUpdatedFired(t, onClose, onUpdate, issue.ID)
	})

	// `bd human dismiss` closes a human-needed bead without responding.
	t.Run("human_dismiss_fires_on_close", func(t *testing.T) {
		p, onClose, onUpdate := initHooks(t, "hdh")
		issue := bdProxiedCreate(t, bd, p.dir, "dismiss me", "--label", "human")

		if out, err := bdProxiedRun(t, bd, p.dir, "human", "dismiss", issue.ID, "--reason", "not applicable"); err != nil {
			t.Fatalf("proxied bd human dismiss failed: %v\n%s", err, out)
		}
		assertClosedAndUpdatedFired(t, onClose, onUpdate, issue.ID)
	})
}
