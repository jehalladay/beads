package dolt

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestDoltExists_TrustsPartialClone documents the trust flaw that makes the
// partial-clone wedge possible (beads-pf1j): doltExists returns true for ANY
// subdirectory that merely contains a ".dolt" directory — it does not verify
// the clone completed. So a partial/interrupted clone (a target dir with an
// incomplete .dolt) is trusted as a valid database forever.
func TestDoltExists_TrustsPartialClone(t *testing.T) {
	doltDir := t.TempDir()
	// Simulate an interrupted clone: <doltDir>/<db>/.dolt exists but is empty
	// (no config.json, no noms data) — not a valid dolt repo.
	partial := filepath.Join(doltDir, "beads", ".dolt")
	if err := os.MkdirAll(partial, 0o750); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if !doltExists(doltDir) {
		t.Fatal("doltExists should (currently) trust a partial .dolt dir — this documents the flaw the cleanup guards against")
	}
	if schemaReady(context.Background(), doltDir, "beads") {
		t.Fatal("schemaReady should be false for a partial clone with no config.json")
	}
}

// TestDoltCloneComplete distinguishes a completed clone (noms + repo_state.json
// present) from a partial one (beads-pf1j). BootstrapFromRemoteWithDB uses this
// to decide whether an existing target may be trusted and re-clone skipped.
func TestDoltCloneComplete(t *testing.T) {
	t.Run("partial: bare .dolt is not complete", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "beads", ".dolt"), 0o750); err != nil {
			t.Fatal(err)
		}
		if doltCloneComplete(dir) {
			t.Fatal("bare .dolt (no noms/repo_state.json) must not be treated as complete")
		}
	})
	t.Run("partial: noms without repo_state is not complete", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "beads", ".dolt", "noms"), 0o750); err != nil {
			t.Fatal(err)
		}
		if doltCloneComplete(dir) {
			t.Fatal("noms without repo_state.json must not be treated as complete")
		}
	})
	t.Run("complete: noms dir + repo_state.json present", func(t *testing.T) {
		dir := t.TempDir()
		dotDolt := filepath.Join(dir, "beads", ".dolt")
		if err := os.MkdirAll(filepath.Join(dotDolt, "noms"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dotDolt, "repo_state.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !doltCloneComplete(dir) {
			t.Fatal("a .dolt with noms + repo_state.json must be treated as complete")
		}
	})
}

// TestBootstrapFromRemoteWithDB_SkipsWhenCompleteCloneExists verifies the happy
// path is preserved: a COMPLETE clone short-circuits (returns false, nil) and
// does not re-clone.
func TestBootstrapFromRemoteWithDB_SkipsWhenCompleteCloneExists(t *testing.T) {
	doltDir := t.TempDir()
	dotDolt := filepath.Join(doltDir, "beads", ".dolt")
	if err := os.MkdirAll(filepath.Join(dotDolt, "noms"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dotDolt, "repo_state.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A bogus remote would fail if a clone were attempted; since a complete
	// clone exists, the function must short-circuit before touching it.
	did, err := BootstrapFromRemoteWithDB(context.Background(), doltDir, "file:///tmp/beads-pf1j-should-not-be-used", "beads")
	if err != nil {
		t.Fatalf("expected no error when a complete clone exists, got %v", err)
	}
	if did {
		t.Fatal("expected did=false (skipped) when a complete clone already exists")
	}
	// The existing clone must be untouched.
	if _, statErr := os.Stat(filepath.Join(dotDolt, "repo_state.json")); statErr != nil {
		t.Fatal("existing complete clone was disturbed")
	}
}

// TestBootstrapFromRemoteWithDB_CleansUpPartialCloneOnFailure verifies that a
// FAILED clone does not leave a partial target directory behind (beads-pf1j).
// Without cleanup, a partial <doltDir>/<db>/.dolt would make doltExists return
// true on the next bootstrap, permanently skipping re-clone and wedging the DB.
func TestBootstrapFromRemoteWithDB_CleansUpPartialCloneOnFailure(t *testing.T) {
	if _, err := exec.LookPath("dolt"); err != nil {
		t.Skip("dolt CLI not available")
	}
	doltDir := t.TempDir()
	database := "beads"
	cloneTarget := filepath.Join(doltDir, database)

	// Pre-seed a partial clone to simulate a previous interrupted attempt
	// (dolt created the target + a partial .dolt before dying).
	if err := os.MkdirAll(filepath.Join(cloneTarget, ".dolt"), 0o750); err != nil {
		t.Fatalf("setup partial clone: %v", err)
	}

	// A bogus remote makes `dolt clone` fail deterministically. Because a
	// partial cloneTarget already exists, doltExists(doltDir) is true, so the
	// early-return would skip re-clone entirely. That is exactly the wedge —
	// but the guard we add re-clones over / cleans a partial target. Here we
	// drive the failure path and assert the partial target is removed rather
	// than left to wedge future runs.
	//
	// Note: BootstrapFromRemoteWithDB returns early (false,nil) when
	// doltExists is true. The fix must therefore treat a target whose clone
	// never completed as NOT-exists (or clean it before re-cloning). We assert
	// the post-condition: after a failed bootstrap, no partial target lingers.
	_, err := BootstrapFromRemoteWithDB(
		context.Background(),
		doltDir,
		"file:///tmp/beads-pf1j-nonexistent-remote",
		database,
	)

	// The bootstrap must NOT silently succeed on a partial clone.
	if err == nil {
		// It returned (false,nil) trusting the partial dir — the wedge.
		if _, statErr := os.Stat(filepath.Join(cloneTarget, ".dolt")); statErr == nil {
			t.Fatal("BootstrapFromRemoteWithDB trusted a partial clone (returned nil, left partial .dolt) — wedge not fixed")
		}
		return
	}

	// On failure, the partial/incomplete target must not remain — otherwise
	// doltExists wedges every future bootstrap.
	if _, statErr := os.Stat(filepath.Join(cloneTarget, ".dolt")); statErr == nil {
		t.Fatal("failed bootstrap left a partial .dolt target behind — future bootstraps will be wedged (beads-pf1j)")
	}
}
