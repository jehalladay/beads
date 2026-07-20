//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerReopenCommentVisible_bimd0 is the proxied twin of
// TestEmbeddedReopenCommentVisible_bimd0. The proxied/domain reopen path goes
// through issueops.ReopenIssueInTx, which (pre-fix) recorded the reason via
// AddCommentEventInTx -> the events table, invisible to `bd comments` (which
// reads the comments table). Both entry points need the fix so a hub-connected
// crew's reopen --reason is readable — the beads-dfzre cmd-layer-misses-proxied
// anti-pattern in reverse (a shared-seam fix must be verified on both paths).
func TestProxiedServerReopenCommentVisible_bimd0(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("reason_visible_in_comments", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "rpcv")
		iss := bdProxiedCreate(t, bd, p.dir, "Proxied reopen reason")
		bdProxiedClose(t, bd, p.dir, iss.ID)

		const reason = "KEEPME-bimd0-proxied-regression"
		out := bdProxiedReopen(t, bd, p.dir, iss.ID, "--reason", reason)
		if !strings.Contains(out, "Reopened") {
			t.Fatalf("expected 'Reopened' in output: %s", out)
		}

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "comments", iss.ID)
		if err != nil {
			t.Fatalf("bd comments %s failed: %v\nstderr:\n%s", iss.ID, err, stderr)
		}
		if !strings.Contains(stdout, reason) {
			t.Errorf("reopen --reason %q not visible in proxied `bd comments %s` (beads-bimd0: ReopenIssueInTx wrote to the events table, not the readable comments table).\ngot:\n%s", reason, iss.ID, stdout)
		}
	})
}
