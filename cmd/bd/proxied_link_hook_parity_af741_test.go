//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// beads-af741 (PROXIED on_update hook parity for `bd link`).
//
// `bd link` is the documented shorthand for `bd dep add`. The DIRECT path
// (cmd/bd/link.go) calls store.AddDependency, and the HookFiringStore decorator
// (internal/storage/hook_decorator.go AddDependency) fires on_update for the
// depending issue (dep.IssueID) via fireDependencyHookByID. The proxied `bd dep
// add` twin already fires it (beads-29tyj, dep_proxied_server.go), but the
// proxied `bd link` handler (cmd/bd/link_proxied_server.go runLinkProxiedServer)
// used proxiedAddDepEdges + uw.Commit WITHOUT firing — so a hub-connected
// (proxiedServerMode, store==nil) crew's on_update automation silently never ran
// for `bd link`. Same class as beads-29tyj / beads-k4yqq (a reimplementing
// proxied handler must re-add the hook fires the direct decorator gives for free).
//
// Driven END-TO-END through the ACTUAL proxied `bd` subprocess. MUTATION-VERIFIED:
// remove the fireProxiedUpdateSnapshots call from runLinkProxiedServer and this
// test goes RED.
func TestProxiedLinkHookParity_af741(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	dir := t.TempDir()
	markerPath := filepath.Join(dir, "on_update_marker")
	appendHookBody := "#!/bin/sh\necho \"$1\" >> " + markerPath + "\n"
	p := bdProxiedInitWithHooks(t, bd, "lhk", map[string]string{
		"on_update": appendHookBody,
	})

	from := bdProxiedCreate(t, bd, p.dir, "link from")
	to := bdProxiedCreate(t, bd, p.dir, "link to")

	_ = os.Remove(markerPath)
	if out, err := bdProxiedRun(t, bd, p.dir, "link", from.ID, to.ID); err != nil {
		t.Fatalf("proxied bd link failed: %v\n%s", err, out)
	}

	if got, ok := waitForMarkerContains(markerPath, from.ID, 5*time.Second); !ok {
		t.Errorf("REGRESSION (beads-af741): proxied `bd link` did NOT fire on_update for %s "+
			"(the direct path fires it via HookFiringStore.AddDependency(dep.IssueID)); marker=%q",
			from.ID, got)
	}
}
