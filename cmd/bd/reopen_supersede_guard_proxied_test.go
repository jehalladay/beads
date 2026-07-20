//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerReopenSupersedeGuard is the proxied twin of the beads-8sjb3
// supersede reopen guard (beads-1mfmk). The direct path (cmd/bd/reopen.go
// supersededByTargets) refuses to reopen an issue that still carries an outgoing
// `supersedes` edge; the proxied handler must enforce the same guard so a
// hub-connected crew can't bypass it (the beads-dfzre cmd-layer-misses-proxied
// anti-pattern — the proxied handler had mirrored only the closed-epic-parent
// guard, never the supersede guard). Overridable with --force.
func TestProxiedServerReopenSupersedeGuard(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("reopen_superseded_refused", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "rpsg")
		old := bdProxiedCreate(t, bd, p.dir, "The old issue")
		newer := bdProxiedCreate(t, bd, p.dir, "The replacement")
		// `bd supersede <old> --with <new>` closes old + adds supersedes edge.
		if _, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "supersede", old.ID, "--with", newer.ID); err != nil {
			t.Fatalf("bd supersede failed: %v\nstderr:\n%s", err, stderr)
		}
		out := bdProxiedReopenFail(t, bd, p.dir, old.ID)
		if !strings.Contains(out, "superseded by") {
			t.Errorf("expected a 'superseded by' guard error reopening %s, got: %s", old.ID, out)
		}
	})

	t.Run("force_overrides_guard", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "rpsgf")
		old := bdProxiedCreate(t, bd, p.dir, "The old issue 2")
		newer := bdProxiedCreate(t, bd, p.dir, "The replacement 2")
		if _, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "supersede", old.ID, "--with", newer.ID); err != nil {
			t.Fatalf("bd supersede failed: %v\nstderr:\n%s", err, stderr)
		}
		out := bdProxiedReopen(t, bd, p.dir, old.ID, "--force")
		if !strings.Contains(out, "Reopened") {
			t.Errorf("expected --force to reopen the superseded issue, got: %s", out)
		}
	})

	t.Run("normal_reopen_unaffected", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "rpsgn")
		iss := bdProxiedCreate(t, bd, p.dir, "Plain closed")
		bdProxiedClose(t, bd, p.dir, iss.ID)
		out := bdProxiedReopen(t, bd, p.dir, iss.ID)
		if !strings.Contains(out, "Reopened") {
			t.Errorf("plain reopen (no supersedes) should succeed, got: %s", out)
		}
	})
}
