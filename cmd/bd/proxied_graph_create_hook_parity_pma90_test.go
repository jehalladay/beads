//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// beads-pma90 (PROXIED on_create + dependency on_update hook parity for
// `bd create --graph`).
//
// The DIRECT graph create fires hooks after the commit: create.go --graph →
// createIssuesFromGraph → executeGraphApply (graph_apply.go:568
// store.RunInTransaction + tx.CreateIssues + tx.AddDependency). Via the
// HookFiringStore decorator (hook_decorator.go) the hookTrackingTransaction
// accumulates createHookEvents (per-node on_create + a synthetic per-label
// on_update) AND dependencyHookEvents (one on_update per persisted edge), fired
// post-commit. BUT the proxied graph handler (create_proxied_server.go
// runCreateProxiedGraph) commits via the UOW use-case (ApplyIssueGraph /
// ApplyWispGraph), which fires NO hooks — so a hub-connected (proxied,
// store==nil) crew's on_create / dependency automation silently never ran for
// `bd create --graph`. This is the graph-leg sibling of beads-w1vxy (single
// create) and beads-29tyj (comment / dep / relate on_update).
//
// Driven END-TO-END through the ACTUAL proxied `bd` subprocess (a UOW-level
// helper would false-green by skipping the CLI hook plumbing). MUTATION-VERIFIED:
// remove the captureProxiedGraphCreateSnapshots / fireProxiedGraphCreateSnapshots
// calls added to runCreateProxiedGraph and these sub-tests go RED.
func TestProxiedGraphCreateHookParity_pma90(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	appendHookBody := func(markerPath string) string {
		return "#!/bin/sh\necho \"$1\" >> " + markerPath + "\n"
	}
	writePlan := func(t *testing.T, dir, name, body string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write plan %s: %v", name, err)
		}
		return p
	}
	graphIDs := func(t *testing.T, dir, planFile string, args ...string) map[string]string {
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

	// Each graph node fires on_create.
	t.Run("graph_nodes_fire_on_create", func(t *testing.T) {
		dir := t.TempDir()
		createMarker := filepath.Join(dir, "on_create_marker")
		p := bdProxiedInitWithHooks(t, bd, "gcp", map[string]string{
			"on_create": appendHookBody(createMarker),
		})

		_ = os.Remove(createMarker)
		plan := `{"nodes":[` +
			`{"key":"a","title":"graph node a","type":"task"},` +
			`{"key":"b","title":"graph node b","type":"task"}` +
			`],"edges":[]}`
		ids := graphIDs(t, p.dir, writePlan(t, p.dir, "p1.json", plan))
		if ids["a"] == "" || ids["b"] == "" {
			t.Fatalf("expected two node ids, got %v", ids)
		}
		for _, id := range []string{ids["a"], ids["b"]} {
			if got, ok := waitForMarkerContains(createMarker, id, 5*time.Second); !ok {
				t.Errorf("REGRESSION (beads-pma90): proxied `bd create --graph` did NOT fire on_create for node %s (direct fires it via HookFiringStore.CreateIssues → createHookEvents); marker=%q", id, got)
			}
		}
	})

	// A wisp (ephemeral) graph fires on_create for each node too — the direct
	// wisp graph routes through the same tx.CreateIssues (executeGraphApply,
	// opts.Ephemeral) and createHookEvents does not skip Ephemeral, mirroring the
	// w1vxy single-create wisp parity. The proxied fix fires after BOTH the
	// ApplyWispGraph and ApplyIssueGraph branches, so `--ephemeral` must fire too.
	t.Run("wisp_graph_nodes_fire_on_create", func(t *testing.T) {
		dir := t.TempDir()
		createMarker := filepath.Join(dir, "on_create_marker")
		p := bdProxiedInitWithHooks(t, bd, "wgp", map[string]string{
			"on_create": appendHookBody(createMarker),
		})

		_ = os.Remove(createMarker)
		plan := `{"nodes":[` +
			`{"key":"a","title":"wisp graph node a","type":"task"},` +
			`{"key":"b","title":"wisp graph node b","type":"task"}` +
			`],"edges":[]}`
		ids := graphIDs(t, p.dir, writePlan(t, p.dir, "pw.json", plan), "--ephemeral")
		if ids["a"] == "" || ids["b"] == "" {
			t.Fatalf("expected two wisp node ids, got %v", ids)
		}
		for _, id := range []string{ids["a"], ids["b"]} {
			if got, ok := waitForMarkerContains(createMarker, id, 5*time.Second); !ok {
				t.Errorf("REGRESSION (beads-pma90): proxied `bd create --graph --ephemeral` did NOT fire on_create for wisp node %s (direct fires it via HookFiringStore.CreateIssues → createHookEvents, which does not skip Ephemeral); marker=%q", id, got)
			}
		}
	})

	// A labeled graph node fires on_create AND the synthetic on_update label stream.
	t.Run("labeled_graph_node_fires_on_create_and_label_on_update", func(t *testing.T) {
		dir := t.TempDir()
		createMarker := filepath.Join(dir, "on_create_marker")
		updateMarker := filepath.Join(dir, "on_update_marker")
		p := bdProxiedInitWithHooks(t, bd, "glp", map[string]string{
			"on_create": appendHookBody(createMarker),
			"on_update": appendHookBody(updateMarker),
		})

		_ = os.Remove(createMarker)
		_ = os.Remove(updateMarker)
		plan := `{"nodes":[{"key":"a","title":"labeled graph node","type":"task","labels":["alpha"]}],"edges":[]}`
		ids := graphIDs(t, p.dir, writePlan(t, p.dir, "p2.json", plan))
		id := ids["a"]
		if id == "" {
			t.Fatalf("no node id: %v", ids)
		}
		if got, ok := waitForMarkerContains(createMarker, id, 5*time.Second); !ok {
			t.Errorf("REGRESSION (beads-pma90): labeled graph node did NOT fire on_create for %s; marker=%q", id, got)
		}
		if got, ok := waitForMarkerContains(updateMarker, id, 5*time.Second); !ok {
			t.Errorf("REGRESSION (beads-pma90): labeled graph node did NOT fire the synthetic on_update label stream for %s (createHookEvents parity); marker=%q", id, got)
		}
	})

	// A dependency edge fires on_update on the dependent node (dependencyHookEvents parity).
	t.Run("graph_edge_fires_dependency_on_update", func(t *testing.T) {
		dir := t.TempDir()
		updateMarker := filepath.Join(dir, "on_update_marker")
		p := bdProxiedInitWithHooks(t, bd, "gep", map[string]string{
			"on_update": appendHookBody(updateMarker),
		})

		_ = os.Remove(updateMarker)
		// node b depends on a (blocked-by edge) → the direct path fires one
		// on_update on b for the persisted edge.
		plan := `{"nodes":[` +
			`{"key":"a","title":"graph dep target","type":"task"},` +
			`{"key":"b","title":"graph dep source","type":"task"}` +
			`],"edges":[{"from_key":"b","to_key":"a","type":"blocks"}]}`
		ids := graphIDs(t, p.dir, writePlan(t, p.dir, "p3.json", plan))
		if ids["a"] == "" || ids["b"] == "" {
			t.Fatalf("expected two node ids, got %v", ids)
		}
		// The dependency on_update fires on whichever node carries the persisted
		// edge; assert the marker saw at least one of the two created IDs.
		_, okA := waitForMarkerContains(updateMarker, ids["a"], 5*time.Second)
		_, okB := waitForMarkerContains(updateMarker, ids["b"], 2*time.Second)
		if !okA && !okB {
			t.Errorf("REGRESSION (beads-pma90): proxied graph edge did NOT fire the dependency on_update for %s/%s (direct fires it via dependencyHookEvents); on_update marker empty", ids["a"], ids["b"])
		}
	})
}
