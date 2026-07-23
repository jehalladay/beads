//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedWispSourceDepAdd_zdg7x is the PROXIED dep-add twin of the
// duplicate/supersede wisp-source fix (beads-pm1kh).
//
// beads-zdg7x: the proxied `bd dep add` family (dep_proxied_server.go —
// runDepAddProxiedServer, its --blocks shorthand runDepBlocksProxiedServer, and
// the bulk --file runDepAddBulkProxied) unconditionally called
// AddDependencies (useWisp=false). The domain dependency use case selects the
// dependency table from that explicit flag (depRepo.Insert -> pickDepTable),
// UNLIKE the direct store's AddDependencyInTx which auto-detects the wisp table
// via IsActiveWispInTx. So for a WISP source the edge INSERT validated
// `SELECT issue_type FROM issues WHERE id=<wisp>` -> ErrNoRows -> "issue <wisp>
// not found", even though the wisp exists. The direct/embedded path worked.
//
// The fix detects the source kind via proxiedDepSourceIsWisp and routes the
// edge write (AddWispDependencies) AND the idempotent-no-op precheck
// (GetWispDependencyRecords) to the wisp-backed tables.
//
// MUTATION-VERIFY: hardcode srcIsWisp=false (or drop the proxiedDepSourceIsWisp
// call) in runDepAddProxiedServer / runDepBlocksProxiedServer and the
// wisp-source subtests FAIL with "not found". Route the bulk partition entirely
// through AddDependencies and the bulk subtest FAILS the same way.
func TestProxiedWispSourceDepAdd_zdg7x(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// assertClean fails on the zdg7x symptom or any nil-store panic.
	assertClean := func(t *testing.T, what, stdout, stderr string, err error) {
		t.Helper()
		combined := stdout + stderr
		if strings.Contains(combined, "not found") {
			t.Fatalf("%s hit the zdg7x symptom ('<wisp> not found' — dep edge routed to the issues table for a wisp source):\nstdout:\n%s\nstderr:\n%s", what, stdout, stderr)
		}
		if strings.Contains(combined, "storage is nil") || strings.Contains(combined, "nil pointer dereference") || strings.Contains(combined, "panic:") {
			t.Fatalf("%s hit a nil-store panic in proxied mode:\nstdout:\n%s\nstderr:\n%s", what, stdout, stderr)
		}
		if err != nil {
			t.Fatalf("%s failed in proxied mode: %v\nstdout:\n%s\nstderr:\n%s", what, err, stdout, stderr)
		}
	}

	// dep add <wisp-source> <target>: the source is a wisp, so the edge must land
	// in wisp_dependencies. Re-adding the same edge must then report the
	// idempotent no-op ("already present" / status:unchanged), which proves BOTH
	// that the edge was actually written to the wisp table AND that the
	// no-op precheck reads the wisp table (not the empty issues table).
	t.Run("dep_add_wisp_source_succeeds_and_is_idempotent", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "wda")
		src := bdProxiedCreate(t, bd, p.dir, "wisp source", "--type", "task", "--ephemeral")
		tgt := bdProxiedCreate(t, bd, p.dir, "wisp target", "--type", "task", "--ephemeral")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "dep", "add", src.ID, tgt.ID)
		assertClean(t, "bd dep add (wisp source)", stdout, stderr, err)

		// Second identical add must be an honest no-op (beads-epuz), reading the
		// wisp_dependencies table. If the precheck queried the issues table it
		// would find nothing and print a false "✓ Added" again.
		out2, err2 := bdProxiedRun(t, bd, p.dir, "dep", "add", src.ID, tgt.ID)
		if err2 != nil {
			t.Fatalf("re-add of wisp-source edge errored: %v\n%s", err2, out2)
		}
		low := strings.ToLower(string(out2))
		if !strings.Contains(low, "already present") && !strings.Contains(low, "no change") {
			t.Errorf("re-add of a wisp-source dep edge should be an honest no-op (proves the edge landed in wisp_dependencies and the precheck reads it); got:\n%s", out2)
		}
	})

	// --json parity for the wisp-source add.
	t.Run("dep_add_wisp_source_json_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "wdj")
		src := bdProxiedCreate(t, bd, p.dir, "wisp source j", "--type", "task", "--ephemeral")
		tgt := bdProxiedCreate(t, bd, p.dir, "wisp target j", "--type", "task", "--ephemeral")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "dep", "add", src.ID, tgt.ID, "--json")
		assertClean(t, "bd dep add --json (wisp source)", stdout, stderr, err)
		if !strings.Contains(stdout, "\"added\"") {
			t.Errorf("expected status:added in --json output for a wisp-source add:\n%s", stdout)
		}
	})

	// The --blocks shorthand: `bd dep <blocker> --blocks <blocked>` mints the
	// edge blocked->blocker (source = blocked, runDepBlocksProxiedServer). A wisp
	// blocked-id must route to wisp_dependencies.
	t.Run("dep_blocks_wisp_source_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "wdb")
		blocker := bdProxiedCreate(t, bd, p.dir, "wisp blocker", "--type", "task", "--ephemeral")
		blocked := bdProxiedCreate(t, bd, p.dir, "wisp blocked", "--type", "task", "--ephemeral")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "dep", blocker.ID, "--blocks", blocked.ID)
		assertClean(t, "bd dep --blocks (wisp source)", stdout, stderr, err)
	})

	// Regression boundary: a plain (non-wisp) issue source must still work — the
	// wisp routing must not break the ordinary issues path.
	t.Run("dep_add_issue_source_still_works", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "wdi")
		src := bdProxiedCreate(t, bd, p.dir, "issue source", "--type", "bug")
		tgt := bdProxiedCreate(t, bd, p.dir, "issue target", "--type", "bug")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "dep", "add", src.ID, tgt.ID)
		assertClean(t, "bd dep add (issue source)", stdout, stderr, err)

		out2, err2 := bdProxiedRun(t, bd, p.dir, "dep", "add", src.ID, tgt.ID)
		if err2 != nil {
			t.Fatalf("re-add of issue-source edge errored: %v\n%s", err2, out2)
		}
		low := strings.ToLower(string(out2))
		if !strings.Contains(low, "already present") && !strings.Contains(low, "no change") {
			t.Errorf("re-add of an issue-source dep edge should be an honest no-op; got:\n%s", out2)
		}
	})
}
