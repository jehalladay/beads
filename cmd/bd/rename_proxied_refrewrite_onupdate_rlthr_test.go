//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// beads-rlthr (PROXIED on_update hook parity for `bd rename`'s cross-issue
// reference rewrite).
//
// The DIRECT `bd rename` (cmd/bd/rename.go updateReferencesInAllIssuesTx) rewrites
// word-boundary id references across every OTHER issue's body fields via
// tx.UpdateIssue, which — through the HookFiringStore /
// hookTrackingTransaction.UpdateIssue decorator (internal/storage/hook_decorator.go:501)
// — fires on_update for each rewritten issue post-commit. The PROXIED twin
// (rename_proxied_server.go updateReferencesInAllIssuesProxied) routed those same
// per-issue field rewrites through the RAW issueUC.UpdateIssue, which BYPASSES
// HookFiringStore — so a hub-connected (proxiedServerMode, store==nil) crew's
// on_update automation silently never ran for issues a rename rewrote, unlike a
// native crew. Sibling of the proxied-hook-parity family (w1vxy create, 07nv2
// set-state, vv8cj wisp-snapshot).
//
// CLEAN-NEG (asserted implicitly by scope): the renamed row itself
// (UpdateIssueID) and comment-body rewrites (UpdateCommentText) are undecorated
// on BOTH paths, so they fire no on_update — the fix collects ONLY the
// field-rewritten referrer ids.
//
// Driven END-TO-END through the ACTUAL proxied `bd` subprocess (a UOW-level
// helper would false-green by skipping the CLI hook plumbing — the family
// lesson). MUTATION-VERIFIED: drop the fireProxiedUpdateSnapshots call added
// after uw.Commit in runRenameProxiedServer and the on_update assertion goes RED
// (the referrer's ID is never written to the marker).
func TestProxiedRenameRefRewriteFiresOnUpdate_rlthr(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	hookBody := func(markerPath string) string {
		return "#!/bin/sh\necho \"$1\" >> " + markerPath + "\n"
	}

	dir := t.TempDir()
	onUpdate := filepath.Join(dir, "on_update_marker")
	p := bdProxiedInitWithHooks(t, bd, "rlt", map[string]string{
		"on_update": hookBody(onUpdate),
	})

	// target is the issue that gets renamed; referrer's body references target by
	// id, so the rename must rewrite referrer.description → fire on_update on it.
	target := bdProxiedCreate(t, bd, p.dir, "rename target", "--type", "task")
	referrer := bdProxiedCreate(t, bd, p.dir, "referring issue", "--type", "task",
		"--description", "depends on "+target.ID+" for context")

	// Clear create-time markers so only the rename's rewrite firing is asserted.
	_ = os.Remove(onUpdate)

	newID := target.ID + "-renamed"
	if out, err := bdProxiedRun(t, bd, p.dir, "rename", target.ID, newID); err != nil {
		t.Fatalf("proxied bd rename %s %s failed: %v\n%s", target.ID, newID, err, out)
	}

	// The referrer's description held target.ID, which the rename rewrote to
	// newID; the direct path fires on_update on the referrer via HookFiringStore,
	// so the proxied twin must too (fireProxiedUpdateSnapshots).
	if got, ok := waitForMarkerContains(onUpdate, referrer.ID, 5*time.Second); !ok {
		t.Errorf("REGRESSION (beads-rlthr): proxied rename did NOT fire on_update for referrer %s whose body it rewrote (the direct path fires it via hookTrackingTransaction.UpdateIssue, hook_decorator.go:501); on_update marker=%q", referrer.ID, got)
	}
}
