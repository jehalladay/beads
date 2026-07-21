//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedServerLink covers beads-8csa: `bd link` (shorthand for `bd dep add`)
// must work for hub-connected (proxied-server) crew — previously it hit nil-store
// "storage is nil" because link.go used the direct `store` with no proxied routing.
func TestProxiedServerLink(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("default_blocks_persists", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "lnk1")
		a := bdProxiedCreate(t, bd, p.dir, "Link A", "--type", "task")
		b := bdProxiedCreate(t, bd, p.dir, "Link B", "--type", "task")
		out, err := bdProxiedRun(t, bd, p.dir, "link", a.ID, b.ID)
		if err != nil {
			t.Fatalf("proxied bd link failed: %v\n%s", err, out)
		}
		if strings.Contains(string(out), "storage is nil") {
			t.Fatalf("proxied link hit nil-store path (beads-8csa regression): %s", out)
		}
		if !strings.Contains(string(out), "Linked") {
			t.Errorf("expected 'Linked', got: %s", out)
		}
		// Verify the edge persisted (a depends on b).
		list := bdProxiedRunOrFail(t, bd, p.dir, "dep", "list", a.ID)
		if !strings.Contains(list, b.ID) {
			t.Errorf("linked edge did not persist: %s", list)
		}
	})

	t.Run("custom_type_related_persists", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "lnk2")
		a := bdProxiedCreate(t, bd, p.dir, "Rel A", "--type", "task")
		b := bdProxiedCreate(t, bd, p.dir, "Rel B", "--type", "task")
		out, err := bdProxiedRun(t, bd, p.dir, "link", a.ID, b.ID, "--type", "related")
		if err != nil {
			t.Fatalf("proxied bd link --type related failed: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), "related") {
			t.Errorf("expected related link output: %s", out)
		}
	})

	// beads-tsu3m: the proxied link path now gates unknown types on IsWellKnown,
	// matching the DIRECT link path (beads-9v0d landed that gate AFTER beads-8csa
	// created this handler). A well-known-but-non-default type is still ACCEPTED.
	t.Run("wellknown_nondefault_type_accepted", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "lnk3")
		a := bdProxiedCreate(t, bd, p.dir, "Arb A", "--type", "task")
		b := bdProxiedCreate(t, bd, p.dir, "Arb B", "--type", "task")
		out, err := bdProxiedRun(t, bd, p.dir, "link", a.ID, b.ID, "--type", "discovered-from")
		if err != nil {
			t.Fatalf("proxied bd link --type discovered-from failed: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), "Linked") {
			t.Errorf("expected well-known type accepted: %s", out)
		}
	})

	// beads-tsu3m (the bug): an UNKNOWN dependency type (typo like "blockd") must
	// be REJECTED on the proxied path just as the direct path rejects it (9v0d).
	// Before the fix the proxied handler was IsValid()-only, so this typo'd edge
	// was silently PERSISTED rc=0 as a non-gating custom edge and the dependent
	// stayed ready — a silent-gate-drift false-success. Mutation-verify: assert
	// both the non-zero exit AND that no edge persisted.
	t.Run("unknown_type_rejected_and_not_persisted", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "lnk5")
		a := bdProxiedCreate(t, bd, p.dir, "Typo A", "--type", "task")
		b := bdProxiedCreate(t, bd, p.dir, "Typo B", "--type", "task")
		out, err := bdProxiedRun(t, bd, p.dir, "link", a.ID, b.ID, "--type", "blockd")
		if err == nil {
			t.Fatalf("proxied bd link --type blockd must be rejected (unknown type), but succeeded:\n%s", out)
		}
		if !strings.Contains(string(out), "unknown dependency type") {
			t.Errorf("expected 'unknown dependency type' rejection, got: %s", out)
		}
		// The false-success bug persisted the bad edge; verify it did NOT.
		list := bdProxiedRunOrFail(t, bd, p.dir, "dep", "list", a.ID)
		if strings.Contains(list, b.ID) {
			t.Errorf("rejected link must not persist an edge (tsu3m false-success): %s", list)
		}
	})

	t.Run("json_output", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "lnk4")
		a := bdProxiedCreate(t, bd, p.dir, "JSON A", "--type", "task")
		b := bdProxiedCreate(t, bd, p.dir, "JSON B", "--type", "task")
		out, err := bdProxiedRun(t, bd, p.dir, "link", a.ID, b.ID, "--json")
		if err != nil {
			t.Fatalf("proxied bd link --json failed: %v\n%s", err, out)
		}
		s := strings.TrimSpace(string(out))
		start := strings.IndexAny(s, "{")
		if start < 0 {
			t.Fatalf("no JSON object in link output: %s", s)
		}
		var m map[string]interface{}
		if jerr := json.Unmarshal([]byte(s[start:]), &m); jerr != nil {
			t.Fatalf("parsing link --json: %v\n%s", jerr, s)
		}
		if m["status"] != "added" {
			t.Errorf("expected status=added, got %v", m["status"])
		}
		if m["issue_id"] != a.ID || m["depends_on_id"] != b.ID {
			t.Errorf("unexpected envelope: %v", m)
		}
	})
}
