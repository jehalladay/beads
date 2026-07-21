package debug

import (
	"path/filepath"
	"testing"
)

// beads-uwdua: teeth for the test-only upward-walk ceiling used by the
// .beads/workspace-discovery walks (internal/beads, internal/config, cmd/bd).

func TestWalkCeilingReached_UnsetIsNoOp(t *testing.T) {
	// No ceiling env → always false, so production discovery walks are unchanged.
	t.Setenv(WalkCeilingEnvVar, "")
	for _, dir := range []string{"/", "/fsx", "/fsx/ubuntu", "/fsx/ubuntu/tmp", "/tmp/x/y"} {
		if WalkCeilingReached(dir) {
			t.Errorf("WalkCeilingReached(%q) = true with unset ceiling, want false (must be a no-op in production)", dir)
		}
	}
}

func TestWalkCeilingReached_StopsAtAndAboveCeiling(t *testing.T) {
	ceiling := t.TempDir() // a real, existing dir so EvalSymlinks resolves it
	t.Setenv(WalkCeilingEnvVar, ceiling)

	// Descendants of the ceiling: keep walking (false).
	for _, sub := range []string{"child", "child/repo", "a/b/c"} {
		dir := filepath.Join(ceiling, sub)
		if WalkCeilingReached(dir) {
			t.Errorf("WalkCeilingReached(%q) = true, want false (strict descendant of ceiling must keep walking)", dir)
		}
	}

	// The ceiling itself: stop (true).
	if !WalkCeilingReached(ceiling) {
		t.Errorf("WalkCeilingReached(ceiling=%q) = false, want true (walk must stop at the ceiling)", ceiling)
	}

	// Strict ancestors of the ceiling: stop (true) — a walk climbing up must not
	// step above the ceiling into the host workspace.
	for dir := filepath.Dir(ceiling); ; dir = filepath.Dir(dir) {
		if !WalkCeilingReached(dir) {
			t.Errorf("WalkCeilingReached(ancestor=%q) = false, want true (walk must not climb above the ceiling)", dir)
		}
		if parent := filepath.Dir(dir); parent == dir {
			break
		}
	}
}

func TestWalkCeilingReached_UnrelatedSubtreeUnaffected(t *testing.T) {
	// A ceiling in one subtree must NOT halt a walk living in a DIFFERENT subtree
	// (regression: an early version stopped every path not under the ceiling,
	// breaking unrelated tests whose t.TempDir() lived elsewhere).
	base := t.TempDir()
	ceiling := filepath.Join(base, "ceilingtree")
	other := filepath.Join(base, "othertree", "repo")
	t.Setenv(WalkCeilingEnvVar, ceiling)

	if WalkCeilingReached(other) {
		t.Errorf("WalkCeilingReached(%q) = true, want false (unrelated subtree must keep walking; ceiling=%q)", other, ceiling)
	}
	// base is an ancestor of the ceiling, so it DOES stop — but othertree (a
	// sibling of ceilingtree) does not.
	sibling := filepath.Join(base, "othertree")
	if WalkCeilingReached(sibling) {
		t.Errorf("WalkCeilingReached(sibling=%q) = true, want false (sibling of ceiling is not an ancestor of it)", sibling)
	}
}
