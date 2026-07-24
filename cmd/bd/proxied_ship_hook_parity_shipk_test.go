//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// beads-shipk (PROXIED on_update hook parity for `bd ship`).
//
// The DIRECT `bd ship` (cmd/bd/ship.go) adds the provides:<cap> label via
// store.AddLabel, and the hook decorator (internal/storage/hook_decorator.go
// HookFiringStore.AddLabel, L199-201) fires on_update for the labeled issue.
// The PROXIED handler (cmd/bd/ship_proxied_server.go runShipProxiedServer),
// used by hub-connected (proxiedServerMode, store==nil) crew, adds the same
// label through the UOW use-case layer (labelUC.AddLabel) + uw.Commit, which
// does NOT fire hooks — so a hub-connected crew's on_update automation silently
// never ran for `bd ship`. Same class as beads-29tyj / beads-bmvfn (a
// reimplementing proxied handler must re-add the hook fires the direct decorator
// gives the direct path for free).
//
// Driven END-TO-END through the ACTUAL proxied `bd` subprocess (a UOW-level
// helper would false-green by skipping the CLI hook plumbing). MUTATION-VERIFIED:
// remove the fireProxiedUpdateSnapshots call from runShipProxiedServer and this
// test goes RED.
func TestProxiedShipHookParity_shipk(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	dir := t.TempDir()
	markerPath := filepath.Join(dir, "on_update_marker")
	appendHookBody := "#!/bin/sh\necho \"$1\" >> " + markerPath + "\n"
	p := bdProxiedInitWithHooks(t, bd, "shk", map[string]string{
		"on_update": appendHookBody,
	})

	// ship reads the export:<cap> labeled issue then adds provides:<cap>. The
	// issue must be CLOSED to satisfy the ship precondition.
	issue := bdProxiedCreate(t, bd, p.dir, "ship hook test")
	if out, err := bdProxiedRun(t, bd, p.dir, "label", "add", issue.ID, "export:mycap"); err != nil {
		t.Fatalf("proxied bd label add failed: %v\n%s", err, out)
	}
	if out, err := bdProxiedRun(t, bd, p.dir, "close", issue.ID); err != nil {
		t.Fatalf("proxied bd close failed: %v\n%s", err, out)
	}

	// The label add + close above also fire on_update — clear the marker so we
	// observe ONLY the ship-triggered fire.
	_ = os.Remove(markerPath)

	if out, err := bdProxiedRun(t, bd, p.dir, "ship", "mycap"); err != nil {
		t.Fatalf("proxied bd ship failed: %v\n%s", err, out)
	}

	if got, ok := waitForMarkerContains(markerPath, issue.ID, 5*time.Second); !ok {
		t.Errorf("REGRESSION (beads-shipk): proxied `bd ship` did NOT fire on_update for %s "+
			"(the direct path fires it via HookFiringStore.AddLabel when adding provides:<cap>); marker=%q",
			issue.ID, got)
	}
}
