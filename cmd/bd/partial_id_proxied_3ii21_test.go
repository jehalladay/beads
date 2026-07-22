//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerPartialID is the teeth for beads-3ii21: the proxied-server
// resolve helpers (proxiedGetIssueOrWisp / proxiedResolveIssueOrWisp) must
// resolve a bare hash / prefix-less / truncated partial id the same way the
// direct path does via utils.ResolvePartialID — and error on ambiguity. Before
// the fix they used only uw.IssueUseCase().GetIssue (strict WHERE id = ?), so
// `bd show <bare-hash>` failed "not found" for a hub-connected crew where the
// same arg resolves locally.
func TestProxiedServerPartialID(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// stripPrefix turns "pfx-abc123" into the bare hash "abc123".
	stripPrefix := func(id string) string {
		if i := strings.Index(id, "-"); i >= 0 {
			return id[i+1:]
		}
		return id
	}

	t.Run("show_by_bare_hash", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pid1")
		issue := bdProxiedCreate(t, bd, p.dir, "Bare hash issue", "--type", "task")
		bare := stripPrefix(issue.ID)
		if bare == issue.ID {
			t.Fatalf("expected a prefixed id, got %q", issue.ID)
		}

		out, err := bdProxiedRun(t, bd, p.dir, "show", bare)
		s := string(out)
		if err != nil {
			t.Fatalf("proxied show by bare hash %q failed (beads-3ii21): %v\n%s", bare, err, s)
		}
		if strings.Contains(s, "not found") || strings.Contains(s, "storage is nil") {
			t.Fatalf("proxied show by bare hash %q did not resolve (beads-3ii21): %s", bare, s)
		}
		if !strings.Contains(s, issue.ID) {
			t.Errorf("expected full id %q in output, got: %s", issue.ID, s)
		}
	})

	t.Run("close_by_bare_hash", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pid2")
		issue := bdProxiedCreate(t, bd, p.dir, "Closable by hash", "--type", "task")
		bare := stripPrefix(issue.ID)

		out, err := bdProxiedRun(t, bd, p.dir, "close", bare)
		s := string(out)
		if err != nil {
			t.Fatalf("proxied close by bare hash %q failed (beads-3ii21): %v\n%s", bare, err, s)
		}
		if strings.Contains(s, "not found") || strings.Contains(s, "storage is nil") {
			t.Fatalf("proxied close by bare hash %q did not resolve (beads-3ii21): %s", bare, s)
		}
	})

	t.Run("label_list_by_bare_hash", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pid3")
		issue := bdProxiedCreate(t, bd, p.dir, "Labeled by hash", "--type", "task")
		if _, err := bdProxiedRun(t, bd, p.dir, "label", "add", issue.ID, "backend"); err != nil {
			t.Fatalf("label add setup failed: %v", err)
		}
		bare := stripPrefix(issue.ID)

		out, err := bdProxiedRun(t, bd, p.dir, "label", "list", bare)
		s := string(out)
		if err != nil {
			t.Fatalf("proxied label list by bare hash %q failed (beads-3ii21): %v\n%s", bare, err, s)
		}
		if strings.Contains(s, "not found") || strings.Contains(s, "storage is nil") {
			t.Fatalf("proxied label list by bare hash %q did not resolve (beads-3ii21): %s", bare, s)
		}
		if !strings.Contains(s, "backend") {
			t.Errorf("expected 'backend' label, got: %s", s)
		}
	})
}
