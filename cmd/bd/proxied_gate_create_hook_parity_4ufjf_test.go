//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// beads-4ufjf (PROXIED on_create hook parity for `bd gate create`).
//
// The DIRECT `bd gate create` (cmd/bd/gate.go) mints the gate issue and its
// blocking dependency through the decorated store via store.RunInTransaction →
// hookTrackingTransaction, which fires on_create for the minted gate + on_update
// for the blocked target post-commit (internal/storage/hook_decorator.go
// createHookEvents / dependencySnapshot). The PROXIED twin
// (gate_proxied_server.go, runGateCreateProxied) mints them through the RAW UOW
// use-case (issueUC.CreateIssue + depUC.AddDependency) + uw.Commit, which
// BYPASSES HookFiringStore — so a hub-connected (proxiedServerMode, store==nil)
// crew's on_create automation silently never ran when a gate was created, unlike
// a native crew. Member of the proxied-create-hook-parity family (w1vxy
// single-create / pma90 graph / 29tyj comment+dep / bmvfn quick+todo / 0l9wv
// swarm-create).
//
// Driven END-TO-END through the ACTUAL proxied `bd` subprocess (a UOW-level
// helper would false-green by skipping the CLI hook plumbing — the family
// lesson, matching proxied_swarm_create_hook_parity_0l9wv_test.go).
// MUTATION-VERIFIED: drop the fireProxiedCreateHooks call added after uw.Commit
// in runGateCreateProxied and the assertion goes RED (marker never written).
func TestProxiedGateCreateFireHooks_4ufjf(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	hookBody := func(markerPath string) string {
		return "#!/bin/sh\necho \"$1\" >> " + markerPath + "\n"
	}

	dir := t.TempDir()
	onCreate := filepath.Join(dir, "on_create_marker")
	p := bdProxiedInitWithHooks(t, bd, "gth", map[string]string{
		"on_create": hookBody(onCreate),
	})

	// A target issue to gate.
	target := bdProxiedCreate(t, bd, p.dir, "Gate target", "--type", "task")

	// Remove the create-time marker so only the gate-create firing is asserted
	// (creating the target above fires on_create for it).
	_ = os.Remove(onCreate)

	if out, err := bdProxiedRun(t, bd, p.dir, "gate", "create", "--blocks", target.ID, "--type", "human"); err != nil {
		t.Fatalf("proxied bd gate create failed: %v\n%s", err, out)
	}

	// The direct path fires on_create for the minted gate (tx.CreateIssue →
	// createHookEvents → EventCreate); the proxied twin must too
	// (fireProxiedCreateHooks). We removed the pre-create marker, so any on_create
	// recorded here is the minted gate.
	if got, ok := waitForMarkerNonEmpty(onCreate, 5*time.Second); !ok {
		t.Errorf("REGRESSION (beads-4ufjf): proxied gate create did NOT fire on_create for the minted gate (the direct path fires it via HookFiringStore/tx.CreateIssue → createHookEvents, gate.go); on_create marker=%q", got)
	}
}
