//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestEmbeddedReadyReversedRangeRejected is the teeth for beads-tjysi.
//
// `bd ready` gained the same range filters as `bd list` (--priority-min/max via
// cseh3, --created/updated/due-after/before via 10y4y/zmtp6), but — like list
// before beads-wnm6g — validated each bound independently and never checked the
// PAIR. A reversed range (min > max, after > before) builds an always-false
// WHERE ("priority >= 4 AND priority <= 0"), so `bd ready` silently returns an
// empty result with no error instead of rejecting the contradiction. This is
// the ready-parity sibling of the wnm6g list fix (BUG-36/BUG-37).
//
// End-to-end embedded test (not a marshal): runs the real `bd ready` binary and
// asserts the reversed range EXITS NON-ZERO with a clear message on both the
// priority and date axes. RED proof: dropping the guards makes ready exit 0 with
// an empty list.
func TestEmbeddedReadyReversedRangeRejected(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "rr")

	// A ready issue must exist so an accepted-but-contradictory range would
	// return a (silently-empty) success rather than trivially empty.
	_ = bdCreate(t, bd, dir, "seed", "--type", "task", "--priority", "2")

	cases := []struct {
		name   string
		args   []string
		errSub string
	}{
		{"priority_min_gt_max", []string{"--priority-min", "4", "--priority-max", "0"}, "--priority-min"},
		{"created_after_gt_before", []string{"--created-after", "2099-01-01", "--created-before", "2020-01-01"}, "--created-after"},
		{"updated_after_gt_before", []string{"--updated-after", "2099-01-01", "--updated-before", "2020-01-01"}, "--updated-after"},
		{"due_after_gt_before", []string{"--due-after", "2099-01-01", "--due-before", "2020-01-01"}, "--due-after"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			full := append([]string{"ready", "--json"}, tc.args...)
			cmd := exec.Command(bd, full...)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("expected non-zero exit for reversed range %v, got success:\n%s", tc.args, out)
			}
			if !strings.Contains(string(out), tc.errSub) || !strings.Contains(string(out), "cannot be") {
				t.Errorf("reversed range %v: expected an error mentioning %q and \"cannot be\", got:\n%s", tc.args, tc.errSub, out)
			}
		})
	}

	// Equal bounds must stay valid (not rejected as reversed).
	full := []string{"ready", "--json", "--priority-min", "2", "--priority-max", "2"}
	cmd := exec.Command(bd, full...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("equal priority bounds (min==max) should be valid, got error: %v\n%s", err, out)
	}
}
