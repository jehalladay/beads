//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// beads-07nv2 (PROXIED on_update / on_create hook parity for `bd set-state`).
//
// The DIRECT `bd set-state` (cmd/bd/state.go) runs through the decorated store:
// tx.CreateIssue(event) → on_create for the minted event bead, and the label
// swap tx.RemoveLabel/tx.AddLabel(fullID) → on_update for the TARGET issue
// (internal/storage/hook_decorator.go createHookEvents + AddLabel/RemoveLabel →
// EventCreate/EventUpdate). The PROXIED twin (state_proxied_server.go,
// runSetStateProxiedServer) mutates through the RAW UOW use-cases
// (issueUC.CreateIssue / labelUC.AddLabel|RemoveLabel), which BYPASS
// HookFiringStore — so a hub-connected (proxiedServerMode, store==nil) crew's
// on_update/on_create automation silently never ran for set-state, unlike a
// native crew. This is the last mutation-verb gap in the proxied-vs-direct
// hook-firing parity family (5o5kp explicitly scoped set-state OUT).
//
// Driven END-TO-END through the ACTUAL proxied `bd` subprocess (a UOW-level
// helper would false-green by skipping the CLI hook plumbing — the family
// lesson, matching proxied_close_verb_onclose_hook_5o5kp_test.go and
// proxied_hook_parity_29tyj_test.go). MUTATION-VERIFIED: drop the
// fireProxiedUpdateHooks / fireProxiedCreateHooks calls added after uw.Commit in
// runSetStateProxiedServer and the corresponding assertion goes RED (marker
// never written).
func TestProxiedSetStateFireHooks_07nv2(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// hookBody writes the fired issue ID (arg $1, per hooks_unix.go
	// exec.CommandContext(ctx, hookPath, issue.ID, event)) to the marker,
	// APPENDING so every invocation is recorded.
	hookBody := func(markerPath string) string {
		return "#!/bin/sh\necho \"$1\" >> " + markerPath + "\n"
	}

	dir := t.TempDir()
	onUpdate := filepath.Join(dir, "on_update_marker")
	onCreate := filepath.Join(dir, "on_create_marker")
	p := bdProxiedInitWithHooks(t, bd, "ssh", map[string]string{
		"on_update": hookBody(onUpdate),
		"on_create": hookBody(onCreate),
	})

	target := bdProxiedCreate(t, bd, p.dir, "set-state hook target", "--type", "task")

	// Remove the create-time markers so only the set-state firing is asserted
	// (creating the target itself fires on_create/on_update for the target).
	_ = os.Remove(onUpdate)
	_ = os.Remove(onCreate)

	if out, err := bdProxiedRun(t, bd, p.dir, "set-state", target.ID, "review=approved"); err != nil {
		t.Fatalf("proxied bd set-state failed: %v\n%s", err, out)
	}

	// The direct path fires on_update on the TARGET (fullID) via the label-swap
	// decorator; the proxied twin must too (fireProxiedUpdateHooks).
	if got, ok := waitForMarkerContains(onUpdate, target.ID, 5*time.Second); !ok {
		t.Errorf("REGRESSION (beads-07nv2): proxied set-state did NOT fire on_update for target %s (the direct path fires it via HookFiringStore.AddLabel/RemoveLabel, hook_decorator.go); on_update marker=%q", target.ID, got)
	}

	// The direct path also fires on_create for the minted event bead
	// (tx.CreateIssue → createHookEvents → EventCreate); the proxied twin must too
	// (fireProxiedCreateHooks). The event bead is target.ID's child, so its ID is
	// prefixed by the target ID — assert the marker recorded an event create beyond
	// the target's own (we removed the pre-set-state markers, so any on_create here
	// is the event bead).
	if got, ok := waitForMarkerNonEmpty(onCreate, 5*time.Second); !ok {
		t.Errorf("REGRESSION (beads-07nv2): proxied set-state did NOT fire on_create for the minted event bead (the direct path fires it via HookFiringStore/tx.CreateIssue → createHookEvents); on_create marker=%q", got)
	}
}

// waitForMarkerNonEmpty polls until the marker file exists with any non-empty
// content, mirroring waitForMarkerContains but without a specific substring (the
// minted event bead's child ID is not known to the test up front).
func waitForMarkerNonEmpty(path string, timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	for {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return string(data), true
		}
		if time.Now().After(deadline) {
			data, _ := os.ReadFile(path)
			return string(data), false
		}
		time.Sleep(50 * time.Millisecond)
	}
}
