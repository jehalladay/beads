//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedWispSourceDuplicateSupersede_pm1kh is the PROXIED twin of
// beads-pega7 (whose direct/embedded fix lives in
// internal/storage/dolt/wisp_source_linkandclose_pega7_test.go).
//
// beads-pm1kh: runLinkAndCloseProxied (cmd/bd/duplicate_proxied_server.go —
// the hub-connected `bd duplicate`/`bd supersede` handler) resolved the source
// via proxiedResolveIssueOrWisp (which returns an isWisp bool) but DISCARDED
// the bool, then unconditionally routed AddDependency + CloseIssue to the
// permanent issues/dependencies tables. For a WISP source that means the edge
// INSERT ran `SELECT issue_type FROM issues WHERE id=<wisp>` -> ErrNoRows ->
// "issue <wisp> not found", even though the wisp exists. The embedded backend
// succeeded (pega7 fixed dolt.LinkAndClose to auto-detect) — a backend-
// asymmetric un-mirrored-guard hole in the pega7/slmql/i9bui/k5oqp/cjvxq family.
//
// The fix uses the isWisp result to route the guard-list, the edge write
// (AddWispDependency) and the close (CloseWisp) to the wisp-backed tables.
//
// MUTATION-VERIFY: discard the isWisp bool / force the non-wisp branch in
// runLinkAndCloseProxied and these subtests FAIL with "not found" on the
// wisp-source duplicate/supersede.
func TestProxiedWispSourceDuplicateSupersede_pm1kh(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// assertNoNotFoundPanic fails on the pega7/pm1kh symptom or any nil-store panic.
	assertClean := func(t *testing.T, what, stdout, stderr string, err error) {
		t.Helper()
		combined := stdout + stderr
		if strings.Contains(combined, "not found") {
			t.Fatalf("%s hit the pm1kh symptom ('<wisp> not found' — edge routed to the issues table for a wisp source):\nstdout:\n%s\nstderr:\n%s", what, stdout, stderr)
		}
		if strings.Contains(combined, "storage is nil") || strings.Contains(combined, "nil pointer dereference") || strings.Contains(combined, "panic:") {
			t.Fatalf("%s hit a nil-store panic in proxied mode:\nstdout:\n%s\nstderr:\n%s", what, stdout, stderr)
		}
		if err != nil {
			t.Fatalf("%s failed in proxied mode: %v\nstdout:\n%s\nstderr:\n%s", what, err, stdout, stderr)
		}
	}

	t.Run("wisp_source_duplicate_succeeds_and_closes", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "wpd")
		// Both source and canonical are wisps — bd duplicate resolves either via
		// the wisp fallback, matching the pega7 direct test.
		canon := bdProxiedCreate(t, bd, p.dir, "canonical wisp", "--type", "task", "--ephemeral")
		dup := bdProxiedCreate(t, bd, p.dir, "duplicate wisp", "--type", "task", "--ephemeral")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "duplicate", dup.ID, "--of", canon.ID)
		assertClean(t, "bd duplicate (wisp source)", stdout, stderr, err)

		// The wisp source must now be closed (the edge landed + close committed).
		showOut := bdProxiedShowRaw(t, bd, p.dir, dup.ID)
		if !strings.Contains(strings.ToLower(showOut), "closed") {
			t.Errorf("expected wisp duplicate %s closed after bd duplicate, show:\n%s", dup.ID, showOut)
		}
	})

	t.Run("wisp_source_duplicate_json_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "wpj")
		canon := bdProxiedCreate(t, bd, p.dir, "canonical wisp j", "--type", "task", "--ephemeral")
		dup := bdProxiedCreate(t, bd, p.dir, "duplicate wisp j", "--type", "task", "--ephemeral")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "duplicate", dup.ID, "--of", canon.ID, "--json")
		assertClean(t, "bd duplicate --json (wisp source)", stdout, stderr, err)
		if !strings.Contains(stdout, "\"duplicate\"") || !strings.Contains(stdout, "\"canonical\"") {
			t.Errorf("expected duplicate/canonical keys in --json output:\n%s", stdout)
		}
	})

	t.Run("wisp_source_supersede_succeeds_and_closes", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "wps")
		repl := bdProxiedCreate(t, bd, p.dir, "replacement wisp", "--type", "task", "--ephemeral")
		old := bdProxiedCreate(t, bd, p.dir, "old wisp", "--type", "task", "--ephemeral")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "supersede", old.ID, "--with", repl.ID)
		assertClean(t, "bd supersede (wisp source)", stdout, stderr, err)

		showOut := bdProxiedShowRaw(t, bd, p.dir, old.ID)
		if !strings.Contains(strings.ToLower(showOut), "closed") {
			t.Errorf("expected wisp %s closed after bd supersede, show:\n%s", old.ID, showOut)
		}
	})

	// Regression boundary: a plain (non-wisp) issue source must still work — the
	// isWisp routing must not break the ordinary path.
	t.Run("issue_source_duplicate_still_works", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "wpi")
		canon := bdProxiedCreate(t, bd, p.dir, "canonical issue", "--type", "bug")
		dup := bdProxiedCreate(t, bd, p.dir, "duplicate issue", "--type", "bug")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "duplicate", dup.ID, "--of", canon.ID)
		assertClean(t, "bd duplicate (issue source)", stdout, stderr, err)
		showOut := bdProxiedShowRaw(t, bd, p.dir, dup.ID)
		if !strings.Contains(strings.ToLower(showOut), "closed") {
			t.Errorf("expected issue duplicate %s closed, show:\n%s", dup.ID, showOut)
		}
	})
}
