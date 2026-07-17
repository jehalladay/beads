package remotecache

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDoltClone_CleansUpTargetOnFailure verifies the beads-ix3r fix: a failed
// clone must not leave a partial target/.dolt behind, because doltExists()
// would then trust it forever and every future Ensure() would take the warm
// (pull) path against a corrupt clone — permanently wedging the remote.
//
// We simulate the "partial clone left behind" precondition by pre-creating
// target/.dolt with garbage, which also forces `dolt clone` to fail fast
// (target already exists). After the fix, doltClone removes target on error,
// so the next Ensure re-clones cold.
func TestDoltClone_CleansUpTargetOnFailure(t *testing.T) {
	skipIfNoDolt(t)

	c := &Cache{Dir: t.TempDir(), FreshFor: 0}
	remoteURL := "https://dolthub.com/example/does-not-exist-ix3r"
	target := c.cloneTarget(remoteURL)

	// Pre-create a partial/garbage .dolt to simulate an interrupted cold clone.
	doltDir := filepath.Join(target, ".dolt")
	if err := os.MkdirAll(doltDir, 0o750); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(doltDir, "garbage"), []byte("junk"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if !c.doltExists(target) {
		t.Fatalf("setup: expected doltExists()==true for partial dir")
	}

	// doltClone must fail (target exists / bogus remote) AND clean up.
	err := c.doltClone(context.Background(), remoteURL, target)
	if err == nil {
		t.Fatalf("expected doltClone to fail for a partial/bogus target")
	}
	if c.doltExists(target) {
		t.Errorf("doltClone left a partial target/.dolt behind after failure; "+
			"doltExists()==true would permanently wedge the cache (target=%s)", target)
	}
}

// TestEnsure_ReclonesAfterInterruptedColdStart is the end-to-end guard: after a
// partial cold clone, the next Ensure must NOT permanently take the corrupt
// warm path. With the fix, the failed cold clone leaves nothing, so a later
// Ensure attempts a fresh cold clone rather than pulling a broken repo.
func TestEnsure_ReclonesAfterInterruptedColdStart(t *testing.T) {
	skipIfNoDolt(t)

	c := &Cache{Dir: t.TempDir(), FreshFor: 0}
	remoteURL := initDoltRemote(t, t.TempDir())

	// First: create a partial cold-start state by hand (as an interrupted
	// clone would), then confirm Ensure recovers rather than wedging.
	target := c.cloneTarget(remoteURL)
	doltDir := filepath.Join(target, ".dolt")
	if err := os.MkdirAll(doltDir, 0o750); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(doltDir, "garbage"), []byte("junk"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Ensure must succeed by self-healing (evict corrupt + re-clone cold),
	// not fail forever on the warm path.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := c.Ensure(ctx, remoteURL); err != nil {
		t.Fatalf("Ensure did not recover from a corrupt warm clone: %v", err)
	}
	if !c.doltExists(target) {
		t.Errorf("Ensure did not leave a valid clone after self-heal")
	}
}
