//go:build cgo

package main

import (
	"strings"
	"testing"
)

// beads-45xb: `bd note` was not proxied-server-aware — it used the nil global
// `store` in proxiedServerMode and died with "storage is nil" for every
// hub-connected crew. These tests exercise the proxied path (append succeeds,
// second append accumulates with a newline, --json returns the issue, and a
// ghost id fails cleanly rather than with a nil-store error).
func TestProxiedServerNote(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("append_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "npa")
		issue := bdProxiedCreate(t, bd, p.dir, "Note target", "--type", "task")
		out, err := bdProxiedRun(t, bd, p.dir, "note", issue.ID, "first note")
		if err != nil {
			t.Fatalf("bd note failed in proxied mode: %v\n%s", err, out)
		}
		if s := string(out); !strings.Contains(s, "Note added") {
			t.Errorf("expected 'Note added' confirmation, got: %s", s)
		}
		got := bdProxiedShow(t, bd, p.dir, issue.ID)
		if !strings.Contains(got.Notes, "first note") {
			t.Errorf("note not persisted via proxied path; Notes=%q", got.Notes)
		}
	})

	t.Run("second_append_accumulates", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "npb")
		issue := bdProxiedCreate(t, bd, p.dir, "Note accum", "--type", "task")
		if _, err := bdProxiedRun(t, bd, p.dir, "note", issue.ID, "line one"); err != nil {
			t.Fatalf("first note failed: %v", err)
		}
		if _, err := bdProxiedRun(t, bd, p.dir, "note", issue.ID, "line two"); err != nil {
			t.Fatalf("second note failed: %v", err)
		}
		got := bdProxiedShow(t, bd, p.dir, issue.ID)
		if !strings.Contains(got.Notes, "line one") || !strings.Contains(got.Notes, "line two") {
			t.Errorf("both notes should accumulate; Notes=%q", got.Notes)
		}
		if !strings.Contains(got.Notes, "line one\nline two") {
			t.Errorf("expected newline-separated append; Notes=%q", got.Notes)
		}
	})

	t.Run("json_returns_issue", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "npj")
		issue := bdProxiedCreate(t, bd, p.dir, "Note json", "--type", "task")
		out, err := bdProxiedRun(t, bd, p.dir, "note", issue.ID, "jsonnote", "--json")
		if err != nil {
			t.Fatalf("bd note --json failed: %v\n%s", err, out)
		}
		got := parseIssueJSON(t, out)
		if got.ID != issue.ID {
			t.Errorf("expected issue %s in --json, got %s", issue.ID, got.ID)
		}
		if !strings.Contains(got.Notes, "jsonnote") {
			t.Errorf("expected note in --json Notes, got %q", got.Notes)
		}
	})

	t.Run("nonexistent_id_fails_cleanly", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "npn")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "note", "npn-nonexistent999", "orphan note")
		if err == nil {
			t.Fatalf("expected bd note on a ghost id to fail; stdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		combined := stdout + stderr
		// The important contract: it fails via the proxied resolution path, NOT
		// with the nil-store "storage is nil" error the direct path would have
		// produced in proxied mode (beads-45xb). The proxied GetIssue reports the
		// missing id as a resolution error ("no rows"/"not found").
		if strings.Contains(combined, "storage is nil") {
			t.Errorf("proxied note leaked the nil-store failure instead of a clean resolution error: %s", combined)
		}
		if !strings.Contains(combined, "resolving") && !strings.Contains(combined, "not found") {
			t.Errorf("expected a clean resolution error, got: %s", combined)
		}
	})
}
