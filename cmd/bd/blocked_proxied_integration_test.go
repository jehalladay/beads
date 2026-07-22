//go:build cgo

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func bdProxiedBlockedJSON(t *testing.T, bd string, p proxiedProject, args ...string) []*types.BlockedIssue {
	t.Helper()
	fullArgs := append([]string{"blocked", "--json"}, args...)
	stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, fullArgs...)
	if err != nil {
		t.Fatalf("bd blocked --json %s failed: %v\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "), err, stdout, stderr)
	}
	s := strings.TrimSpace(stdout)
	start := strings.Index(s, "[")
	if start < 0 {
		t.Fatalf("no JSON array in blocked --json output: %s", stdout)
	}
	var out []*types.BlockedIssue
	if err := json.Unmarshal([]byte(s[start:]), &out); err != nil {
		t.Fatalf("parse blocked JSON: %v\n%s", err, s[start:])
	}
	return out
}

func bdProxiedBlockedCapture(t *testing.T, bd string, p proxiedProject, args ...string) (string, string) {
	t.Helper()
	fullArgs := append([]string{"blocked"}, args...)
	stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, fullArgs...)
	if err != nil {
		t.Fatalf("bd blocked %s failed: %v\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "), err, stdout, stderr)
	}
	return stdout, stderr
}

func TestProxiedServerBlocked(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("json_and_text_with_blockers", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "bk1")
		blocker := bdProxiedCreate(t, bd, p.dir, "The blocker")
		dependent := bdProxiedCreate(t, bd, p.dir, "I am blocked", "--deps", "depends-on:"+blocker.ID)

		got := bdProxiedBlockedJSON(t, bd, p)
		var entry *types.BlockedIssue
		for _, bi := range got {
			if bi.ID == dependent.ID {
				entry = bi
				break
			}
		}
		if entry == nil {
			out, _ := json.Marshal(got)
			t.Fatalf("expected %s in blocked listing, got: %s", dependent.ID, out)
		}
		if entry.BlockedByCount != 1 {
			t.Errorf("BlockedByCount = %d, want 1", entry.BlockedByCount)
		}
		if len(entry.BlockedBy) != 1 || entry.BlockedBy[0] != blocker.ID {
			t.Errorf("BlockedBy = %v, want [%s]", entry.BlockedBy, blocker.ID)
		}

		stdout, _ := bdProxiedBlockedCapture(t, bd, p)
		if !strings.Contains(stdout, "Blocked issues") {
			t.Errorf("expected heading in text output, got: %s", stdout)
		}
		if !strings.Contains(stdout, "Blocked by 1 open dependencies") {
			t.Errorf("expected blocker summary line, got: %s", stdout)
		}
	})

	t.Run("empty_case", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "bk2")
		bdProxiedCreate(t, bd, p.dir, "Nothing blocked here")

		out, err := bdProxiedRun(t, bd, p.dir, "blocked", "--json")
		if err != nil {
			t.Fatalf("bd blocked --json failed: %v\n%s", err, out)
		}
		var empty []*types.BlockedIssue
		if err := json.Unmarshal(bytes.TrimSpace(out), &empty); err != nil {
			t.Fatalf("parse empty blocked JSON: %v\n%s", err, out)
		}
		if len(empty) != 0 {
			t.Errorf("expected [] for no blockers, got %d entries", len(empty))
		}

		stdout, _ := bdProxiedBlockedCapture(t, bd, p)
		if !strings.Contains(stdout, "No blocked issues") {
			t.Errorf("expected 'No blocked issues' message, got: %s", stdout)
		}
	})

	t.Run("parent_filter_restricts_to_descendants", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "bk3")
		parent := bdProxiedCreate(t, bd, p.dir, "Parent epic", "--type", "epic")
		blocker := bdProxiedCreate(t, bd, p.dir, "Inside blocker")
		inside := bdProxiedCreate(t, bd, p.dir, "Inside blocked", "--parent", parent.ID, "--deps", "depends-on:"+blocker.ID)
		outsideBlocker := bdProxiedCreate(t, bd, p.dir, "Outside blocker")
		outside := bdProxiedCreate(t, bd, p.dir, "Outside blocked", "--deps", "depends-on:"+outsideBlocker.ID)

		got := bdProxiedBlockedJSON(t, bd, p, "--parent", parent.ID)
		ids := map[string]bool{}
		for _, bi := range got {
			ids[bi.ID] = true
		}
		if !ids[inside.ID] {
			t.Errorf("expected descendant %s in --parent result", inside.ID)
		}
		if ids[outside.ID] {
			t.Errorf("non-descendant %s leaked into --parent result", outside.ID)
		}
	})

	// beads-x5c76: bd blocked --assignee filters by assignee, at parity with bd
	// ready --assignee / bd list --assignee. Proxied twin of the direct-path
	// TestEmbeddedBlockedAssigneeFilter — exercises runBlockedProxiedServer's
	// flag read into filter.Assignee.
	t.Run("assignee_filter", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "bk5")
		blocker := bdProxiedCreate(t, bd, p.dir, "Shared blocker")
		alice := bdProxiedCreate(t, bd, p.dir, "Alice blocked", "--assignee", "alice", "--deps", "depends-on:"+blocker.ID)
		bob := bdProxiedCreate(t, bd, p.dir, "Bob blocked", "--assignee", "bob", "--deps", "depends-on:"+blocker.ID)

		got := bdProxiedBlockedJSON(t, bd, p, "--assignee", "alice")
		ids := map[string]bool{}
		for _, bi := range got {
			ids[bi.ID] = true
		}
		if !ids[alice.ID] {
			t.Errorf("expected alice's blocked %s in --assignee alice result", alice.ID)
		}
		if ids[bob.ID] {
			t.Errorf("bob's blocked %s leaked into --assignee alice result", bob.ID)
		}
	})

	// beads-wu0u: bd blocked --parent <nonexistent> must error (exit != 0) with a
	// parseable JSON error under --json — not silently return [] exit 0. Proxied
	// twin of the direct-path check (beads-d5jg) / bd list --parent (beads-n8lv):
	// a typo'd epic id should be a hard error, not "nothing blocked".
	t.Run("parent_nonexistent_errors", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "bk4")
		bdProxiedCreate(t, bd, p.dir, "Some issue")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "blocked", "--parent", "nope-not-a-real-id", "--json")
		if err == nil {
			t.Fatalf("bd blocked --parent <nonexistent> --json should fail; stdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		s := strings.TrimSpace(stdout)
		start := strings.IndexAny(s, "{")
		if start < 0 {
			t.Fatalf("expected a JSON error object on stdout, got stdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		var obj map[string]interface{}
		if jerr := json.Unmarshal([]byte(s[start:]), &obj); jerr != nil {
			t.Fatalf("stdout is not valid JSON (%v): %s", jerr, s[start:])
		}
		if _, ok := obj["error"]; !ok {
			t.Errorf("expected an {\"error\":...} object on stdout, got: %s", s[start:])
		}
		// The error references the parent id (a missing id surfaces either as
		// "parent issue '...' not found" or the store's lookup error — both name
		// the id and both are the hard-fail we require vs a silent empty result).
		if !strings.Contains(fmt.Sprint(obj["error"]), "nope-not-a-real-id") {
			t.Errorf("expected the bad parent id in the error, got: %v", obj["error"])
		}
	})
}
