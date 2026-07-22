//go:build cgo

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEmbeddedMigrateIssuesIncludeSilentDrop is the behavioral teeth for
// beads-0oj6e: `bd migrate issues --include <mode>` accepted ANY string with no
// validation. validateMigrateIssuesFilters (beads-ev8m) checked
// --status/--type/--priority but MISSED --include. A typo like "closur" is not
// "none"/"" so it skips the expandMigrationSet short-circuit, enters the BFS,
// and matches NO case in the include switch (there was no default) → deps stays
// nil → migrationSet == candidates → the requested dependency closure is
// SILENTLY dropped while the operator sees "✓ Successfully migrated N".
//
// This proves the fail-loud fix end-to-end on a MUTATING (non-dry-run) move:
//   - --include closur (typo) must abort NON-zero with the invalid --include
//     error, and must NOT move only the seed {X} while dropping its upstream D.
//   - --include closure (valid) must migrate BOTH {X, D}.
//
// Seeding mirrors migrate_issues_status_apply_3hmym_test.go: a plain `bd create`
// leaves source_repo empty (unselectable by --from), so issues are seeded via a
// sibling repo's issues.jsonl carrying an explicit source_repo + `bd repo
// add`/`repo sync`, then the dependency edge is created locally.
func TestEmbeddedMigrateIssuesIncludeSilentDrop(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "inc")

	// Seed X (the explicitly migrated issue) and D (its in-A upstream
	// dependency) with a concrete source_repo so --from can select them.
	srcRepo := t.TempDir()
	srcBeads := filepath.Join(srcRepo, ".beads")
	if err := os.MkdirAll(srcBeads, 0o755); err != nil {
		t.Fatalf("mkdir src .beads: %v", err)
	}
	xID, dID := "inc-x", "inc-dep"
	jsonl := `{"id":"` + xID + `","title":"explicitly migrated issue","status":"open","priority":2,"issue_type":"bug","source_repo":"` + srcRepo + `","created_at":"2026-07-22T00:00:00Z","updated_at":"2026-07-22T00:00:00Z"}
{"id":"` + dID + `","title":"upstream dependency in A","status":"open","priority":2,"issue_type":"task","source_repo":"` + srcRepo + `","created_at":"2026-07-22T00:00:00Z","updated_at":"2026-07-22T00:00:00Z"}
`
	if err := os.WriteFile(filepath.Join(srcBeads, "issues.jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write src issues.jsonl: %v", err)
	}
	runOK := func(t *testing.T, args ...string) string {
		t.Helper()
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bd %v failed: %v\n%s", args, err, out)
		}
		return string(out)
	}
	runOK(t, "repo", "add", srcRepo)
	runOK(t, "repo", "sync")

	// X depends on D (X is upstream-blocked by D): a `blocks` edge D -> X makes
	// D an upstream dependency of X, so --include upstream/closure pulls D in.
	runOK(t, "dep", "add", xID, dID, "--type", "blocks")

	// LEG 1 (fail-loud): a typo'd --include must abort NON-zero with the
	// invalid --include error on a real (non-dry-run) migrate, before any move.
	t.Run("typo_aborts_loudly_no_partial_move", func(t *testing.T) {
		toDir := t.TempDir()
		out := bdMigrateFail(t, bd, dir, "issues", "--from", srcRepo, "--to", toDir, "--id", xID, "--include", "closur")
		if !strings.Contains(out, "invalid --include") {
			t.Errorf("beads-0oj6e: --include closur must abort with an 'invalid --include' error, got:\n%s", out)
		}
		if strings.Contains(out, "Successfully migrated") {
			t.Errorf("beads-0oj6e: --include closur silently reported success (partial move dropping the closure), got:\n%s", out)
		}
	})

	// LEG 2 (valid mode still works): --include closure must migrate BOTH X and
	// its upstream D — proving the fix did not over-narrow and reject a real
	// expansion mode.
	t.Run("valid_closure_migrates_x_and_dep", func(t *testing.T) {
		toDir := t.TempDir()
		out := bdMigrate(t, bd, dir, "issues", "--from", srcRepo, "--to", toDir, "--id", xID, "--include", "closure", "--dry-run")
		if strings.Contains(out, "invalid") {
			t.Fatalf("beads-0oj6e: valid --include closure was unexpectedly rejected:\n%s", out)
		}
		if !strings.Contains(out, xID) {
			t.Errorf("--include closure must include the explicitly requested issue %s:\n%s", xID, out)
		}
		if !strings.Contains(out, dID) {
			t.Errorf("--include closure must expand to the upstream dependency %s (the closure the typo silently dropped):\n%s", dID, out)
		}
	})
}
