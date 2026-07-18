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

	// beads-8csa is routing-ONLY: it must preserve the direct link path's exact
	// validation (dt.IsValid(), NOT the IsWellKnown gate). An arbitrary type that
	// passes IsValid must be ACCEPTED on the proxied path just as it is directly
	// — otherwise we'd trade the nil-store bug for the OPPOSITE asymmetry (whether
	// link should reject unknown types is beads-9v0d, owned elsewhere).
	t.Run("arbitrary_valid_type_accepted_like_direct", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "lnk3")
		a := bdProxiedCreate(t, bd, p.dir, "Arb A", "--type", "task")
		b := bdProxiedCreate(t, bd, p.dir, "Arb B", "--type", "task")
		out, err := bdProxiedRun(t, bd, p.dir, "link", a.ID, b.ID, "--type", "discovered-from")
		if err != nil {
			t.Fatalf("proxied bd link --type discovered-from failed: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), "Linked") {
			t.Errorf("expected valid type accepted: %s", out)
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
