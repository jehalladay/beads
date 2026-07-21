//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedUpsertRefreshesAllMutableColumns is the teeth for beads-u6lgj: on a
// proxied UPSERT of an EXISTING id (`bd create --id <existing> --force`, which
// reaches insertIssueRow's INSERT ... ON DUPLICATE KEY UPDATE — the sync /
// re-create path), the proxied ODKU list historically refreshed only 18 columns
// while the DIRECT path (issueops.issueUpsertColumns, ~35) refreshed the full
// user-mutable set. So a proxied re-insert SILENTLY kept stale values for the 18
// missing columns — 9 of them user-mutable (defer_until, pinned, due_at,
// waiters, mol_type, wisp_type, sender, await_id, spec_id) plus owner/work_type.
//
// This proves the fix through the CLI: due_at and defer_until are set on the
// first create, then CHANGED on the force-upsert; the re-read must reflect the
// CHANGED values, not the stale originals. With the pre-fix 18-col ODKU those
// two columns are absent from the UPDATE clause, so the stored row keeps its
// original due_at/defer_until → RED. With the ODKU derived from the shared
// issueUpsertColumns → GREEN.
//
// Coverage note: due_at + defer_until are the clearly-user-facing dropped
// columns that are CLI-settable on the create/upsert path (--due/--defer).
// pinned has no create flag and owner derives from the git author (not
// per-invocation settable through this path), so they can't be driven through
// the proxied create-upsert CLI — the invariant test in issueops
// (ref_path_helpers_test.go) guards the full column set at the source, and this
// test proves the proxied path now shares that source.
func TestProxiedUpsertRefreshesAllMutableColumns(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("force_upsert_refreshes_due_at_and_defer_until", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ups1")

		// First create with an explicit id + distinctive due/defer values.
		orig := bdProxiedCreate(t, bd, p.dir,
			"--id", "ups1-aaa", "Upsert target",
			"--due", "2027-01-15", "--defer", "2027-02-20")
		if orig.ID != "ups1-aaa" {
			t.Fatalf("explicit id: got %q, want ups1-aaa", orig.ID)
		}
		if orig.DueAt == nil || orig.DueAt.Year() != 2027 {
			t.Fatalf("initial due_at not set to 2027: %+v", orig.DueAt)
		}
		if orig.DeferUntil == nil || orig.DeferUntil.Year() != 2027 {
			t.Fatalf("initial defer_until not set to 2027: %+v", orig.DeferUntil)
		}

		// Force-upsert the SAME id with CHANGED due/defer (different year).
		out, err := bdProxiedRun(t, bd, p.dir, "create",
			"--id", "ups1-aaa", "--force", "Upsert target",
			"--due", "2029-08-10", "--defer", "2029-09-25")
		if err != nil {
			t.Fatalf("force-upsert create failed: %v\n%s", err, out)
		}

		// Re-read: the CHANGED values must have persisted. Pre-fix, due_at and
		// defer_until were absent from the proxied ODKU, so the row kept 2027.
		got := bdProxiedShow(t, bd, p.dir, "ups1-aaa")
		if got.DueAt == nil {
			t.Fatalf("due_at is nil after upsert; want 2029")
		}
		if got.DueAt.Year() != 2029 {
			t.Errorf("due_at stale after proxied upsert: got year %d, want 2029 (u6lgj: ODKU dropped due_at)", got.DueAt.Year())
		}
		if got.DeferUntil == nil {
			t.Fatalf("defer_until is nil after upsert; want 2029")
		}
		if got.DeferUntil.Year() != 2029 {
			t.Errorf("defer_until stale after proxied upsert: got year %d, want 2029 (u6lgj: ODKU dropped defer_until)", got.DeferUntil.Year())
		}
	})

	// Guard the ordinary refreshed columns still work (title was always in the
	// 18-col set) — proves the shared-list swap didn't regress the base set.
	t.Run("force_upsert_still_refreshes_title", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ups2")
		if _, err := bdProxiedRun(t, bd, p.dir, "create",
			"--id", "ups2-bbb", "Original title"); err != nil {
			t.Fatalf("initial create failed: %v", err)
		}
		if _, err := bdProxiedRun(t, bd, p.dir, "create",
			"--id", "ups2-bbb", "--force", "Rewritten title"); err != nil {
			t.Fatalf("force-upsert failed: %v", err)
		}
		got := bdProxiedShow(t, bd, p.dir, "ups2-bbb")
		if !strings.Contains(got.Title, "Rewritten title") {
			t.Errorf("title not refreshed on upsert: got %q, want 'Rewritten title'", got.Title)
		}
	})
}
