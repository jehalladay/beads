//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestNoteJSONArrayShape_bjyq is the end-to-end regression for beads-bjyq (a
// sibling of beads-yrtx/beads-utby): `bd note <id> <text>` is a single-issue
// mutation verb that returns the modified issue under --json, but emitted a
// bare DICT (`{...}`) while `bd update` and the sibling mutation verbs
// (close/reopen/assign/tag/priority) emit a one-element ARRAY (`[{...}]`). A
// consumer following the house contract breaks on the shape.
//
// This proves the ROUTING through the real command handler (the fix wraps the
// single *Issue in a []*Issue at note.go and its proxied-server sibling).
func TestNoteJSONArrayShape_bjyq(t *testing.T) {
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "nt")

	created := bdCreate(t, bd, dir, "note shape target", "--type", "task")

	// Baseline: `bd update` (the array-emitting long form) — capture its shape
	// so the assertion is anchored to the actual house contract.
	updOut, err := bdRunWithFlockRetry(t, bd, dir, "update", created.ID, "--assignee", "carol", "--json")
	if err != nil {
		t.Fatalf("bd update --json failed: %v\n%s", err, updOut)
	}
	if tok := noteFirstJSONToken(t, updOut); tok != '[' {
		t.Fatalf("precondition: bd update --json is not an array (got %q); house contract changed?\n%s", tok, updOut)
	}

	// `bd note <id> <text> --json` must match: ARRAY, 1 element, the issue.
	noteOut, err := bdRunWithFlockRetry(t, bd, dir, "note", created.ID, "a note line", "--json")
	if err != nil {
		t.Fatalf("bd note --json failed: %v\n%s", err, noteOut)
	}
	arr := noteDecodeIssueArray(t, noteOut)
	if len(arr) != 1 || arr[0].ID != created.ID {
		t.Fatalf("bd note --json = %d issues, want 1 (the noted issue); shape/content wrong\n%s", len(arr), noteOut)
	}
	if !strings.Contains(arr[0].Notes, "a note line") {
		t.Fatalf("bd note --json did not carry the appended note; Notes=%q\n%s", arr[0].Notes, noteOut)
	}
}

// noteFirstJSONToken returns the first non-whitespace JSON structural byte
// ('[' or '{') in out, skipping any leading non-JSON lines (tips/warnings).
func noteFirstJSONToken(t *testing.T, out []byte) byte {
	t.Helper()
	s := string(out)
	ai := strings.IndexByte(s, '[')
	oi := strings.IndexByte(s, '{')
	switch {
	case ai < 0 && oi < 0:
		t.Fatalf("no JSON payload found in output:\n%s", s)
		return 0
	case ai < 0:
		return '{'
	case oi < 0:
		return '['
	case ai < oi:
		return '['
	default:
		return '{'
	}
}

// noteDecodeIssueArray asserts out carries a JSON ARRAY of issues (fails loud on
// a bare object — the pre-bjyq shape) and returns it.
func noteDecodeIssueArray(t *testing.T, out []byte) []noteIssueShape {
	t.Helper()
	if tok := noteFirstJSONToken(t, out); tok != '[' {
		t.Fatalf("bd note --json emitted a %q (bare object), want an array `[{...}]` to match bd update:\n%s", tok, out)
	}
	s := string(out)
	start := strings.IndexByte(s, '[')
	var arr []noteIssueShape
	dec := json.NewDecoder(strings.NewReader(s[start:]))
	if err := dec.Decode(&arr); err != nil {
		t.Fatalf("bd note --json is not a decodable issue array: %v\n%s", err, out)
	}
	return arr
}

type noteIssueShape struct {
	ID    string `json:"id"`
	Notes string `json:"notes"`
}
