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

	t.Run("undefer_non_deferred_reports_and_fails", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "und2")
		iss := bdProxiedCreate(t, bd, p.dir, "Open", "--type", "task")
		// Not deferred → the only requested ID fails the status guard → rc!=0.
		out, err := bdProxiedRun(t, bd, p.dir, "undefer", iss.ID)
		if err == nil {
			t.Errorf("expected non-zero exit undeferring a non-deferred issue, got success:\n%s", out)
		}
		if !strings.Contains(string(out), "not deferred") {
			t.Errorf("expected 'not deferred' message, got: %s", out)
		}
	})
}
