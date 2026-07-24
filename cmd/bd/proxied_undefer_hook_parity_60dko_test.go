//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// beads-60dko (PROXIED on_update hook parity for `bd undefer`).
//
// The DIRECT `bd undefer` (cmd/bd/undefer.go) writes the deferred→open
// transition through the decorated store (store.UpdateIssue via
// HookFiringStore → on_update; hook_decorator.go). The PROXIED twin
// (undefer_proxied_server.go, undeferProxiedOne) writes via the RAW UOW
// use-case issueUC.ApplyUpdate DIRECTLY, which BYPASSES both HookFiringStore
// AND the shared applyUpdateProxiedOne firing helper that the proxied DEFER
// twin routes through (defer_proxied_server.go → applyUpdateProxiedOne →
// fireProxiedUpdateHooks). So a hub-connected (proxiedServerMode, store==nil)
// crew's on_update automation silently never ran on undefer — asymmetric with
// both a native crew and proxied defer.
//
// Driven END-TO-END through the ACTUAL proxied `bd` subprocess (a UOW-level
// helper would false-green by skipping the CLI hook plumbing — the family
// lesson, matching proxied_setstate_hook_parity_07nv2_test.go and
// proxied_close_verb_onclose_hook_5o5kp_test.go). MUTATION-VERIFIED: drop the
// fireProxiedUpdateHooks call added after uw.Commit in undeferProxiedOne and
// the on_update assertion goes RED (marker never written).
func TestProxiedUndeferFireHooks_60dko(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// hookBody appends the fired issue ID (arg $1, per hooks_unix.go
	// exec.CommandContext(ctx, hookPath, issue.ID, event)) to the marker.
	hookBody := func(markerPath string) string {
		return "#!/bin/sh\necho \"$1\" >> " + markerPath + "\n"
	}

	dir := t.TempDir()
	onUpdate := filepath.Join(dir, "on_update_marker")
	p := bdProxiedInitWithHooks(t, bd, "udh", map[string]string{
		"on_update": hookBody(onUpdate),
	})

	target := bdProxiedCreate(t, bd, p.dir, "undefer hook target", "--type", "task")

	// Defer it first (this itself fires on_update via the proxied defer twin).
	if out, err := bdProxiedRun(t, bd, p.dir, "defer", target.ID); err != nil {
		t.Fatalf("proxied bd defer (setup) failed: %v\n%s", err, out)
	}
	// Remove the defer-time marker so only the undefer firing is asserted.
	_ = os.Remove(onUpdate)

	if out, err := bdProxiedRun(t, bd, p.dir, "undefer", target.ID); err != nil {
		t.Fatalf("proxied bd undefer failed: %v\n%s", err, out)
	}

	// The direct path fires on_update for the target on the deferred→open
	// transition (store.UpdateIssue via HookFiringStore); the proxied twin must
	// too (fireProxiedUpdateHooks).
	if got, ok := waitForMarkerContains(onUpdate, target.ID, 5*time.Second); !ok {
		t.Errorf("REGRESSION (beads-60dko): proxied undefer did NOT fire on_update for target %s (the direct path fires it via HookFiringStore.UpdateIssue on deferred→open; the proxied defer twin fires via applyUpdateProxiedOne); on_update marker=%q", target.ID, got)
	}
}
