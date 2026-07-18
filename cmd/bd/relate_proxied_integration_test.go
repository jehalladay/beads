//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerRelate is the teeth for beads-1zuh: bd dep relate / unrelate
// must WORK in proxied-server mode (previously failed "storage is nil" because
// runRelate/runUnrelate used the direct nil `store` with no usesProxiedServer()
// routing). Also verifies the direct-path guards carry over: self-relate reject
// + the beads-piud no-op unrelate guard.
func TestProxiedServerRelate(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("relate_then_unrelate_happy_path", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "rel1")
		a := bdProxiedCreate(t, bd, p.dir, "Rel A", "--type", "task")
		b := bdProxiedCreate(t, bd, p.dir, "Rel B", "--type", "task")

		out := bdProxiedDep(t, bd, p.dir, "relate", a.ID, b.ID)
		if !strings.Contains(out, "Linked") {
			t.Errorf("expected '✓ Linked' from proxied relate, got: %s", out)
		}
		if strings.Contains(out, "storage is nil") {
			t.Fatalf("proxied relate hit the nil-store path (beads-1zuh regression): %s", out)
		}

		out = bdProxiedDep(t, bd, p.dir, "unrelate", a.ID, b.ID)
		if !strings.Contains(out, "Unlinked") {
			t.Errorf("expected '✓ Unlinked' from proxied unrelate, got: %s", out)
		}
	})

	t.Run("relate_self_rejected", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "rel2")
		a := bdProxiedCreate(t, bd, p.dir, "Self", "--type", "task")
		out := bdProxiedDepFail(t, bd, p.dir, "relate", a.ID, a.ID)
		if !strings.Contains(out, "itself") {
			t.Errorf("expected self-relate rejection, got: %s", out)
		}
	})

	t.Run("unrelate_never_related_fails_loud", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "rel3")
		a := bdProxiedCreate(t, bd, p.dir, "NR A", "--type", "task")
		b := bdProxiedCreate(t, bd, p.dir, "NR B", "--type", "task")
		// beads-piud parity: a no-op unrelate must fail, not print a false success.
		out := bdProxiedDepFail(t, bd, p.dir, "unrelate", a.ID, b.ID)
		if !strings.Contains(out, "no relates-to link to remove") {
			t.Errorf("expected honest no-op error, got: %s", out)
		}
	})

	// beads-hwgq: re-relating an already-related pair must report an honest
	// "Already related, no change" (rc=0), mirroring the direct path (57nt) —
	// not a false "✓ Linked" as if the edge were newly created.
	t.Run("re_relate_reports_no_change", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "rel4")
		a := bdProxiedCreate(t, bd, p.dir, "RR A", "--type", "task")
		b := bdProxiedCreate(t, bd, p.dir, "RR B", "--type", "task")

		out := bdProxiedDep(t, bd, p.dir, "relate", a.ID, b.ID)
		if !strings.Contains(out, "Linked") {
			t.Fatalf("expected first relate to Link, got: %s", out)
		}

		out = bdProxiedDep(t, bd, p.dir, "relate", a.ID, b.ID)
		if !strings.Contains(out, "Already related, no change") {
			t.Errorf("expected 'Already related, no change' on re-relate, got: %s", out)
		}
	})

	t.Run("re_relate_json_unchanged", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "rel5")
		a := bdProxiedCreate(t, bd, p.dir, "RRJ A", "--type", "task")
		b := bdProxiedCreate(t, bd, p.dir, "RRJ B", "--type", "task")
		bdProxiedDep(t, bd, p.dir, "relate", a.ID, b.ID)
		m := bdProxiedDepJSON(t, bd, p.dir, "relate", a.ID, b.ID)
		if m["unchanged"] != true {
			t.Errorf("expected unchanged:true on re-relate --json, got: %v", m)
		}
	})
}
