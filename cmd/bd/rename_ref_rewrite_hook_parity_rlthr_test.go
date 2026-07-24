//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// beads-rlthr (PROXIED on_update hook parity for `bd rename` ref-rewrites).
//
// A rename rewrites word-boundary text references to the old id across EVERY
// other issue's title/description/design/notes/acceptance_criteria. The DIRECT
// rename (cmd/bd/rename.go: store.RunInTransaction -> updateReferencesInAllIssuesTx
// -> tx.UpdateIssue) runs those per-issue rewrites inside a HookFiringStore
// hook-tracked transaction (internal/storage/hook_decorator.go
// hookTrackingTransaction.UpdateIssue -> pendingHook{EventUpdate}), which fires
// on_update per rewritten issue POST-COMMIT. The proxied handler
// (updateReferencesInAllIssuesProxied, cmd/bd/rename_proxied_server.go) writes
// each rewrite via uw.IssueUseCase().UpdateIssue + a single uw.Commit with NO
// fire — so a hub-connected (proxiedServerMode, store==nil) crew's on_update
// automation silently never ran for the issues whose bodies a rename rewrote.
// Same bespoke-UpdateIssue+Commit class as beads-fs73t (gate) / beads-elq6a
// (undefer).
//
// Note: the renamed row itself and comment-body rewrites are CLEAN-NEG (no
// on_update on either path). This asserts the OTHER issue's ref-rewrite fire.
//
// Driven END-TO-END through the ACTUAL proxied `bd` subprocess. MUTATION-VERIFIED:
// remove the post-commit fire loop from updateReferencesInAllIssuesProxied and
// this test goes RED.
func TestProxiedRenameRefRewriteHookParity_rlthr(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	dir := t.TempDir()
	markerPath := filepath.Join(dir, "on_update_marker")
	appendHookBody := "#!/bin/sh\necho \"$1\" >> " + markerPath + "\n"
	p := bdProxiedInitWithHooks(t, bd, "rrh", map[string]string{
		"on_update": appendHookBody,
	})

	// The target that will be renamed.
	bdProxiedCreate(t, bd, p.dir, "rename target", "--type", "task", "--id", "rrh-abc")
	// A DIFFERENT issue whose body textually references the target's old id — its
	// body gets ref-rewritten by the rename, so the DIRECT path fires on_update
	// for it.
	refs := bdProxiedCreate(t, bd, p.dir, "referrer", "--type", "task", "--id", "rrh-ref",
		"-d", "see rrh-abc for details")

	// Clear the marker so only the rename's ref-rewrite on_update fires are
	// observed (the creates above are create events, not on_update).
	_ = os.Remove(markerPath)

	if _, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "rename", "rrh-abc", "rrh-xyz"); err != nil {
		t.Fatalf("proxied bd rename failed: %v\n%s", err, stderr)
	}

	if got, ok := waitForMarkerContains(markerPath, refs.ID, 5*time.Second); !ok {
		t.Errorf("REGRESSION (beads-rlthr): proxied `bd rename` did NOT fire on_update for the "+
			"ref-rewritten issue %s (the direct path fires it via the hook-tracked transaction); marker=%q",
			refs.ID, got)
	}
}
