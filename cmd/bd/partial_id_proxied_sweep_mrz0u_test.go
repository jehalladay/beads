//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerPartialIDSweep_mrz0u is the teeth for beads-mrz0u, the
// sibling sweep of beads-3ii21. The direct paths of note/undefer/history/
// comments-add/comments-list/relate/unrelate resolve partial/bare-hash IDs
// (via utils.ResolvePartialID), but their proxied-server twins called
// GetIssue/GetWisp with the RAW arg → a hub-connected (proxied, store==nil)
// crew got "not found" on a bare hash. The fix routes each through the shared
// proxiedGetIssueOrWisp / proxiedResolvePartialID helper (beads-3ii21) and
// rebinds id to the canonical ID before downstream exact-ID ops.
//
// Gate (gate_proxied_server.go) is intentionally EXCLUDED: the direct gate
// path (gate.go) also uses raw store.GetIssue with NO ResolvePartialID, so the
// proxied gate is already at parity — adding resolution there would create a
// divergence in the opposite direction.
//
// MUTATION-VERIFIED: reverting a handler's proxiedGetIssueOrWisp/
// proxiedResolvePartialID call back to a raw GetIssue turns the matching
// sub-test RED.
func TestProxiedServerPartialIDSweep_mrz0u(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// stripPrefix turns "pfx-abc123" into the bare hash "abc123".
	stripPrefix := func(id string) string {
		if i := strings.Index(id, "-"); i >= 0 {
			return id[i+1:]
		}
		return id
	}

	// resolvedOK asserts the command succeeded and did not report an unresolved
	// bare hash.
	resolvedOK := func(t *testing.T, verb, bare string, out []byte, err error) {
		t.Helper()
		s := string(out)
		if err != nil {
			t.Fatalf("proxied %s by bare hash %q failed (beads-mrz0u): %v\n%s", verb, bare, err, s)
		}
		if strings.Contains(s, "not found") || strings.Contains(s, "storage is nil") {
			t.Fatalf("proxied %s by bare hash %q did not resolve (beads-mrz0u): %s", verb, bare, s)
		}
	}

	t.Run("note_by_bare_hash", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "mz1")
		issue := bdProxiedCreate(t, bd, p.dir, "Note by hash", "--type", "task")
		bare := stripPrefix(issue.ID)
		out, err := bdProxiedRun(t, bd, p.dir, "note", bare, "a note body")
		resolvedOK(t, "note", bare, out, err)
	})

	t.Run("undefer_by_bare_hash", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "mz2")
		issue := bdProxiedCreate(t, bd, p.dir, "Undefer by hash", "--type", "task")
		if _, err := bdProxiedRun(t, bd, p.dir, "defer", issue.ID, "--until", "2099-01-01"); err != nil {
			t.Fatalf("defer setup failed: %v", err)
		}
		bare := stripPrefix(issue.ID)
		out, err := bdProxiedRun(t, bd, p.dir, "undefer", bare)
		resolvedOK(t, "undefer", bare, out, err)
	})

	t.Run("history_by_bare_hash", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "mz3")
		issue := bdProxiedCreate(t, bd, p.dir, "History by hash", "--type", "task")
		bare := stripPrefix(issue.ID)
		out, err := bdProxiedRun(t, bd, p.dir, "history", bare)
		resolvedOK(t, "history", bare, out, err)
	})

	t.Run("comments_add_by_bare_hash", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "mz4")
		issue := bdProxiedCreate(t, bd, p.dir, "Comments add by hash", "--type", "task")
		bare := stripPrefix(issue.ID)
		out, err := bdProxiedRun(t, bd, p.dir, "comments", "add", bare, "hello")
		resolvedOK(t, "comments add", bare, out, err)
	})

	t.Run("comments_list_by_bare_hash", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "mz5")
		issue := bdProxiedCreate(t, bd, p.dir, "Comments list by hash", "--type", "task")
		if _, err := bdProxiedRun(t, bd, p.dir, "comments", "add", issue.ID, "seed comment"); err != nil {
			t.Fatalf("comment add setup failed: %v", err)
		}
		bare := stripPrefix(issue.ID)
		out, err := bdProxiedRun(t, bd, p.dir, "comments", bare)
		resolvedOK(t, "comments list", bare, out, err)
		if !strings.Contains(string(out), "seed comment") {
			t.Errorf("expected seeded comment in output, got: %s", string(out))
		}
	})

	t.Run("relate_by_bare_hash", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "mz6")
		a := bdProxiedCreate(t, bd, p.dir, "Relate A", "--type", "task")
		b := bdProxiedCreate(t, bd, p.dir, "Relate B", "--type", "task")
		bareA := stripPrefix(a.ID)
		bareB := stripPrefix(b.ID)
		out, err := bdProxiedRun(t, bd, p.dir, "dep", "relate", bareA, bareB)
		resolvedOK(t, "dep relate", bareA+"/"+bareB, out, err)

		// And unrelate the same pair by bare hash.
		out, err = bdProxiedRun(t, bd, p.dir, "dep", "unrelate", bareA, bareB)
		resolvedOK(t, "dep unrelate", bareA+"/"+bareB, out, err)
	})
}
