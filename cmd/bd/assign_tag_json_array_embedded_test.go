//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestAssignTagJSONArrayShape_yrtx is the end-to-end regression for beads-yrtx:
// `bd assign` and `bd tag` are documented shorthands for `bd update --assignee`
// and `bd update --add-label`, but their --json output emitted a bare DICT
// (`{...}`) while `bd update` (and the sibling mutation verbs close/reopen) emit
// an ARRAY (`[{...}]`). A consumer following the shorthand docs breaks on the
// shape divergence — one needs `[0]`, the other doesn't.
//
// This proves the ROUTING through the real command handlers (the fix wraps the
// single *Issue in a []*Issue at assign.go/tag.go and their proxied-server
// siblings), which a pure marshal test of a hand-built slice cannot: it fails on
// the pre-yrtx code where those sites passed a bare *Issue to outputJSON.
func TestAssignTagJSONArrayShape_yrtx(t *testing.T) {
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "yr")

	created := bdCreate(t, bd, dir, "shape target", "--type", "task")

	// Baseline: `bd update --assignee` is the documented long form and emits an
	// ARRAY. Capture its shape so the assertion is anchored to the actual
	// house contract, not a hard-coded expectation.
	updOut, err := bdRunWithFlockRetry(t, bd, dir, "update", created.ID, "--assignee", "carol", "--json")
	if err != nil {
		t.Fatalf("bd update --assignee --json failed: %v\n%s", err, updOut)
	}
	if tok := firstJSONToken(t, updOut); tok != '[' {
		t.Fatalf("precondition: bd update --assignee --json is not an array (got %q); house contract changed?\n%s", tok, updOut)
	}

	// `bd assign <id> <assignee> --json` must match: ARRAY, 1 element, the issue.
	asgOut, err := bdRunWithFlockRetry(t, bd, dir, "assign", created.ID, "alice", "--json")
	if err != nil {
		t.Fatalf("bd assign --json failed: %v\n%s", err, asgOut)
	}
	assignArr := decodeIssueArray(t, "bd assign", asgOut)
	if len(assignArr) != 1 || assignArr[0].ID != created.ID {
		t.Fatalf("bd assign --json = %d issues, want 1 (the assigned issue); shape/content wrong\n%s", len(assignArr), asgOut)
	}
	if assignArr[0].Assignee != "alice" {
		t.Fatalf("bd assign --json assignee = %q, want alice\n%s", assignArr[0].Assignee, asgOut)
	}

	// `bd tag <id> <label> --json` must likewise be an ARRAY.
	tagOut, err := bdRunWithFlockRetry(t, bd, dir, "tag", created.ID, "urgent", "--json")
	if err != nil {
		t.Fatalf("bd tag --json failed: %v\n%s", err, tagOut)
	}
	tagArr := decodeIssueArray(t, "bd tag", tagOut)
	if len(tagArr) != 1 || tagArr[0].ID != created.ID {
		t.Fatalf("bd tag --json = %d issues, want 1 (the tagged issue); shape/content wrong\n%s", len(tagArr), tagOut)
	}
}

// firstJSONToken returns the first non-whitespace byte of the JSON payload in
// out (the raw '[' or '{' that determines array-vs-object shape), skipping any
// leading non-JSON lines (tips/warnings).
func firstJSONToken(t *testing.T, out []byte) byte {
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

// decodeIssueArray asserts out carries a JSON ARRAY of issues (fails loud on a
// bare object — the pre-yrtx shape) and returns it.
func decodeIssueArray(t *testing.T, label string, out []byte) []issueShape {
	t.Helper()
	if tok := firstJSONToken(t, out); tok != '[' {
		t.Fatalf("%s --json emitted a %q (bare object), want an array `[{...}]` to match bd update:\n%s", label, tok, out)
	}
	s := string(out)
	start := strings.IndexByte(s, '[')
	var arr []issueShape
	dec := json.NewDecoder(strings.NewReader(s[start:]))
	if err := dec.Decode(&arr); err != nil {
		t.Fatalf("%s --json is not a decodable issue array: %v\n%s", label, err, out)
	}
	return arr
}

// issueShape is the minimal projection needed to verify identity/content.
type issueShape struct {
	ID       string `json:"id"`
	Assignee string `json:"assignee"`
}
