//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedWispSourceLink_l4hil is the PROXIED `bd link` twin of the
// proxied dep-add wisp-source fix (beads-zdg7x).
//
// beads-l4hil: `bd link` is shorthand for `bd dep add`. Its proxied handler
// (runLinkProxiedServer, link_proxied_server.go) unconditionally called
// AddDependencies (useWisp=false). The domain dependency use case selects the
// dependency table from that explicit flag (depRepo.Insert -> pickDepTable),
// UNLIKE the direct store's AddDependencyInTx which auto-detects the wisp table
// via IsActiveWispInTx (and the direct link path's wisp-aware GetIssue
// fallback in GetIssueInTxSplit). So for a WISP source the edge INSERT
// validated `SELECT issue_type FROM issues WHERE id=<wisp>` -> ErrNoRows ->
// "issue <wisp> not found", even though the direct/embedded `bd link` worked.
//
// The fix detects the source kind via proxiedDepSourceIsWisp (reused from
// zdg7x) and routes the edge write through proxiedAddDepEdges
// (AddWispDependencies vs AddDependencies).
//
// MUTATION-VERIFY: hardcode srcIsWisp=false in runLinkProxiedServer and the
// wisp-source subtests FAIL with "not found"; the issue-source control stays
// GREEN.
func TestProxiedWispSourceLink_l4hil(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// assertClean fails on the l4hil symptom or any nil-store panic.
	assertClean := func(t *testing.T, what, stdout, stderr string, err error) {
		t.Helper()
		combined := stdout + stderr
		if strings.Contains(combined, "not found") {
			t.Fatalf("%s hit the l4hil symptom ('<wisp> not found' — link edge routed to the issues table for a wisp source):\nstdout:\n%s\nstderr:\n%s", what, stdout, stderr)
		}
		if strings.Contains(combined, "storage is nil") || strings.Contains(combined, "nil pointer dereference") || strings.Contains(combined, "panic:") {
			t.Fatalf("%s hit a nil-store panic in proxied mode:\nstdout:\n%s\nstderr:\n%s", what, stdout, stderr)
		}
		if err != nil {
			t.Fatalf("%s failed in proxied mode: %v\nstdout:\n%s\nstderr:\n%s", what, err, stdout, stderr)
		}
	}

	// bd link <wisp-source> <target>: the source is a wisp, so the edge must
	// land in wisp_dependencies. Without the routing fix this INSERTs against
	// the issues table -> "issue <wisp> not found".
	t.Run("link_wisp_source_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "wla")
		src := bdProxiedCreate(t, bd, p.dir, "wisp source", "--type", "task", "--ephemeral")
		tgt := bdProxiedCreate(t, bd, p.dir, "wisp target", "--type", "task", "--ephemeral")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "link", src.ID, tgt.ID)
		assertClean(t, "bd link (wisp source)", stdout, stderr, err)
		if !strings.Contains(stdout, "Linked") {
			t.Errorf("expected 'Linked' confirmation for a wisp-source link; got:\n%s", stdout)
		}
	})

	// --json parity for the wisp-source link.
	t.Run("link_wisp_source_json_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "wlj")
		src := bdProxiedCreate(t, bd, p.dir, "wisp source j", "--type", "task", "--ephemeral")
		tgt := bdProxiedCreate(t, bd, p.dir, "wisp target j", "--type", "task", "--ephemeral")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "link", src.ID, tgt.ID, "--json")
		assertClean(t, "bd link --json (wisp source)", stdout, stderr, err)
		if !strings.Contains(stdout, "\"added\"") {
			t.Errorf("expected status:added in --json output for a wisp-source link:\n%s", stdout)
		}
	})

	// A non-blocks link type from a wisp source must also route correctly
	// (--type is the differentiator between `link` and the plain `dep add`
	// default). "related" mints a wisp_dependencies edge just the same.
	t.Run("link_wisp_source_typed_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "wlt")
		src := bdProxiedCreate(t, bd, p.dir, "wisp source t", "--type", "task", "--ephemeral")
		tgt := bdProxiedCreate(t, bd, p.dir, "wisp target t", "--type", "task", "--ephemeral")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "link", src.ID, tgt.ID, "--type", "related")
		assertClean(t, "bd link --type related (wisp source)", stdout, stderr, err)
	})

	// Regression boundary: a plain (non-wisp) issue source must still work — the
	// wisp routing must not break the ordinary issues path.
	t.Run("link_issue_source_still_works", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "wli")
		src := bdProxiedCreate(t, bd, p.dir, "issue source", "--type", "bug")
		tgt := bdProxiedCreate(t, bd, p.dir, "issue target", "--type", "bug")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "link", src.ID, tgt.ID)
		assertClean(t, "bd link (issue source)", stdout, stderr, err)
		if !strings.Contains(stdout, "Linked") {
			t.Errorf("expected 'Linked' confirmation for an issue-source link; got:\n%s", stdout)
		}
	})
}
