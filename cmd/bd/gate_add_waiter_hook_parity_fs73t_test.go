//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// beads-fs73t (PROXIED on_update hook parity for `bd gate add-waiter`).
//
// The DIRECT `bd gate add-waiter` (cmd/bd/gate.go) writes the gate's new waiters
// slice via store.UpdateIssue, and the HookFiringStore decorator
// (internal/storage/hook_decorator.go UpdateIssue) fires on_update. The proxied
// handler (cmd/bd/gate_proxied_server.go, the add-waiter RunInTxMsg leg) calls
// uw.IssueUseCase().UpdateIssue + Commit DIRECTLY with no fire — the gate
// beads-5o5kp hook fix only covered the CLOSE paths (closeGateProxied /
// resolveGate), leaving the UpdateIssue paths (add-waiter, await_id discovery)
// uncovered. So a hub-connected (proxiedServerMode, store==nil) crew's on_update
// automation silently never ran for `bd gate add-waiter`. Same class as
// beads-elq6a (bespoke UpdateIssue+Commit proxied path skips the fire) /
// beads-29tyj / beads-af741.
//
// Driven END-TO-END through the ACTUAL proxied `bd` subprocess. MUTATION-VERIFIED:
// remove the post-commit on_update fire from the add-waiter proxied leg and this
// test goes RED.
func TestProxiedGateAddWaiterHookParity_fs73t(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	dir := t.TempDir()
	markerPath := filepath.Join(dir, "on_update_marker")
	appendHookBody := "#!/bin/sh\necho \"$1\" >> " + markerPath + "\n"
	p := bdProxiedInitWithHooks(t, bd, "gwh", map[string]string{
		"on_update": appendHookBody,
	})

	target := bdProxiedCreate(t, bd, p.dir, "gate add-waiter target", "--type", "task")

	createOut, createErr, err := bdProxiedRunBuffers(t, bd, p.dir,
		"gate", "create", "--blocks", target.ID, "--type", "human", "--json")
	if err != nil {
		t.Fatalf("gate create --json failed: %v\n%s\n%s", err, createOut, createErr)
	}
	gate := parseIssueJSON(t, []byte(createOut))
	if gate.ID == "" {
		t.Fatalf("no gate ID parsed:\n%s", createOut)
	}

	// Clear the marker so only the add-waiter's on_update fire is observed
	// (gate create itself is a create event, not on_update).
	_ = os.Remove(markerPath)

	if out, err := bdProxiedRun(t, bd, p.dir, "gate", "add-waiter", gate.ID, "my-rig/workers/agent-0"); err != nil {
		t.Fatalf("proxied bd gate add-waiter failed: %v\n%s", err, out)
	}

	if got, ok := waitForMarkerContains(markerPath, gate.ID, 5*time.Second); !ok {
		t.Errorf("REGRESSION (beads-fs73t): proxied `bd gate add-waiter` did NOT fire on_update for gate %s "+
			"(the direct path fires it via HookFiringStore.UpdateIssue); marker=%q",
			gate.ID, got)
	}
}
