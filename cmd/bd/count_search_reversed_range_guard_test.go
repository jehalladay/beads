//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestEmbeddedCountSearchReversedRangeRejected is the teeth for beads-8a631.
//
// `bd count` and `bd search` carry the same range filters as `bd list`
// (--priority-min/max, --created/updated/closed-after/before) but — like list
// before beads-wnm6g and ready before beads-tjysi — validated each bound
// independently and never checked the PAIR. A reversed range (min > max, after
// > before) builds an always-false WHERE, so both silently returned a
// zero/empty result instead of rejecting the contradiction.
//
// End-to-end embedded test (real binary), asserting every reversed axis exits
// non-zero with a clear message, and equal bounds stay valid. RED-verified by
// running before the guards (all returned exit 0).
func TestEmbeddedCountSearchReversedRangeRejected(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "cs")
	_ = bdCreate(t, bd, dir, "reversed range seed", "--type", "task", "--priority", "2")

	reversed := []struct {
		name   string
		args   []string
		errSub string
	}{
		{"priority_min_gt_max", []string{"--priority-min", "4", "--priority-max", "0"}, "--priority-min"},
		{"created_after_gt_before", []string{"--created-after", "2099-01-01", "--created-before", "2020-01-01"}, "--created-after"},
		{"updated_after_gt_before", []string{"--updated-after", "2099-01-01", "--updated-before", "2020-01-01"}, "--updated-after"},
		{"closed_after_gt_before", []string{"--closed-after", "2099-01-01", "--closed-before", "2020-01-01"}, "--closed-after"},
	}

	runFail := func(t *testing.T, verb string, args []string, errSub string) {
		t.Helper()
		full := append([]string{verb}, args...)
		cmd := exec.Command(bd, full...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected `bd %s %v` to reject the reversed range, got success:\n%s", verb, args, out)
		}
		if !strings.Contains(string(out), errSub) || !strings.Contains(string(out), "cannot be") {
			t.Errorf("`bd %s %v`: expected an error mentioning %q and \"cannot be\", got:\n%s", verb, args, errSub, out)
		}
	}

	for _, tc := range reversed {
		t.Run("count/"+tc.name, func(t *testing.T) { runFail(t, "count", tc.args, tc.errSub) })
		// search takes a [query]; append one so only the reversed range differs.
		t.Run("search/"+tc.name, func(t *testing.T) { runFail(t, "search", append([]string{"seed"}, tc.args...), tc.errSub) })
	}

	// Equal bounds must stay valid (min==max) — a surgical no-regression check.
	for _, verb := range []string{"count", "search"} {
		full := []string{verb}
		if verb == "search" {
			full = append(full, "seed")
		}
		full = append(full, "--priority-min", "2", "--priority-max", "2")
		cmd := exec.Command(bd, full...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("`bd %s` equal priority bounds should be valid, got error: %v\n%s", verb, err, out)
		}
	}
}
