//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedDepTreeStatusValidation is the teeth for beads-5gwaj: the proxied
// `bd dep tree --status` handler (runDepTreeProxiedServer) must validate +
// Normalize the value like the direct path (dep.go, beads-p330), instead of a
// raw types.Status(statusFilter) cast into filterTreeByStatus.
//
// Two divergences the raw cast caused on the hub-connected path:
//   LEG 1 (silent-accept typo): --status opne returned an empty tree, exit 0 —
//     no "invalid status" error, the silent-accept gap the enum-value-reject
//     family closed elsewhere. filterTreeByStatus does node.Status == status,
//     so a typo matches nothing and the tree silently empties.
//   LEG 2 (no case-normalize): --status OPEN dropped open nodes because "OPEN"
//     != "open" without Normalize; the direct path Normalizes first.
func TestProxiedDepTreeStatusValidation(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// A simple tree: b depends on a (a blocks b). Both open. `dep tree a` shows
	// its dependent b under --reverse; `dep tree b` shows a (its blocker) down.
	p := bdProxiedInit(t, bd, "dts")
	a := bdProxiedCreate(t, bd, p.dir, "Root A", "--type", "task")
	b := bdProxiedCreate(t, bd, p.dir, "Child B", "--type", "task")
	bdProxiedDep(t, bd, p.dir, "add", b.ID, a.ID) // b depends on a

	// Control: an unfiltered tree of b shows its blocker a.
	t.Run("control_unfiltered_shows_node", func(t *testing.T) {
		out := bdProxiedDep(t, bd, p.dir, "tree", b.ID)
		if !strings.Contains(out, a.ID) {
			t.Fatalf("setup: proxied `dep tree %s` should show blocker %s:\n%s", b.ID, a.ID, out)
		}
	})

	// Control: a valid lowercase --status open keeps the open blocker.
	t.Run("valid_lowercase_status_matches", func(t *testing.T) {
		out := bdProxiedDep(t, bd, p.dir, "tree", b.ID, "--status", "open")
		if !strings.Contains(out, a.ID) {
			t.Errorf("proxied `dep tree %s --status open` should keep the open node %s:\n%s", b.ID, a.ID, out)
		}
	})

	// LEG 1: an invalid --status must be REJECTED (rc!=0, "invalid status"),
	// not silently return an empty tree.
	t.Run("invalid_status_rejected", func(t *testing.T) {
		out := bdProxiedDepFail(t, bd, p.dir, "tree", b.ID, "--status", "opne")
		if !strings.Contains(strings.ToLower(out), "invalid status") {
			t.Errorf("proxied `dep tree %s --status opne` must error 'invalid status' (beads-5gwaj LEG 1: silently returned empty tree exit 0 before); got:\n%s", b.ID, out)
		}
	})

	// LEG 2: an uppercase --status must Normalize and MATCH the open node, not
	// drop it on a case mismatch.
	t.Run("uppercase_status_normalizes_and_matches", func(t *testing.T) {
		out := bdProxiedDep(t, bd, p.dir, "tree", b.ID, "--status", "OPEN")
		if !strings.Contains(out, a.ID) {
			t.Errorf("proxied `dep tree %s --status OPEN` must Normalize to 'open' and keep node %s (beads-5gwaj LEG 2: dropped it on case mismatch before); got:\n%s", b.ID, a.ID, out)
		}
	})
}
