//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedWispSourceDepRemove_63dwu is the PROXIED dep-REMOVE mirror of the
// dep-add wisp-source fix (beads-zdg7x).
//
// beads-63dwu: runDepRemoveProxiedServer (dep_proxied_server.go) removed via
// RemoveDependency (removeDep useWisp=false) -> Delete(pickDepTable(false) =
// "dependencies"). The domain remove path is FLAG-routed, UNLIKE the direct
// store's Delete which auto-detects the wisp table via IsActiveWispInTx. So for
// a WISP SOURCE (whose edges live in wisp_dependencies) the DELETE targeted the
// wrong ("dependencies") table -> 0 rows deleted, but removeDep discards the
// count and returns nil -> a SILENT FALSE SUCCESS ("✓ Removed" / status:removed
// rc=0), a byh6-class false success that a CI/agent gate reads as proof the
// edge is gone while it lives on in wisp_dependencies. (The w2tk edge-exists
// precheck did NOT fire because GetIssueDependencyRecords auto-partitions wisp
// vs perm IDs and reads the wisp table — so the edge is found, then deleted
// from the wrong table.) The direct/embedded path worked.
//
// The fix detects the source kind via proxiedDepSourceIsWisp and routes the
// removal (RemoveWispDependency) — and, symmetrically, the precheck
// (GetWispDependencyRecords) — to the wisp-backed tables.
//
// MUTATION-VERIFY: hardcode srcIsWisp=false in runDepRemoveProxiedServer and
// the wisp-source remove subtest FAILS: the first remove FALSELY succeeds
// (no-op on the wrong table) so a second remove of the "gone" edge ALSO
// succeeds instead of erroring w2tk (the edge was never actually deleted).
func TestProxiedWispSourceDepRemove_63dwu(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	assertClean := func(t *testing.T, what, stdout, stderr string, err error) {
		t.Helper()
		combined := stdout + stderr
		if strings.Contains(combined, "no dependency to remove") {
			t.Fatalf("%s hit the 63dwu symptom ('no dependency to remove' — the wisp-source edge was invisible to the issues-table precheck):\nstdout:\n%s\nstderr:\n%s", what, stdout, stderr)
		}
		if strings.Contains(combined, "not found") {
			t.Fatalf("%s hit a 'not found' error (wisp routing gap):\nstdout:\n%s\nstderr:\n%s", what, stdout, stderr)
		}
		if strings.Contains(combined, "storage is nil") || strings.Contains(combined, "nil pointer dereference") || strings.Contains(combined, "panic:") {
			t.Fatalf("%s hit a nil-store panic in proxied mode:\nstdout:\n%s\nstderr:\n%s", what, stdout, stderr)
		}
		if err != nil {
			t.Fatalf("%s failed in proxied mode: %v\nstdout:\n%s\nstderr:\n%s", what, err, stdout, stderr)
		}
	}

	// add a wisp-source edge (routes to wisp_dependencies via the zdg7x fix),
	// then remove it: the remove precheck + delete must both hit the wisp table.
	t.Run("dep_remove_wisp_source_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "wdr")
		src := bdProxiedCreate(t, bd, p.dir, "wisp source r", "--type", "task", "--ephemeral")
		tgt := bdProxiedCreate(t, bd, p.dir, "wisp target r", "--type", "task", "--ephemeral")

		// establish the edge in wisp_dependencies.
		addOut, addStderr, addErr := bdProxiedRunBuffers(t, bd, p.dir, "dep", "add", src.ID, tgt.ID)
		assertClean(t, "bd dep add (wisp source, setup)", addOut, addStderr, addErr)

		// remove it: precheck (GetWispDependencyRecords) must find the edge and
		// the delete (RemoveWispDependency) must succeed.
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "dep", "remove", src.ID, tgt.ID)
		assertClean(t, "bd dep remove (wisp source)", stdout, stderr, err)

		// after removal the edge must be gone: a second remove must now honestly
		// report "no dependency to remove" (proves the first remove actually
		// deleted from wisp_dependencies, not a no-op on the issues table).
		_, stderr2, err2 := bdProxiedRunBuffers(t, bd, p.dir, "dep", "remove", src.ID, tgt.ID)
		if err2 == nil {
			t.Errorf("second remove of an already-removed wisp-source edge should error (w2tk), got success; stderr:\n%s", stderr2)
		}
		if !strings.Contains(stderr2, "no dependency to remove") {
			t.Errorf("second remove should report 'no dependency to remove' (proves the first remove hit wisp_dependencies); got stderr:\n%s", stderr2)
		}
	})

	// --json parity for the wisp-source remove.
	t.Run("dep_remove_wisp_source_json_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "wrj")
		src := bdProxiedCreate(t, bd, p.dir, "wisp source rj", "--type", "task", "--ephemeral")
		tgt := bdProxiedCreate(t, bd, p.dir, "wisp target rj", "--type", "task", "--ephemeral")

		_, addStderr, addErr := bdProxiedRunBuffers(t, bd, p.dir, "dep", "add", src.ID, tgt.ID)
		assertClean(t, "bd dep add (wisp source json setup)", "", addStderr, addErr)

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "dep", "remove", src.ID, tgt.ID, "--json")
		assertClean(t, "bd dep remove --json (wisp source)", stdout, stderr, err)
		if !strings.Contains(stdout, "\"removed\"") {
			t.Errorf("expected status:removed in --json output for a wisp-source remove:\n%s", stdout)
		}
	})

	// Regression boundary: a plain (non-wisp) issue source remove must still
	// work — the wisp routing must not break the ordinary issues path.
	t.Run("dep_remove_issue_source_still_works", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "wri")
		src := bdProxiedCreate(t, bd, p.dir, "issue source r", "--type", "bug")
		tgt := bdProxiedCreate(t, bd, p.dir, "issue target r", "--type", "bug")

		_, addStderr, addErr := bdProxiedRunBuffers(t, bd, p.dir, "dep", "add", src.ID, tgt.ID)
		assertClean(t, "bd dep add (issue source setup)", "", addStderr, addErr)

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "dep", "remove", src.ID, tgt.ID)
		assertClean(t, "bd dep remove (issue source)", stdout, stderr, err)
	})
}
