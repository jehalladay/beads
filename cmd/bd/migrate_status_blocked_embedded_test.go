//go:build cgo

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEmbeddedMigrateIssuesStatusBlocked covers beads-s5ha0: bd migrate issues
// --status blocked must route the "blocked" pseudo-status to the is_blocked
// predicate (filter.Blocked), NOT the stored-status column (filter.Status).
//
// "blocked" is a DERIVED pseudo-status (beads-7f3g): a blocked issue keeps its
// stored status ('open'/'in_progress') and blocked-ness lives in the
// denormalized is_blocked column. A plain `status = 'blocked'` WHERE clause is
// unsatisfiable by construction, so before the fix findCandidateIssues selected
// ZERO issues and migrate reported "Nothing to do" (rc 0) — a silent no-op move
// on a MUTATING command, exactly the failure mode beads-ev8m's --status
// validation was added to prevent. This mirrors the landed sibling fixes for
// bd list/count (7f3g), lint (pbelp), stale (h40fl), and
// find-duplicates/human/search (3x0e4).
//
// Seeding: a plain `bd create` leaves source_repo empty, which migrate cannot
// select (--from is a source_repo filter). So we import a sibling repo's
// issues.jsonl via `bd repo add` + `bd repo sync` (which stamps
// source_repo = the added repo path, the same lever migrate_issues_json_test
// uses), then wire a blocks edge in the target store so is_blocked is computed.
func TestEmbeddedMigrateIssuesStatusBlocked(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "msb")

	runBD := func(t *testing.T, args ...string) string {
		t.Helper()
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd %v failed: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout.String(), stderr.String())
		}
		return stdout.String()
	}

	// Seed a source repo with a blocker, a dependent, and a free (unblocked)
	// bug, all stamped source_repo=srcRepo. The dependent blocks-edge makes it
	// is_blocked=1 while its stored status stays "open".
	srcRepo := t.TempDir()
	srcBeads := filepath.Join(srcRepo, ".beads")
	if err := os.MkdirAll(srcBeads, 0o755); err != nil {
		t.Fatalf("mkdir src .beads: %v", err)
	}
	jsonl := `{"id":"src-blk","title":"root blocker task","status":"open","priority":2,"issue_type":"task","source_repo":"` + srcRepo + `","created_at":"2026-07-19T00:00:00Z","updated_at":"2026-07-19T00:00:00Z"}
{"id":"src-dep","title":"blocked dependent bug","status":"open","priority":2,"issue_type":"bug","source_repo":"` + srcRepo + `","created_at":"2026-07-19T00:00:00Z","updated_at":"2026-07-19T00:00:00Z"}
{"id":"src-free","title":"unblocked open bug","status":"open","priority":2,"issue_type":"bug","source_repo":"` + srcRepo + `","created_at":"2026-07-19T00:00:00Z","updated_at":"2026-07-19T00:00:00Z"}
`
	if err := os.WriteFile(filepath.Join(srcBeads, "issues.jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write src issues.jsonl: %v", err)
	}
	runBD(t, "repo", "add", srcRepo)
	runBD(t, "repo", "sync")

	// Wire the blocks edge so src-dep is is_blocked=1 (stored status stays open).
	runBD(t, "dep", "add", "src-dep", "src-blk", "--type", "blocks")

	dest := t.TempDir()

	// Precondition: bd list --status blocked (the 7f3g authority) sees the
	// dependent — proves is_blocked=1 with a stored status that is not "blocked".
	t.Run("precondition_list_blocked_sees_dependent", func(t *testing.T) {
		listed := bdListJSON(t, bd, dir, "--status", "blocked")
		if !containsID(listed, "src-dep") {
			t.Fatalf("precondition: bd list --status blocked should include src-dep")
		}
		if containsID(listed, "src-free") || containsID(listed, "src-blk") {
			t.Fatalf("precondition: only the blocked dependent src-dep should be listed")
		}
	})

	// bd migrate issues --status blocked --dry-run must plan the blocked
	// dependent (and NOT the unblocked bug / blocker), instead of the pre-fix
	// "Nothing to do".
	t.Run("migrate_status_blocked_selects_blocked_cohort", func(t *testing.T) {
		out := runBD(t, "migrate", "issues", "--from", srcRepo, "--to", dest,
			"--status", "blocked", "--dry-run")
		if strings.Contains(out, "Nothing to do") {
			t.Fatalf("beads-s5ha0: migrate --status blocked selected zero (silent no-op):\n%s", out)
		}
		if !strings.Contains(out, "src-dep") {
			t.Errorf("beads-s5ha0: blocked dependent src-dep must be in the migration plan:\n%s", out)
		}
		if strings.Contains(out, "src-free") {
			t.Errorf("beads-s5ha0: unblocked bug src-free must NOT be selected by --status blocked:\n%s", out)
		}
		if strings.Contains(out, "src-blk") {
			t.Errorf("beads-s5ha0: blocker src-blk is not blocked and must NOT be selected:\n%s", out)
		}
	})

	// Regression guard: a real stored status still routes to filter.Status and
	// selects the correct cohort (no over-broadening from the blocked branch).
	// All three seeded issues are stored-status open, so --status open selects
	// all of them (proves the else-branch still routes to filter.Status).
	t.Run("migrate_status_open_still_works", func(t *testing.T) {
		out := runBD(t, "migrate", "issues", "--from", srcRepo, "--to", dest,
			"--status", "open", "--dry-run")
		if strings.Contains(out, "invalid") || strings.Contains(out, "Nothing to do") {
			t.Errorf("beads-s5ha0: --status open unexpectedly selected nothing/rejected:\n%s", out)
		}
		if !strings.Contains(out, "src-free") {
			t.Errorf("beads-s5ha0: --status open must still select the open bug src-free:\n%s", out)
		}
	})
}
