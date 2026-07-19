//go:build cgo

package main

import (
	"os"
	"testing"
)

// beads-5m56 (yrtx follow-on): `bd assign <id> <same-assignee> --json` on the
// NO-OP branch ("already assigned, no change") must ALSO emit an ARRAY, matching
// the real-assign path (yrtx) and `bd update --assignee`. It previously emitted
// a bare DICT, so a --json consumer got a different shape depending on whether
// the reassign happened to be a no-op.
func TestAssignNoOpJSONArrayShape_5m56(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "an")

	created := bdCreate(t, bd, dir, "noop shape target", "--type", "task")

	// First assign (real change) — establishes the assignee; ARRAY per yrtx.
	first, err := bdRunWithFlockRetry(t, bd, dir, "assign", created.ID, "alice", "--json")
	if err != nil {
		t.Fatalf("bd assign (real) --json failed: %v\n%s", err, first)
	}
	firstArr := decodeIssueArray(t, "bd assign (real)", first)
	if len(firstArr) != 1 || firstArr[0].Assignee != "alice" {
		t.Fatalf("real assign --json shape/content wrong: %s", first)
	}

	// Re-assign to the SAME assignee — the no-op branch. Must STILL be an ARRAY.
	noop, err := bdRunWithFlockRetry(t, bd, dir, "assign", created.ID, "alice", "--json")
	if err != nil {
		t.Fatalf("bd assign (no-op) --json failed: %v\n%s", err, noop)
	}
	if tok := firstJSONToken(t, noop); tok != '[' {
		t.Fatalf("bd assign no-op --json emitted a %q (bare object), want an array to match the real-assign path + bd update (beads-5m56):\n%s", tok, noop)
	}
	noopArr := decodeIssueArray(t, "bd assign (no-op)", noop)
	if len(noopArr) != 1 || noopArr[0].ID != created.ID || noopArr[0].Assignee != "alice" {
		t.Fatalf("no-op assign --json shape/content wrong: %s", noop)
	}
}
