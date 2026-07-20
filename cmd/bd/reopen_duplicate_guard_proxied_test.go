//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerReopenDuplicateGuard is the proxied twin of the beads-8nugc
// reopen duplicate guard. The direct path (cmd/bd/reopen.go duplicatesTargets)
// refuses to reopen an issue that still carries an outgoing `duplicates` edge;
// the proxied handler must enforce the same guard so a hub-connected crew can't
// bypass it (the beads-dfzre cmd-layer-misses-proxied anti-pattern). Overridable
// with --force.
func TestProxiedServerReopenDuplicateGuard(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("reopen_duplicate_refused", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "rpdg")
		canonical := bdProxiedCreate(t, bd, p.dir, "Canonical issue")
		dup := bdProxiedCreate(t, bd, p.dir, "The duplicate")
		// `bd duplicate <dup> --of <canonical>` closes dup + adds duplicates edge.
		if _, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "duplicate", dup.ID, "--of", canonical.ID); err != nil {
			t.Fatalf("bd duplicate failed: %v\nstderr:\n%s", err, stderr)
		}
		out := bdProxiedReopenFail(t, bd, p.dir, dup.ID)
		if !strings.Contains(out, "duplicate of") {
			t.Errorf("expected a 'duplicate of' guard error reopening %s, got: %s", dup.ID, out)
		}
	})

	t.Run("force_overrides_guard", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "rpdgf")
		canonical := bdProxiedCreate(t, bd, p.dir, "Canonical 2")
		dup := bdProxiedCreate(t, bd, p.dir, "The duplicate 2")
		if _, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "duplicate", dup.ID, "--of", canonical.ID); err != nil {
			t.Fatalf("bd duplicate failed: %v\nstderr:\n%s", err, stderr)
		}
		out := bdProxiedReopen(t, bd, p.dir, dup.ID, "--force")
		if !strings.Contains(out, "Reopened") {
			t.Errorf("expected --force to reopen the duplicate issue, got: %s", out)
		}
	})

	t.Run("normal_reopen_unaffected", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "rpdgn")
		iss := bdProxiedCreate(t, bd, p.dir, "Plain closed")
		bdProxiedClose(t, bd, p.dir, iss.ID)
		out := bdProxiedReopen(t, bd, p.dir, iss.ID)
		if !strings.Contains(out, "Reopened") {
			t.Errorf("plain reopen (no duplicates) should succeed, got: %s", out)
		}
	})
}
