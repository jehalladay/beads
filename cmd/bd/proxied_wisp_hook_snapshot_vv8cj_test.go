//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// beads-vv8cj (PROXIED on_update hook parity for WISP targets).
//
// The direct store's post-mutation hook re-fetch is WISP-AWARE: fireHookByID /
// fireDependencyHookByID (internal/storage/hook_decorator.go) re-fetch via
// DoltStore.GetIssue → issueops.GetIssueInTx, which falls back to the wisps
// table on an issues-table miss (GetIssueInTxSplit). So a `bd comment` / `bd dep
// add` / `bd dep relate` on a WISP (ephemeral) target fires on_update directly.
//
// The proxied twins (beads-29tyj) capture the post-mutation snapshot via
// captureProxiedHookSnapshot, which used the use-case GetIssue — issues table
// ONLY (IssueTableOpts{UseWispsTable:false}). A wisp target captured nil → the
// on_update hook silently never fired for hub-connected (proxied, store==nil)
// crew, while the direct path fired it. captureProxiedHookSnapshot now falls
// back to GetWisp on an issues-table miss to mirror the direct re-fetch.
//
// Driven END-TO-END through the ACTUAL proxied `bd` subprocess (a UOW-level
// helper would false-green by skipping the CLI hook plumbing). MUTATION-VERIFIED:
// remove the GetWisp fallback in captureProxiedHookSnapshot and these go RED.

func TestProxiedWispHookSnapshot_vv8cj(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	appendHookBody := func(markerPath string) string {
		return "#!/bin/sh\necho \"$1\" >> " + markerPath + "\n"
	}

	// bd comment on a WISP fires on_update (parity with the wisp-aware direct path).
	// This isolates the shared captureProxiedHookSnapshot helper's wisp fallback:
	// the resolve step (proxiedResolveIssueOrWisp) already finds the wisp, only the
	// post-mutation re-capture used issues-only GetIssue before the fix.
	t.Run("comment_on_wisp_fires_on_update", func(t *testing.T) {
		dir := t.TempDir()
		markerPath := filepath.Join(dir, "on_update_marker")
		p := bdProxiedInitWithHooks(t, bd, "cwv", map[string]string{
			"on_update": appendHookBody(markerPath),
		})
		wisp := bdProxiedCreate(t, bd, p.dir, "wisp comment target", "--ephemeral")

		_ = os.Remove(markerPath)
		if out, err := bdProxiedRun(t, bd, p.dir, "comment", wisp.ID, "a note"); err != nil {
			t.Fatalf("proxied bd comment on wisp failed: %v\n%s", err, out)
		}

		if got, ok := waitForMarkerContains(markerPath, wisp.ID, 5*time.Second); !ok {
			t.Errorf("REGRESSION (beads-vv8cj): proxied `bd comment` did NOT fire on_update for WISP %s (the direct path re-fetches wisp-aware via GetIssueInTxSplit); marker=%q", wisp.ID, got)
		}
	})
}
