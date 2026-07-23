//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// beads-29tyj (PROXIED on_update hook parity for comment / dependency / relate).
//
// The DIRECT store fires on_update after these mutations via the hook decorator
// (internal/storage/hook_decorator.go): AddIssueComment (L234), AddDependency /
// AddDependencyWithOptions / RemoveDependency (fireDependencyHookByID, L160/171/
// 193). close/update/reopen/label proxied twins already fire on_update (they
// route through fireProxiedUpdateHooks), proving the intended parity. BUT the
// comment / dependency / relate proxied handlers committed via the UOW use-case
// layer, which does NOT fire hooks — so a hub-connected (proxied, store==nil)
// crew's on_update automation silently never ran for `bd comment`, `bd dep
// add/blocks/remove`, `bd dep relate/unrelate`.
//
// Driven END-TO-END through the ACTUAL proxied `bd` subprocess (a UOW-level
// helper would false-green by skipping the CLI hook plumbing — the batch-parity
// family lesson). MUTATION-VERIFIED: remove the fireProxiedUpdateSnapshots call
// added to the handler and the corresponding sub-test goes RED.

// waitForMarkerContains polls a marker file until it exists and contains want,
// or the timeout elapses. Returns the file contents (for diagnostics).
func waitForMarkerContains(path, want string, timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	for {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), want) {
			return string(data), true
		}
		if time.Now().After(deadline) {
			return string(data), false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestProxiedHookParity_29tyj(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// appendHookBody writes the fired issue ID (arg $1) to the marker, APPENDING so
	// a multi-endpoint verb (relate) records every invocation.
	appendHookBody := func(markerPath string) string {
		return "#!/bin/sh\necho \"$1\" >> " + markerPath + "\n"
	}

	// bd comment fires on_update for the commented issue.
	t.Run("comment_add_fires_on_update", func(t *testing.T) {
		dir := t.TempDir()
		markerPath := filepath.Join(dir, "on_update_marker")
		p := bdProxiedInitWithHooks(t, bd, "chc", map[string]string{
			"on_update": appendHookBody(markerPath),
		})
		issue := bdProxiedCreate(t, bd, p.dir, "comment hook test")

		_ = os.Remove(markerPath)
		if out, err := bdProxiedRun(t, bd, p.dir, "comment", issue.ID, "a note"); err != nil {
			t.Fatalf("proxied bd comment failed: %v\n%s", err, out)
		}

		if got, ok := waitForMarkerContains(markerPath, issue.ID, 5*time.Second); !ok {
			t.Errorf("REGRESSION (beads-29tyj): proxied `bd comment` did NOT fire on_update for %s (the direct path fires it via HookFiringStore.AddIssueComment); marker=%q", issue.ID, got)
		}
	})

	// bd dep add fires on_update for the depending issue (fromID = dep.IssueID).
	t.Run("dep_add_fires_on_update", func(t *testing.T) {
		dir := t.TempDir()
		markerPath := filepath.Join(dir, "on_update_marker")
		p := bdProxiedInitWithHooks(t, bd, "dhc", map[string]string{
			"on_update": appendHookBody(markerPath),
		})
		from := bdProxiedCreate(t, bd, p.dir, "dep from")
		to := bdProxiedCreate(t, bd, p.dir, "dep to")

		_ = os.Remove(markerPath)
		if out, err := bdProxiedRun(t, bd, p.dir, "dep", "add", from.ID, to.ID); err != nil {
			t.Fatalf("proxied bd dep add failed: %v\n%s", err, out)
		}

		if got, ok := waitForMarkerContains(markerPath, from.ID, 5*time.Second); !ok {
			t.Errorf("REGRESSION (beads-29tyj): proxied `bd dep add` did NOT fire on_update for %s (the direct path fires it via fireDependencyHookByID(dep.IssueID)); marker=%q", from.ID, got)
		}
	})

	// bd dep relate fires on_update for BOTH endpoints (the direct path adds two
	// edges, each firing on_update for its IssueID).
	t.Run("relate_fires_on_update_both_endpoints", func(t *testing.T) {
		dir := t.TempDir()
		markerPath := filepath.Join(dir, "on_update_marker")
		p := bdProxiedInitWithHooks(t, bd, "rhc", map[string]string{
			"on_update": appendHookBody(markerPath),
		})
		a := bdProxiedCreate(t, bd, p.dir, "relate A")
		b := bdProxiedCreate(t, bd, p.dir, "relate B")

		_ = os.Remove(markerPath)
		if out, err := bdProxiedRun(t, bd, p.dir, "dep", "relate", a.ID, b.ID); err != nil {
			t.Fatalf("proxied bd dep relate failed: %v\n%s", err, out)
		}

		if got, ok := waitForMarkerContains(markerPath, a.ID, 5*time.Second); !ok {
			t.Errorf("REGRESSION (beads-29tyj): proxied `bd dep relate` did NOT fire on_update for endpoint %s; marker=%q", a.ID, got)
		}
		if got, ok := waitForMarkerContains(markerPath, b.ID, 5*time.Second); !ok {
			t.Errorf("REGRESSION (beads-29tyj): proxied `bd dep relate` did NOT fire on_update for endpoint %s; marker=%q", b.ID, got)
		}
	})
}
