package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// TestRunRulesAuditThresholdValidation is the teeth for beads-iwup: the
// `bd rules audit --threshold` flag is a Jaccard similarity ratio consumed via
// `score >= threshold` in FindMergeCandidates — semantically identical to the
// find-duplicates --threshold flag, which gained 0.0-1.0 range validation
// (validateThreshold). rules audit had NO such validation, so out-of-range
// values silently misbehaved: >1.0 matched nothing (false "no merge
// candidates"), <0.0 matched every pair (nonsensical "similarity > -1.00").
//
// runRulesAudit reads its threshold from cmd.Flags() and scans rule files on
// disk (no database), so the guard is exercised directly via the cobra command
// without the root PersistentPreRunE no-db guard.
func TestRunRulesAuditThresholdValidation(t *testing.T) {
	// A valid, empty rules directory: an in-range threshold reaches RunAudit and
	// succeeds (zero rules is not an error), so any error for an in-range value
	// can only come from the threshold guard.
	rulesDir := t.TempDir()

	// run sets the flags and invokes runRulesAudit, capturing stderr (the
	// non-json path prints the range error there). It returns the RunE error
	// and the captured stderr text. HandleErrorRespectJSON returns a generic
	// exitError{Code:1} whose message is printed, not embedded — so the guard
	// is asserted via (non-nil error) + (message on stderr).
	run := func(t *testing.T, threshold string) (error, string) {
		t.Helper()
		if err := rulesAuditCmd.Flags().Set("path", rulesDir); err != nil {
			t.Fatalf("set path: %v", err)
		}
		if err := rulesAuditCmd.Flags().Set("threshold", threshold); err != nil {
			t.Fatalf("set threshold %s: %v", threshold, err)
		}

		origErr := os.Stderr
		r, w, perr := os.Pipe()
		if perr != nil {
			t.Fatalf("pipe: %v", perr)
		}
		os.Stderr = w
		runErr := runRulesAudit(rulesAuditCmd, nil)
		_ = w.Close()
		os.Stderr = origErr
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		return runErr, buf.String()
	}

	t.Run("out_of_range_high_rejected", func(t *testing.T) {
		err, stderr := run(t, "5.0")
		if err == nil {
			t.Fatal("--threshold 5.0 (>1.0) must be rejected, got nil error (false 'no merge candidates')")
		}
		if !strings.Contains(stderr, "0.0 and 1.0") {
			t.Errorf("expected a 0.0-1.0 range error on stderr, got: %q", stderr)
		}
	})

	t.Run("out_of_range_negative_rejected", func(t *testing.T) {
		err, stderr := run(t, "-1.0")
		if err == nil {
			t.Fatal("--threshold -1.0 (<0.0) must be rejected, got nil error (matches every pair)")
		}
		if !strings.Contains(stderr, "0.0 and 1.0") {
			t.Errorf("expected a 0.0-1.0 range error on stderr, got: %q", stderr)
		}
	})

	t.Run("in_range_and_bounds_accepted", func(t *testing.T) {
		for _, v := range []string{"0.0", "0.6", "1.0"} {
			if err, _ := run(t, v); err != nil {
				t.Errorf("--threshold %s (in range) must be accepted, got: %v", v, err)
			}
		}
	})
}
