//go:build cgo

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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

// TestEmbeddedSearchJSONReportsTruncation is the teeth for beads-uopti: the
// `bd search --json` path must signal --limit truncation, not silently return a
// short slice. Before the fix the JSON branch returned the truncated array with
// NO signal on either stream (the totalMatches re-query was text-path-only), so
// a consumer got e.g. 2 of 3 believing they were complete. The fix keeps the
// bare-array stdout payload (matching bd ready/list --json) and warns to stderr
// exactly like they do.
func TestEmbeddedSearchJSONReportsTruncation(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "sjt")

	for i := 1; i <= 3; i++ {
		bdCreate(t, bd, dir, fmt.Sprintf("jsontrunc item %d", i), "--type", "task")
	}

	run := func(t *testing.T, args ...string) (stdout, stderr string) {
		t.Helper()
		full := append([]string{"search", "--json"}, args...)
		cmd := exec.Command(bd, full...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		so, se, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd search --json %v failed: %v\nstdout:\n%s\nstderr:\n%s", args, err, so.String(), se.String())
		}
		return so.String(), se.String()
	}

	t.Run("truncated: stdout stays a valid 2-element array, stderr warns of the true total", func(t *testing.T) {
		stdout, stderr := run(t, "jsontrunc", "--limit", "2")
		// stdout must remain a parseable JSON array of exactly the page size (2).
		var arr []map[string]interface{}
		if jerr := json.Unmarshal(bytes.TrimSpace([]byte(stdout)), &arr); jerr != nil {
			t.Fatalf("beads-uopti: stdout must stay a valid JSON array, got parse error %v:\n%s", jerr, stdout)
		}
		if len(arr) != 2 {
			t.Fatalf("expected 2 elements under --limit 2, got %d:\n%s", len(arr), stdout)
		}
		// The truncation signal must be on stderr (matching ready/list --json).
		if !strings.Contains(stderr, "Showing 2 of 3") {
			t.Fatalf("beads-uopti: expected 'Showing 2 of 3' truncation warning on stderr, got:\n%s", stderr)
		}
	})

	t.Run("untruncated: no truncation warning", func(t *testing.T) {
		stdout, stderr := run(t, "jsontrunc")
		var arr []map[string]interface{}
		if jerr := json.Unmarshal(bytes.TrimSpace([]byte(stdout)), &arr); jerr != nil {
			t.Fatalf("stdout must be a valid JSON array, got %v:\n%s", jerr, stdout)
		}
		if len(arr) != 3 {
			t.Fatalf("expected all 3 elements, got %d:\n%s", len(arr), stdout)
		}
		if strings.Contains(stderr, "Showing ") {
			t.Fatalf("un-truncated search --json should not warn, got stderr:\n%s", stderr)
		}
	})
}
