//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// beads-0l9wv (PROXIED on_create hook parity for `bd swarm create`).
//
// The DIRECT `bd swarm create` (cmd/bd/swarm.go) mints the swarm molecule (and,
// when auto-wrapping a single issue, the wrapper epic) through the decorated
// store via store.RunInTransaction → hookTrackingTransaction.CreateIssue, which
// fires on_create per minted issue post-commit (internal/storage/hook_decorator.go
// createHookEvents). The PROXIED twin (swarm_proxied_server.go,
// runSwarmCreateProxied) mints them through the RAW UOW use-case
// (issueUC.CreateIssue) + uw.Commit, which BYPASSES HookFiringStore — so a
// hub-connected (proxiedServerMode, store==nil) crew's on_create automation
// silently never ran when a swarm was created, unlike a native crew. Member of
// the proxied-create-hook-parity family (w1vxy single-create / pma90 graph /
// 29tyj comment+dep / bmvfn quick+todo); the mtvlf atomicity twin already
// covers this seam's UOW batching, this is its hook-firing leg.
//
// Driven END-TO-END through the ACTUAL proxied `bd` subprocess (a UOW-level
// helper would false-green by skipping the CLI hook plumbing — the family
// lesson, matching proxied_setstate_hook_parity_07nv2_test.go). MUTATION-VERIFIED:
// drop the fireProxiedCreateHooks call added after uw.Commit in
// runSwarmCreateProxied and the assertion goes RED (marker never written).
func TestProxiedSwarmCreateFireHooks_0l9wv(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// hookBody writes the fired issue ID (arg $1) to the marker, APPENDING so
	// every invocation is recorded.
	hookBody := func(markerPath string) string {
		return "#!/bin/sh\necho \"$1\" >> " + markerPath + "\n"
	}

	dir := t.TempDir()
	onCreate := filepath.Join(dir, "on_create_marker")
	p := bdProxiedInitWithHooks(t, bd, "swh", map[string]string{
		"on_create": hookBody(onCreate),
	})

	// Build a swarmable epic (2 independent unblocked children).
	epic := bdProxiedCreate(t, bd, p.dir, "Swarm hook epic", "--type", "epic")
	bdProxiedCreate(t, bd, p.dir, "Task 1", "--type", "task", "--parent", epic.ID)
	bdProxiedCreate(t, bd, p.dir, "Task 2", "--type", "task", "--parent", epic.ID)

	// Remove the create-time marker so only the swarm-molecule create firing is
	// asserted (creating the epic + children above fires on_create for each).
	_ = os.Remove(onCreate)

	if out, err := bdProxiedRun(t, bd, p.dir, "swarm", "create", epic.ID); err != nil {
		t.Fatalf("proxied bd swarm create failed: %v\n%s", err, out)
	}

	// The direct path fires on_create for the minted swarm molecule
	// (tx.CreateIssue → createHookEvents → EventCreate); the proxied twin must
	// too (fireProxiedCreateHooks). We removed the pre-create marker, so any
	// on_create recorded here is the swarm molecule (an epic is already terminal,
	// so no wrapper epic is minted).
	if got, ok := waitForMarkerNonEmpty(onCreate, 5*time.Second); !ok {
		t.Errorf("REGRESSION (beads-0l9wv): proxied swarm create did NOT fire on_create for the minted swarm molecule (the direct path fires it via HookFiringStore/tx.CreateIssue → createHookEvents, swarm.go); on_create marker=%q", got)
	}
}
