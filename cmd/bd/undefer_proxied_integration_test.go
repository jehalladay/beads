//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerUndefer is the teeth for beads-fszd (undefer leg): bd undefer
// must WORK in proxied-server mode (previously returned "database not
// initialized" because the RunE used the nil direct store with no
// usesProxiedServer() routing). Also verifies the deferred-status guard and the
// partial-exit contract carry over to the proxied path.
func TestProxiedServerUndefer(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("undefer_deferred_issue_restores_open", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "und1")
		iss := bdProxiedCreate(t, bd, p.dir, "Deferred", "--type", "task", "--defer", "+8760h")

		out, err := bdProxiedRun(t, bd, p.dir, "undefer", iss.ID)
		if err != nil {
			t.Fatalf("proxied undefer failed: %v\n%s", err, out)
		}
		s := string(out)
		if strings.Contains(s, "database not initialized") || strings.Contains(s, "storage is nil") {
			t.Fatalf("proxied undefer hit the nil-store path (beads-fszd regression): %s", s)
		}
		if !strings.Contains(s, "Undeferred") {
			t.Errorf("expected 'Undeferred' output, got: %s", s)
		}
	})

	t.Run("undefer_non_deferred_is_rc0_noop", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "und2")
		iss := bdProxiedCreate(t, bd, p.dir, "Open", "--type", "task")
		// beads-36iz0: not-deferred is an idempotent advisory no-op on the
		// proxied path too — undefer's target state (open) is already satisfied,
		// so rc=0 (mirrors reopen's already-open path / the direct path). A
		// genuine unresolvable id still fails; not-deferred does not. The advisory
		// lands on stderr, so capture both streams.
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "undefer", iss.ID)
		if err != nil {
			t.Errorf("expected rc=0 undeferring a non-deferred issue (beads-36iz0 no-op), got error: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if !strings.Contains(stderr, "not deferred") {
			t.Errorf("expected 'not deferred' advisory on stderr, got stdout=%q stderr=%q", stdout, stderr)
		}
	})
}
