//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerPriority is the teeth for beads-rejl: bd priority must WORK
// in proxied-server mode. Before the fix, it used the direct nil `store` via
// resolveAndGetIssueForMutation in proxiedServerMode with no usesProxiedServer()
// routing, so it failed "storage is nil" — unlike its long form
// (`bd update --priority`) which routes to a proxied handler. Mirrors the
// beads-qwez (assign/tag) and beads-8xb7 (defer) routing fixes.
func TestProxiedServerPriority(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("happy_path", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pri1")
		a := bdProxiedCreate(t, bd, p.dir, "Prioritize me", "--type", "task", "--priority", "3")

		out, err := bdProxiedRun(t, bd, p.dir, "priority", a.ID, "0")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied priority failed: %v\n%s", err, s)
		}
		if strings.Contains(s, "storage is nil") {
			t.Fatalf("proxied priority hit the nil-store path (beads-rejl regression): %s", s)
		}
		if !strings.Contains(s, "priority") && !strings.Contains(s, "P0") {
			t.Errorf("expected priority-set confirmation, got: %s", s)
		}
		// Verify the mutation actually persisted through the proxied path.
		got := bdProxiedShow(t, bd, p.dir, a.ID)
		if got.Priority != 0 {
			t.Errorf("priority after proxied set = %d, want 0", got.Priority)
		}
	})

	t.Run("invalid_priority_rejected", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pri2")
		a := bdProxiedCreate(t, bd, p.dir, "Bad priority", "--type", "task")

		out, err := bdProxiedRun(t, bd, p.dir, "priority", a.ID, "99")
		s := string(out)
		if err == nil {
			t.Fatalf("expected nonzero exit for an out-of-range priority, got success:\n%s", s)
		}
		if strings.Contains(s, "storage is nil") {
			t.Fatalf("proxied priority hit the nil-store path (beads-rejl regression): %s", s)
		}
	})

	t.Run("unresolvable_id_nonzero_exit", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pri3")
		out, err := bdProxiedRun(t, bd, p.dir, "priority", "no-such-id", "1")
		s := string(out)
		if err == nil {
			t.Fatalf("expected nonzero exit when id does not resolve, got success:\n%s", s)
		}
		if strings.Contains(s, "storage is nil") {
			t.Fatalf("proxied priority hit the nil-store path (beads-rejl regression): %s", s)
		}
	})
}
