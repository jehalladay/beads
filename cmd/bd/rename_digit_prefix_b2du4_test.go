//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"
)

// beads-b2du4: `bd rename`'s new-ID format guard (cmd/bd/rename.go) used a
// private regex `^[a-z]+-[a-zA-Z0-9._-]+$` whose PREFIX segment was
// letters-only, so ANY digit-containing prefix (jpij12x-, mz5x-, proj2-) was
// rejected — "invalid new ID format ... must be prefix-suffix" — even though
// `bd create`/ValidateIDFormat accept and the rest of bd operates on such IDs
// fine. That made every issue in a digit-in-prefix rig un-renamable (CLAUDE.md
// §4 lists live rigs jpij12x + mz5x). CLASS: VALIDATION-CHARSET-DIVERGENCE
// (create-accept/rename-reject), sibling of e8que.
//
// The fix delegates the format check to the canonical
// validation.ValidateIDFormat (no-whitespace + contains-hyphen, correct
// hyphenated/digit prefix extraction); the DB-prefix invariant stays enforced
// separately by beads-c3igh's ValidateIDPrefixAllowed. The format guard is a
// SHARED pre-dispatch line (runs before the usesProxiedServer branch), so a
// digit-prefix rename round-trip exercises the guard identically for both the
// direct and proxied paths.
//
// MUTATION-VERIFY: restore the old `^[a-z]+-...` regex → the digit-prefix
// rename below fails with "invalid new ID format", so this test goes RED.
func TestEmbeddedRenameDigitPrefix_b2du4(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// A digit-containing prefix like the live rigs jpij12x / mz5x / proj2.
	dir, _, _ := bdInit(t, bd, "--prefix", "proj2")
	issue := bdCreate(t, bd, dir, "digit-prefix rename target", "--type", "task")
	if !strings.HasPrefix(issue.ID, "proj2-") {
		t.Fatalf("setup: created issue id %q does not carry the digit prefix", issue.ID)
	}

	newID := "proj2-renamed"
	// Under the old letters-only regex this rename hard-fails; the fix lets it
	// through (the prefix invariant is satisfied — same prefix).
	out := bdRename(t, bd, dir, issue.ID, newID)
	_ = out

	// Round-trip: the renamed id must now resolve, and the old id must not.
	if got := bdShow(t, bd, dir, newID); got.ID != newID {
		t.Fatalf("after rename, show %q returned id %q", newID, got.ID)
	}

	// A digit-suffix-on-digit-prefix target must also be accepted (the whole
	// charset, not just the letters that happened to precede the fix).
	issue2 := bdCreate(t, bd, dir, "second target", "--type", "task")
	newID2 := "proj2-a1b2c3"
	bdRename(t, bd, dir, issue2.ID, newID2)
	if got := bdShow(t, bd, dir, newID2); got.ID != newID2 {
		t.Fatalf("after rename to digit-suffix id, show %q returned id %q", newID2, got.ID)
	}
}

// TestProxiedRenameDigitPrefix_b2du4 is the proxied twin. The format guard is a
// shared pre-dispatch line, so the same digit-prefix rename must succeed through
// runRenameProxiedServer — proving the fix is not direct-path-only.
func TestProxiedRenameDigitPrefix_b2du4(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	p := bdProxiedInit(t, bd, "mz5x")
	bdProxiedCreate(t, bd, p.dir, "digit-prefix proxied target", "--type", "task", "--id", "mz5x-src")

	if _, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "rename", "mz5x-src", "mz5x-dst"); err != nil {
		t.Fatalf("proxied rename of a digit-prefix id failed (b2du4): %v\n%s", err, stderr)
	}

	shown := bdProxiedShowRaw(t, bd, p.dir, "mz5x-dst")
	if !strings.Contains(shown, "mz5x-dst") {
		t.Errorf("proxied rename did not produce the renamed digit-prefix id mz5x-dst:\n%s [beads-b2du4]", shown)
	}
}
