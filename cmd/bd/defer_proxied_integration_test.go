//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerDefer is the teeth for the defer leg of beads-aocj: bd defer
// must WORK in proxied-server mode. Before the fix, defer used the direct nil
// `store` in proxiedServerMode with no usesProxiedServer() routing, so it failed
// "storage is nil" — unlike `bd update --defer` which routes to a proxied
// handler. Mirrors beads-1zuh (relate/unrelate) and beads-qwez (assign/tag).
func TestProxiedServerDefer(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("defer_happy_path", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "dfr1")
		a := bdProxiedCreate(t, bd, p.dir, "Defer me", "--type", "task")

		out, err := bdProxiedRun(t, bd, p.dir, "defer", a.ID)
		s := string(out)
		if err != nil {
			t.Fatalf("proxied defer failed: %v\n%s", err, s)
		}
		if strings.Contains(s, "storage is nil") {
			t.Fatalf("proxied defer hit the nil-store path (beads-aocj regression): %s", s)
		}
		if !strings.Contains(s, "Deferred") {
			t.Errorf("expected '* Deferred' from proxied defer, got: %s", s)
		}
		// Verify the status actually changed to deferred via the proxied path.
		got := bdProxiedShow(t, bd, p.dir, a.ID)
		if string(got.Status) != "deferred" {
			t.Errorf("status after proxied defer = %q, want deferred", got.Status)
		}
	})

	t.Run("defer_with_reason_appends_notes", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "dfr2")
		a := bdProxiedCreate(t, bd, p.dir, "Defer with reason", "--type", "task")

		out, err := bdProxiedRun(t, bd, p.dir, "defer", a.ID, "--reason", "waiting on API")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied defer --reason failed: %v\n%s", err, s)
		}
		got := bdProxiedShow(t, bd, p.dir, a.ID)
		if string(got.Status) != "deferred" {
			t.Errorf("status after proxied defer --reason = %q, want deferred", got.Status)
		}
		if !strings.Contains(got.Notes, "waiting on API") {
			t.Errorf("notes after proxied defer --reason = %q, want to contain the reason", got.Notes)
		}
	})

	t.Run("defer_all_unresolvable_exits_nonzero", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "dfr3")
		// No such issue → every requested ID fails → non-zero exit (not false
		// success), matching the direct defer path (beads-fg6).
		out := bdProxiedRunExpectFail(t, bd, p.dir, "defer", "no-such-id-xyz")
		if strings.Contains(out, "storage is nil") {
			t.Fatalf("proxied defer hit the nil-store path (beads-aocj regression): %s", out)
		}
	})
}

// bdProxiedRunExpectFail runs a proxied bd command expecting a non-zero exit,
// returning combined output. Fails the test if the command unexpectedly
// succeeds.
func bdProxiedRunExpectFail(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	out, err := bdProxiedRun(t, bd, dir, args...)
	if err == nil {
		t.Fatalf("bd %s should have failed; got:\n%s", strings.Join(args, " "), out)
	}
	return string(out)
}
