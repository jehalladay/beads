//go:build cgo

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEmbeddedMigrateIssuesStatusApply is the teeth for beads-3hmym: the
// bd migrate-issues apply path (findCandidateIssues) must Normalize the
// --status value and route the "blocked" pseudo-status to filter.Blocked,
// mirroring the validate path (validateMigrateIssuesFilters, beads-ev8m) and
// count.go/list/lint(pbelp)/stale(h40fl).
//
// Before the fix the apply path used a raw types.Status(p.status) cast:
//   LEG A (no case-normalize): --status OPEN passed validation (Normalized to
//     "open") then applied `status = 'OPEN'`, which matches nothing → a silent
//     zero-migration reported as "Nothing to do" exit 0 on a MUTATING command.
//   LEG B (blocked pseudo-status): --status blocked passed validation (a valid
//     value) then applied the unsatisfiable `status = 'blocked'` predicate
//     ("blocked" is the derived is_blocked column, beads-7f3g, never stored) →
//     zero of the blocked cohort selected.
//
// Seeding: a plain `bd create` leaves source_repo empty, which migrate cannot
// select (--from matches the stored source_repo). So issues are seeded via a
// sibling repo's issues.jsonl carrying an explicit source_repo + `bd repo
// add`/`repo sync` (the same lever migrate_issues_json_test.go uses); the
// blocked state is then created locally with `bd dep add ... --type blocks`.
func TestEmbeddedMigrateIssuesStatusApply(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "msa")

	runBD := func(t *testing.T, args ...string) string {
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

	// Seed three issues with a concrete source_repo: an open bug, a
	// to-be-blocked bug, and its open blocker.
	srcRepo := t.TempDir()
	srcBeads := filepath.Join(srcRepo, ".beads")
	if err := os.MkdirAll(srcBeads, 0o755); err != nil {
		t.Fatalf("mkdir src .beads: %v", err)
	}
	openID, blockedID, blockerID := "msa-open", "msa-blocked", "msa-blocker"
	jsonl := `{"id":"` + openID + `","title":"plain open bug","status":"open","priority":2,"issue_type":"bug","source_repo":"` + srcRepo + `","created_at":"2026-07-19T00:00:00Z","updated_at":"2026-07-19T00:00:00Z"}
{"id":"` + blockedID + `","title":"blocked bug","status":"open","priority":2,"issue_type":"bug","source_repo":"` + srcRepo + `","created_at":"2026-07-19T00:00:00Z","updated_at":"2026-07-19T00:00:00Z"}
{"id":"` + blockerID + `","title":"open blocker","status":"open","priority":2,"issue_type":"task","source_repo":"` + srcRepo + `","created_at":"2026-07-19T00:00:00Z","updated_at":"2026-07-19T00:00:00Z"}
`
	if err := os.WriteFile(filepath.Join(srcBeads, "issues.jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write src issues.jsonl: %v", err)
	}
	runBD(t, "repo", "add", srcRepo)
	runBD(t, "repo", "sync")

	// Make blockedID actually blocked (sets is_blocked=1 in the local store).
	runBD(t, "dep", "add", blockedID, blockerID, "--type", "blocks")

	migrate := func(t *testing.T, statusVal string) string {
		t.Helper()
		// Fresh --to each call so a prior subtest can't affect selection.
		args := []string{"issues", "--from", srcRepo, "--to", t.TempDir(), "--dry-run", "--status", statusVal}
		return bdMigrate(t, bd, dir, args...)
	}

	// Control: lowercase --status open plans a migration including the open bug
	// (unaffected by the fix; proves the seed + harness select at all).
	t.Run("control_lowercase_open_selects", func(t *testing.T) {
		out := migrate(t, "open")
		if strings.Contains(out, "Nothing to do") {
			t.Fatalf("setup: --status open should select open issues, got:\n%s", out)
		}
		if !strings.Contains(out, openID) {
			t.Errorf("--status open should include open bug %s:\n%s", openID, out)
		}
	})

	// LEG A: uppercase --status OPEN must Normalize and select, NOT hit the
	// raw-cast `status='OPEN'` silent no-op ("Nothing to do").
	t.Run("uppercase_status_normalizes_and_selects", func(t *testing.T) {
		out := migrate(t, "OPEN")
		if strings.Contains(out, "Nothing to do") {
			t.Errorf("beads-3hmym LEG A: --status OPEN silently selected zero issues (raw cast `status='OPEN'`); must Normalize and select %s:\n%s", openID, out)
		}
		if !strings.Contains(out, openID) {
			t.Errorf("--status OPEN must Normalize to 'open' and include open bug %s:\n%s", openID, out)
		}
	})

	// LEG B: --status blocked must route to filter.Blocked (is_blocked
	// pseudo-status) and select the blocked cohort, NOT apply the unsatisfiable
	// stored `status='blocked'` predicate that selects nothing.
	t.Run("blocked_pseudostatus_selects_blocked_cohort", func(t *testing.T) {
		out := migrate(t, "blocked")
		if strings.Contains(out, "Nothing to do") {
			t.Errorf("beads-3hmym LEG B: --status blocked selected zero issues (unsatisfiable stored predicate `status='blocked'`); must route to filter.Blocked and select blocked bug %s:\n%s", blockedID, out)
		}
		if !strings.Contains(out, blockedID) {
			t.Errorf("--status blocked must select the blocked cohort (blocked bug %s):\n%s", blockedID, out)
		}
	})
}
