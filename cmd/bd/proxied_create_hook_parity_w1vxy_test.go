//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// beads-w1vxy (PROXIED on_create hook parity for single `bd create`).
//
// The DIRECT single create fires on_create after the commit via the hook
// decorator (internal/storage/hook_decorator.go): create.go routes
// tx.CreateIssue through HookFiringStore → hookTrackingTransaction, which
// records createHookEvents (on_create on a label-free snapshot, then a
// synthetic on_update per cumulative label) and fires them post-commit — for
// wisp + non-wisp alike (createHookEvents does not skip Ephemeral). BUT the
// proxied single-create handler (create_proxied_server.go runCreateProxiedSingle)
// committed via the UOW use-case layer, which does NOT fire hooks — so a
// hub-connected (proxied, store==nil) crew's on_create automation silently never
// ran for `bd create`. This is the create-leg sibling of beads-29tyj (comment /
// dep / relate on_update parity).
//
// Driven END-TO-END through the ACTUAL proxied `bd` subprocess (a UOW-level
// helper would false-green by skipping the CLI hook plumbing — the batch-parity
// family lesson). MUTATION-VERIFIED: remove the fireProxiedCreateHooks call
// added to runCreateProxiedSingle and these sub-tests go RED.
//
// NOT covered here (deliberate parity clean-negatives): markdown create (direct
// bulk CreateIssuesWithFullOptions intentionally fires no per-issue on_create,
// markdown.go) and graph create (separate sibling — direct fires but also
// carries dependency-hook parity).
func TestProxiedCreateHookParity_w1vxy(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	appendHookBody := func(markerPath string) string {
		return "#!/bin/sh\necho \"$1\" >> " + markerPath + "\n"
	}

	// A label-free `bd create` fires on_create for the new issue.
	t.Run("create_fires_on_create", func(t *testing.T) {
		dir := t.TempDir()
		createMarker := filepath.Join(dir, "on_create_marker")
		p := bdProxiedInitWithHooks(t, bd, "chp", map[string]string{
			"on_create": appendHookBody(createMarker),
		})

		_ = os.Remove(createMarker)
		issue := bdProxiedCreate(t, bd, p.dir, "create hook test")

		if got, ok := waitForMarkerContains(createMarker, issue.ID, 5*time.Second); !ok {
			t.Errorf("REGRESSION (beads-w1vxy): proxied `bd create` did NOT fire on_create for %s (the direct path fires it via HookFiringStore.CreateIssue → createHookEvents); marker=%q", issue.ID, got)
		}
	})

	// A wisp (ephemeral) create fires on_create too — the direct path routes
	// wisps through the same tx.CreateIssue and createHookEvents does not skip
	// Ephemeral.
	t.Run("wisp_create_fires_on_create", func(t *testing.T) {
		dir := t.TempDir()
		createMarker := filepath.Join(dir, "on_create_marker")
		p := bdProxiedInitWithHooks(t, bd, "whp", map[string]string{
			"on_create": appendHookBody(createMarker),
		})

		_ = os.Remove(createMarker)
		issue := bdProxiedCreate(t, bd, p.dir, "wisp hook test", "--ephemeral")

		if got, ok := waitForMarkerContains(createMarker, issue.ID, 5*time.Second); !ok {
			t.Errorf("REGRESSION (beads-w1vxy): proxied `bd create --ephemeral` did NOT fire on_create for wisp %s; marker=%q", issue.ID, got)
		}
	})

	// A labeled `bd create` fires on_create (label-free snapshot) AND the
	// synthetic on_update label stream, mirroring createHookEvents.
	t.Run("labeled_create_fires_on_create_and_label_on_update", func(t *testing.T) {
		dir := t.TempDir()
		createMarker := filepath.Join(dir, "on_create_marker")
		updateMarker := filepath.Join(dir, "on_update_marker")
		p := bdProxiedInitWithHooks(t, bd, "lhp", map[string]string{
			"on_create": appendHookBody(createMarker),
			"on_update": appendHookBody(updateMarker),
		})

		_ = os.Remove(createMarker)
		_ = os.Remove(updateMarker)
		issue := bdProxiedCreate(t, bd, p.dir, "labeled hook test", "--label", "alpha")

		if got, ok := waitForMarkerContains(createMarker, issue.ID, 5*time.Second); !ok {
			t.Errorf("REGRESSION (beads-w1vxy): proxied labeled `bd create` did NOT fire on_create for %s; marker=%q", issue.ID, got)
		}
		if got, ok := waitForMarkerContains(updateMarker, issue.ID, 5*time.Second); !ok {
			t.Errorf("REGRESSION (beads-w1vxy): proxied labeled `bd create` did NOT fire the synthetic on_update label stream for %s (createHookEvents parity); marker=%q", issue.ID, got)
		}
	})
}
