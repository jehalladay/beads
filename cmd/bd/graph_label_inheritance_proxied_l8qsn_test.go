//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// beads-l8qsn PROXIED twin: the embedded graph path (graph_apply.go) and the
// domain applyGraph path (create_proxied_server.go -> domain issue.go) both
// created graph children top-level without inheriting parent labels. The
// embedded fix lives in executeGraphApply; the proxied fix lives in domain
// applyGraph Pass 4.5 (fed GraphPlan.NoInheritLabels from buildDomainGraphPlan).
// This exercises the domain path under BEADS_TEST_PROXIED_SERVER=1, asserting
// parity with the embedded teeth (TestEmbeddedGraphLabelInheritance_l8qsn).
// MUTATION-VERIFIED: removing the domain Pass 4.5 inherit block leaves proxied
// graph children with no inherited labels.
func TestProxiedGraphLabelInheritance_l8qsn(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	writePlan := func(t *testing.T, dir, name, body string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write plan %s: %v", name, err)
		}
		return p
	}

	graphIDs := func(t *testing.T, bd, dir, planFile string, args ...string) map[string]string {
		t.Helper()
		full := append([]string{"create", "--graph", planFile, "--json"}, args...)
		out, err := bdProxiedRun(t, bd, dir, full...)
		if err != nil {
			t.Fatalf("bd create --graph failed: %v\n%s", err, out)
		}
		var result GraphApplyResult
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatalf("parse graph result: %v\nstdout:\n%s", err, out)
		}
		return result.IDs
	}

	labelSet := func(t *testing.T, bd, dir, id string) map[string]bool {
		t.Helper()
		got := bdProxiedShow(t, bd, dir, id)
		set := make(map[string]bool, len(got.Labels))
		for _, l := range got.Labels {
			set[l] = true
		}
		return set
	}

	// parent_id → child inherits parent labels via the domain applyGraph path.
	t.Run("parent_id_inherits", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pgi")
		parent := bdProxiedCreate(t, bd, p.dir, "labeled parent", "-t", "epic", "-l", "area:core,team-a")
		plan := `{"nodes":[{"key":"c","title":"graph child","type":"task","parent_id":"` + parent.ID + `"}],"edges":[]}`
		ids := graphIDs(t, bd, p.dir, writePlan(t, p.dir, "p1.json", plan))
		childID := ids["c"]
		if childID == "" {
			t.Fatalf("no child id: %v", ids)
		}
		set := labelSet(t, bd, p.dir, childID)
		if !set["area:core"] || !set["team-a"] {
			t.Errorf("proxied graph child should inherit parent labels, got %v", set)
		}
	})

	// parent_key declared later → still inherits (post-pass order-independence).
	t.Run("parent_key_declared_later_inherits", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pgk")
		plan := `{"nodes":[` +
			`{"key":"child","title":"graph child","type":"task","parent_key":"root"},` +
			`{"key":"root","title":"graph root","type":"epic","labels":["area:core","gate:x"]}` +
			`],"edges":[]}`
		ids := graphIDs(t, bd, p.dir, writePlan(t, p.dir, "p2.json", plan))
		childID := ids["child"]
		if childID == "" {
			t.Fatalf("no child id: %v", ids)
		}
		set := labelSet(t, bd, p.dir, childID)
		if !set["area:core"] || !set["gate:x"] {
			t.Errorf("proxied graph child should inherit later-declared parent labels, got %v", set)
		}
	})

	// --no-inherit-labels opt-out suppresses inheritance (parity).
	t.Run("no_inherit_labels_opt_out", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pgn")
		parent := bdProxiedCreate(t, bd, p.dir, "labeled parent", "-t", "epic", "-l", "parent-label")
		plan := `{"nodes":[{"key":"c","title":"graph child","type":"task","parent_id":"` + parent.ID + `","labels":["own-label"]}],"edges":[]}`
		ids := graphIDs(t, bd, p.dir, writePlan(t, p.dir, "p3.json", plan), "--no-inherit-labels")
		childID := ids["c"]
		if childID == "" {
			t.Fatalf("no child id: %v", ids)
		}
		set := labelSet(t, bd, p.dir, childID)
		if !set["own-label"] {
			t.Errorf("proxied graph child should keep its own label, got %v", set)
		}
		if set["parent-label"] {
			t.Errorf("proxied graph child should NOT inherit under --no-inherit-labels, got %v", set)
		}
	})
}
