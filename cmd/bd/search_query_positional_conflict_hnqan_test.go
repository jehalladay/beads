//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestSearchQueryPositionalConflict_hnqan pins the beads-hnqan fix: `bd search`
// documents --query as an alternative to the positional query arg, and
// parseSearchQuery resolved them by first-match priority (positional first,
// then --query). So `bd search alpha --query bravo` silently used "alpha" and
// DISCARDED --query "bravo" with rc0 and no diagnostic — the dz1t8 input-source
// silent-drop / positional-vs-flag class (the same clash bd create already
// rejects: "cannot specify different titles as both positional argument and
// --title flag"). The fix rejects the conflicting pair when the two values
// differ, while tolerating a harmless matching duplicate.
//
// Mutation check: remove the `if len(args) > 0 && queryFlag != ""` reject block
// in parseSearchQuery and conflict_rejected goes RED (the command succeeds rc0,
// the positional wins, and --query is silently dropped).
func TestSearchQueryPositionalConflict_hnqan(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "sq")

	runSearch := func(t *testing.T, args ...string) (string, bool) {
		t.Helper()
		full := append([]string{"search"}, args...)
		cmd := exec.Command(bd, full...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		return string(out), err != nil
	}

	// Two issues with distinct, searchable keywords.
	alpha := bdCreate(t, bd, dir, "alpha keyword unique", "--type", "task")
	bravo := bdCreate(t, bd, dir, "bravo keyword unique", "--type", "task")

	// The conflicting positional/flag pair with DIFFERENT values must be
	// rejected loudly, not silently resolve to the positional and drop --query.
	t.Run("conflict_rejected", func(t *testing.T) {
		out, failed := runSearch(t, "alpha", "--query", "bravo")
		if !failed {
			t.Fatalf("bd search alpha --query bravo must be rejected (conflicting query sources), got success:\n%s", out)
		}
		if !strings.Contains(out, "different queries") {
			t.Errorf("expected a positional-vs--query conflict error, got:\n%s", out)
		}
	})

	// Regression: identical positional and --query is a harmless duplicate.
	t.Run("matching_duplicate_ok", func(t *testing.T) {
		out, failed := runSearch(t, "alpha", "--query", "alpha")
		if failed {
			t.Fatalf("bd search alpha --query alpha (identical) must succeed, got failure:\n%s", out)
		}
		if !strings.Contains(out, alpha.ID) {
			t.Errorf("bd search alpha --query alpha should match %s, got:\n%s", alpha.ID, out)
		}
	})

	// Regression: positional-only still searches.
	t.Run("positional_alone_ok", func(t *testing.T) {
		out, failed := runSearch(t, "alpha")
		if failed {
			t.Fatalf("bd search alpha (positional only) must succeed, got failure:\n%s", out)
		}
		if !strings.Contains(out, alpha.ID) {
			t.Errorf("bd search alpha should match %s, got:\n%s", alpha.ID, out)
		}
	})

	// Regression: --query-only still searches (it is a live alternative).
	t.Run("query_flag_alone_ok", func(t *testing.T) {
		out, failed := runSearch(t, "--query", "bravo")
		if failed {
			t.Fatalf("bd search --query bravo (flag only) must succeed, got failure:\n%s", out)
		}
		if !strings.Contains(out, bravo.ID) {
			t.Errorf("bd search --query bravo should match %s, got:\n%s", bravo.ID, out)
		}
	})
}
