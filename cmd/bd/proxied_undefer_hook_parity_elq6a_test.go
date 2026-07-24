//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// beads-elq6a (PROXIED on_update hook parity for `bd undefer`).
//
// The DIRECT undefer (cmd/bd/undefer.go) writes the deferred->open transition
// via store.UpdateIssue, and the HookFiringStore decorator
// (internal/storage/hook_decorator.go UpdateIssue) fires on_update. The proxied
// defer TWIN (cmd/bd/defer_proxied_server.go) routes through
// applyUpdateProxiedOne, which fires on_update via fireProxiedUpdateHooks. But
// the proxied undefer handler (cmd/bd/undefer_proxied_server.go
// undeferProxiedOne) calls issueUC.ApplyUpdate + uw.Commit DIRECTLY with no
// fire — so a hub-connected (proxiedServerMode, store==nil) crew's on_update
// automation silently never ran for `bd undefer`. Same class as beads-29tyj /
// beads-k4yqq / beads-af741 (a reimplementing proxied handler must re-add the
// hook fires the direct decorator gives for free).
//
// Driven END-TO-END through the ACTUAL proxied `bd` subprocess. MUTATION-VERIFIED:
// remove the fireProxiedUpdateSnapshots call from undeferProxiedOne and this
// test goes RED.
func TestProxiedUndeferHookParity_elq6a(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	dir := t.TempDir()
	markerPath := filepath.Join(dir, "on_update_marker")
	appendHookBody := "#!/bin/sh\necho \"$1\" >> " + markerPath + "\n"
	p := bdProxiedInitWithHooks(t, bd, "udh", map[string]string{
		"on_update": appendHookBody,
	})

	issue := bdProxiedCreate(t, bd, p.dir, "undefer target")

	// Defer it first (this fires on_update via the defer proxied twin — expected).
	if out, err := bdProxiedRun(t, bd, p.dir, "defer", issue.ID); err != nil {
		t.Fatalf("proxied bd defer failed: %v\n%s", err, out)
	}

	// Clear the marker so only the undefer's on_update fire is observed.
	_ = os.Remove(markerPath)

	if out, err := bdProxiedRun(t, bd, p.dir, "undefer", issue.ID); err != nil {
		t.Fatalf("proxied bd undefer failed: %v\n%s", err, out)
	}

	if got, ok := waitForMarkerContains(markerPath, issue.ID, 5*time.Second); !ok {
		t.Errorf("REGRESSION (beads-elq6a): proxied `bd undefer` did NOT fire on_update for %s "+
			"(the direct path fires it via HookFiringStore.UpdateIssue, and proxied `bd defer` "+
			"fires it via applyUpdateProxiedOne); marker=%q",
			issue.ID, got)
	}
}
