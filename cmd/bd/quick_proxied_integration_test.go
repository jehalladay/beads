//go:build cgo

package main

import (
	"strings"
	"testing"
)

// beads-eh0z: `bd q`/quick was not proxied-server-aware — it used the nil
// global `store` in proxiedServerMode and died with "storage is nil" for every
// hub-connected crew. These tests exercise the proxied path: quick prints a new
// id, the issue actually exists (with title/priority/type/labels applied), and
// the run does not leak the nil-store failure.
func TestProxiedServerQuick(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("creates_issue_via_proxied_path", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "qkc")
		out, err := bdProxiedRun(t, bd, p.dir, "q", "Quick captured task")
		if err != nil {
			t.Fatalf("bd q failed in proxied mode: %v\n%s", err, out)
		}
		id := strings.TrimSpace(string(out))
		if id == "" || strings.Contains(id, "storage is nil") {
			t.Fatalf("expected a new issue id on stdout, got: %q", id)
		}
		got := bdProxiedShow(t, bd, p.dir, id)
		if got.Title != "Quick captured task" {
			t.Errorf("created issue title = %q, want %q", got.Title, "Quick captured task")
		}
	})

	t.Run("applies_priority_type_labels", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "qkf")
		out, err := bdProxiedRun(t, bd, p.dir, "q", "Flagged quick", "-p", "1", "-t", "bug", "-l", "urgent")
		if err != nil {
			t.Fatalf("bd q with flags failed: %v\n%s", err, out)
		}
		id := strings.TrimSpace(string(out))
		got := bdProxiedShow(t, bd, p.dir, id)
		if got.Priority != 1 {
			t.Errorf("priority = %d, want 1", got.Priority)
		}
		if string(got.IssueType) != "bug" {
			t.Errorf("type = %q, want bug", got.IssueType)
		}
		foundLabel := false
		for _, l := range got.Labels {
			if l == "urgent" {
				foundLabel = true
			}
		}
		if !foundLabel {
			t.Errorf("expected label 'urgent' on quick-created issue, got %v", got.Labels)
		}
	})
}
