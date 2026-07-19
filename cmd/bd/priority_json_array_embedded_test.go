//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestPriorityJSONArrayShape_utby is the end-to-end regression for beads-utby
// (a sibling of beads-yrtx): `bd priority <id> <n>` is a documented shorthand
// for `bd update --priority`, but its --json output emitted a bare DICT
// (`{...}`) while `bd update` (and the sibling mutation verbs) emit an ARRAY
// (`[{...}]`). A consumer following the shorthand docs breaks on the shape.
//
// This proves the ROUTING through the real command handler (the fix wraps the
// single *Issue in a []*Issue at priority.go and its proxied-server sibling),
// which a pure marshal test of a hand-built slice cannot.
func TestPriorityJSONArrayShape_utby(t *testing.T) {
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "pr")

	created := bdCreate(t, bd, dir, "priority shape target", "--type", "task")

	// Baseline: `bd update --priority` is the documented long form and emits an
	// ARRAY. Anchor the assertion to the actual house contract.
	updOut, err := bdRunWithFlockRetry(t, bd, dir, "update", created.ID, "--priority", "1", "--json")
	if err != nil {
		t.Fatalf("bd update --priority --json failed: %v\n%s", err, updOut)
	}
	if tok := priorityFirstJSONToken(t, updOut); tok != '[' {
		t.Fatalf("precondition: bd update --priority --json is not an array (got %q); house contract changed?\n%s", tok, updOut)
	}

	// `bd priority <id> <n> --json` must match: ARRAY, 1 element, the issue.
	prOut, err := bdRunWithFlockRetry(t, bd, dir, "priority", created.ID, "0", "--json")
	if err != nil {
		t.Fatalf("bd priority --json failed: %v\n%s", err, prOut)
	}
	arr := priorityDecodeIssueArray(t, prOut)
	if len(arr) != 1 || arr[0].ID != created.ID {
		t.Fatalf("bd priority --json = %d issues, want 1 (the target issue); shape/content wrong\n%s", len(arr), prOut)
	}
	if arr[0].Priority != 0 {
		t.Fatalf("bd priority --json priority = %d, want 0\n%s", arr[0].Priority, prOut)
	}
}

// priorityFirstJSONToken returns the first non-whitespace JSON structural byte
// ('[' or '{') in out, skipping any leading non-JSON lines (tips/warnings).
func priorityFirstJSONToken(t *testing.T, out []byte) byte {
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

// priorityDecodeIssueArray asserts out carries a JSON ARRAY of issues (fails
// loud on a bare object — the pre-utby shape) and returns it.
func priorityDecodeIssueArray(t *testing.T, out []byte) []priorityIssueShape {
	t.Helper()
	if tok := priorityFirstJSONToken(t, out); tok != '[' {
		t.Fatalf("bd priority --json emitted a %q (bare object), want an array `[{...}]` to match bd update:\n%s", tok, out)
	}
	s := string(out)
	start := strings.IndexByte(s, '[')
	var arr []priorityIssueShape
	dec := json.NewDecoder(strings.NewReader(s[start:]))
	if err := dec.Decode(&arr); err != nil {
		t.Fatalf("bd priority --json is not a decodable issue array: %v\n%s", err, out)
	}
	return arr
}

type priorityIssueShape struct {
	ID       string `json:"id"`
	Priority int    `json:"priority"`
}
