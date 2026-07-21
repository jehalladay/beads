//go:build cgo

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// beads-pwtvp (create-input-parity class; graph-seam sibling of the label
// inheritance beads-l8qsn and the assignee normalize beads-7i4m/llzt): single
// `bd create` trims the title then empty-checks it (create.go: "title =
// strings.TrimSpace(title); if title==\"\"") — so a padded "  x  " is stored
// trimmed and a whitespace-only "   " is rejected. `bd create --graph`
// validated only `node.Title == ""` (no trim) and stored node.Title RAW at both
// mint seams (direct graph_apply.go, proxied materializeGraphNodeIssue), so it
// ACCEPTED a whitespace-only title and stored padded titles unmatchable.
//
// FIX: trim + write-back in the shared validateGraphApplyPlan node loop before
// the empty-check; both mint seams read plan.Nodes after validate, so one
// write-back covers direct AND proxied.
//
// End-to-end through the ACTUAL `bd create --graph` subprocess. bdShow hydrates
// Issue.Title. MUTATION-VERIFIED: reverting the trim/write-back leaves padded
// titles stored raw (padded_title_stored_trimmed RED) and accepts a
// whitespace-only title (whitespace_only_title_rejected RED).
func TestEmbeddedGraphTitleTrim_pwtvp(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	writePlan := func(t *testing.T, dir, name, body string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write plan %s: %v", name, err)
		}
		return p
	}

	// 1. a padded title is stored TRIMMED, matching single `bd create` (which
	//    does title = strings.TrimSpace(title)).
	t.Run("padded_title_stored_trimmed", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "gt")
		plan := `{"nodes":[{"key":"c","title":"  padded title  ","type":"task"}],"edges":[]}`
		res := bdCreateGraph(t, bd, dir, writePlan(t, dir, "p1.json", plan))
		id := res.IDs["c"]
		if id == "" {
			t.Fatalf("no node id in result: %v", res.IDs)
		}
		got := bdShow(t, bd, dir, id)
		if got.Title != "padded title" {
			t.Errorf("expected trimmed title %q, got %q", "padded title", got.Title)
		}
	})

	// 2. a whitespace-only title is REJECTED (non-zero exit, "empty title"),
	//    matching single create's "title cannot be empty" — the pre-fix bug
	//    accepted it because "   " != "".
	t.Run("whitespace_only_title_rejected", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "gw")
		plan := `{"nodes":[{"key":"c","title":"   ","type":"task"}],"edges":[]}`
		planFile := writePlan(t, dir, "p2.json", plan)

		cmd := exec.Command(bd, "create", "--json", "--graph", planFile)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err == nil {
			t.Fatalf("`bd create --graph` with a whitespace-only title unexpectedly succeeded\nstdout:\n%s", stdout.String())
		}
		combined := stdout.String() + stderr.String()
		if !strings.Contains(combined, "empty title") {
			t.Errorf("expected an 'empty title' rejection, got:\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
		}
	})

	// 3. negative/regression: a normal (already-clean) title is unaffected —
	//    stored verbatim, no over-trimming of internal spaces.
	t.Run("clean_title_unaffected", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "gc")
		plan := `{"nodes":[{"key":"c","title":"clean multi word title","type":"task"}],"edges":[]}`
		res := bdCreateGraph(t, bd, dir, writePlan(t, dir, "p3.json", plan))
		id := res.IDs["c"]
		if id == "" {
			t.Fatalf("no node id in result: %v", res.IDs)
		}
		got := bdShow(t, bd, dir, id)
		if got.Title != "clean multi word title" {
			t.Errorf("clean title altered: got %q", got.Title)
		}
	})
}
