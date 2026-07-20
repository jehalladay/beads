//go:build cgo

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestEmbeddedRulesAuditThresholdValidation is the beads-iwup teeth.
//
// `bd rules audit --threshold` is a Jaccard similarity ratio (consumed via
// score >= threshold in FindMergeCandidates) — identical semantics to
// find-duplicates --threshold, which validates the 0.0-1.0 range via the shared
// validateThreshold (beads-j1r0). rules audit had NO validation, so an
// out-of-range --threshold (e.g. 5 or -1) was silently accepted, producing
// all-or-nothing merge candidates instead of surfacing the bad input.
//
// Drives the real binary. `bd rules audit` reads a rules directory (default
// .claude/rules/, empty on a missing dir) and needs no Dolt, so the guard —
// which fires before the dir read — is exercised in a bare temp dir. RED
// proof: without the validateThreshold call, both invocations exit 0.
func TestEmbeddedRulesAuditThresholdValidation(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "rth")

	run := func(t *testing.T, threshold string) string {
		t.Helper()
		cmd := exec.Command(bd, "rules", "audit", "--threshold", threshold)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected `bd rules audit --threshold %s` to fail (out-of-range), got success:\n%s", threshold, out)
		}
		return string(out)
	}

	t.Run("above_one_rejected", func(t *testing.T) {
		if out := run(t, "5"); !strings.Contains(out, "threshold must be between 0.0 and 1.0") {
			t.Errorf("expected threshold-range error, got: %s", out)
		}
	})
	t.Run("negative_rejected", func(t *testing.T) {
		if out := run(t, "-1"); !strings.Contains(out, "threshold must be between 0.0 and 1.0") {
			t.Errorf("expected threshold-range error, got: %s", out)
		}
	})

	// A valid in-range threshold must NOT be rejected (surgical no-regression):
	// the default (missing) rules dir audits cleanly (exit 0).
	t.Run("in_range_ok", func(t *testing.T) {
		cmd := exec.Command(bd, "rules", "audit", "--threshold", "0.7")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("in-range --threshold 0.7 should succeed, got error: %v\n%s", err, out)
		}
	})
}
