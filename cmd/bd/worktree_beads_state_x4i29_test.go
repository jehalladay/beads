package main

import (
	"os"
	"path/filepath"
	"testing"
)

// beads-x4i29: getBeadsState classified any physical, non-main .beads/ dir as
// "local". But `bd worktree create` produces a worktree whose .beads/ holds
// only git-TRACKED artifacts (metadata.json, config.yaml) — the actual DB
// dirs (dolt/, embeddeddolt/, proxieddb/, *.db) are gitignored and absent — so
// it resolves the DB via git-common-dir and genuinely SHARES the main DB. The
// classifier must return "shared" for such a worktree, and reserve "local" for
// a worktree that owns a real DB.
//
// getBeadsState is a pure filesystem classifier (os.Stat / Glob on a path), so
// these teeth run against t.TempDir() with no Docker / no Dolt server.
//
// MUTATION-VERIFIED: reverting the worktreeHasLocalBeadsDatabase gate (i.e.
// unconditionally returning "local" for a non-main .beads/) → the
// tracked-artifacts-only subtest goes RED.
func TestGetBeadsState_x4i29(t *testing.T) {
	mkBeads := func(t *testing.T, files []string, dirs []string) (worktreePath, beadsDir string) {
		t.Helper()
		worktreePath = t.TempDir()
		beadsDir = filepath.Join(worktreePath, ".beads")
		if err := os.MkdirAll(beadsDir, 0o755); err != nil {
			t.Fatalf("mkdir .beads: %v", err)
		}
		for _, f := range files {
			if err := os.WriteFile(filepath.Join(beadsDir, f), []byte("x"), 0o644); err != nil {
				t.Fatalf("write %s: %v", f, err)
			}
		}
		for _, d := range dirs {
			if err := os.MkdirAll(filepath.Join(beadsDir, d), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", d, err)
			}
		}
		return worktreePath, beadsDir
	}

	// A distinct main .beads dir so the worktree is never the main dir.
	otherMain := filepath.Join(t.TempDir(), ".beads")

	t.Run("tracked artifacts only (worktree create) -> shared", func(t *testing.T) {
		// metadata.json + config.yaml are git-tracked and check out into every
		// worktree; no gitignored DB dir → the DB resolves via common-dir.
		wt, _ := mkBeads(t, []string{"metadata.json", "config.yaml"}, nil)
		if got := getBeadsState(wt, otherMain); got != "shared" {
			t.Errorf("worktree with only tracked .beads artifacts: got %q, want \"shared\" (beads-x4i29) — it shares the main DB via git-common-dir, it is not local", got)
		}
	})

	t.Run("owns embeddeddolt DB -> local", func(t *testing.T) {
		wt, _ := mkBeads(t, []string{"metadata.json"}, []string{"embeddeddolt"})
		if got := getBeadsState(wt, otherMain); got != "local" {
			t.Errorf("worktree owning an embeddeddolt/ DB: got %q, want \"local\"", got)
		}
	})

	t.Run("owns dolt DB -> local", func(t *testing.T) {
		wt, _ := mkBeads(t, []string{"metadata.json"}, []string{"dolt"})
		if got := getBeadsState(wt, otherMain); got != "local" {
			t.Errorf("worktree owning a dolt/ DB: got %q, want \"local\"", got)
		}
	})

	t.Run("owns proxieddb DB -> local", func(t *testing.T) {
		wt, _ := mkBeads(t, []string{"metadata.json"}, []string{"proxieddb"})
		if got := getBeadsState(wt, otherMain); got != "local" {
			t.Errorf("worktree owning a proxieddb/ DB: got %q, want \"local\"", got)
		}
	})

	t.Run("owns *.db file -> local", func(t *testing.T) {
		wt, _ := mkBeads(t, []string{"beads.db"}, nil)
		if got := getBeadsState(wt, otherMain); got != "local" {
			t.Errorf("worktree owning a *.db file: got %q, want \"local\"", got)
		}
	})

	t.Run("backup/vc.db do not count as local", func(t *testing.T) {
		// A worktree whose only *.db files are a backup or vc.db still shares.
		wt, _ := mkBeads(t, []string{"issues.backup.db", "vc.db"}, nil)
		if got := getBeadsState(wt, otherMain); got != "shared" {
			t.Errorf("worktree with only backup/vc.db: got %q, want \"shared\" (those are not the beads DB)", got)
		}
	})

	t.Run("redirect file -> redirect (unchanged)", func(t *testing.T) {
		wt, beadsDir := mkBeads(t, nil, nil)
		if err := os.WriteFile(filepath.Join(beadsDir, "redirect"), []byte("../main/.beads"), 0o644); err != nil {
			t.Fatalf("write redirect: %v", err)
		}
		if got := getBeadsState(wt, otherMain); got != "redirect" {
			t.Errorf("worktree with a redirect file: got %q, want \"redirect\"", got)
		}
	})

	t.Run("no .beads -> none (unchanged)", func(t *testing.T) {
		wt := t.TempDir()
		if got := getBeadsState(wt, otherMain); got != "none" {
			t.Errorf("worktree with no .beads: got %q, want \"none\"", got)
		}
	})

	t.Run("is the main beads dir -> shared (unchanged)", func(t *testing.T) {
		wt, beadsDir := mkBeads(t, []string{"metadata.json"}, []string{"embeddeddolt"})
		// Pass the worktree's own .beads as mainBeadsDir → the main-dir branch
		// returns "shared" even though it owns a DB (it IS the main dir).
		if got := getBeadsState(wt, beadsDir); got != "shared" {
			t.Errorf("the main beads dir: got %q, want \"shared\"", got)
		}
	})
}
