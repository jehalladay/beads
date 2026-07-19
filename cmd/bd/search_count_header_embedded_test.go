//go:build cgo

package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestEmbeddedSearchHeaderReportsTrueMatchCount is the end-to-end teeth for
// beads-4wn0: the `bd search` text header must report the TRUE match count, not
// the --limit-truncated page size.
//
// Before the fix, cmd/bd/search.go printed "Found %d issues" with len(issues) —
// the already-LIMIT-truncated slice — so `bd search <term> --limit 2` on 3
// matches reported "Found 2 issues" (undercount) with no "showing 2 of 3" hint.
// --limit defaults to 50, so a plain search returning >50 matches hit it too.
// The fix re-queries with Limit=0 when the page fills and reports the true
// total, plus a "Showing K of N" line when truncated (mirrors bd ready).
func TestEmbeddedSearchHeaderReportsTrueMatchCount(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "shc")

	// 3 issues all matching "sharedterm".
	for i := 1; i <= 3; i++ {
		bdCreate(t, bd, dir, fmt.Sprintf("sharedterm item %d", i), "--type", "task")
	}

	t.Run("truncated view reports true total, not page size", func(t *testing.T) {
		out := bdSearch(t, bd, dir, "sharedterm", "--limit", "2")
		// Header must report the true count (3), not the truncated page size (2).
		if !strings.Contains(out, "Found 3 issues matching") {
			t.Fatalf("beads-4wn0: header should report true match count 3, got:\n%s", out)
		}
		if strings.Contains(out, "Found 2 issues matching") {
			t.Fatalf("beads-4wn0: header reported truncated page size 2 (the bug), got:\n%s", out)
		}
		// A "Showing 2 of 3" hint must be present when truncated.
		if !strings.Contains(out, "Showing 2 of 3") {
			t.Fatalf("beads-4wn0: expected 'Showing 2 of 3' truncation hint, got:\n%s", out)
		}
		// Only 2 rows are actually listed.
		if got := strings.Count(out, "shc-"); got != 2 {
			t.Fatalf("expected 2 listed rows under --limit 2, got %d:\n%s", got, out)
		}
	})

	t.Run("untruncated view reports the count with no showing-hint", func(t *testing.T) {
		out := bdSearch(t, bd, dir, "sharedterm")
		if !strings.Contains(out, "Found 3 issues matching") {
			t.Fatalf("expected 'Found 3 issues matching', got:\n%s", out)
		}
		if strings.Contains(out, "Showing ") {
			t.Fatalf("un-truncated search should not print a 'Showing K of N' hint, got:\n%s", out)
		}
	})

	t.Run("long format header also reports true total", func(t *testing.T) {
		out := bdSearch(t, bd, dir, "sharedterm", "--limit", "2", "--long")
		if !strings.Contains(out, "Found 3 issues matching") {
			t.Fatalf("beads-4wn0 (--long): header should report true match count 3, got:\n%s", out)
		}
		if !strings.Contains(out, "Showing 2 of 3") {
			t.Fatalf("beads-4wn0 (--long): expected 'Showing 2 of 3' hint, got:\n%s", out)
		}
	})
}
