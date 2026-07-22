package main

import (
	"errors"
	"testing"
)

// TestGateCheckResolveCounts_9zt6z guards beads-9zt6z: in the `bd gate check`
// batch loop, a gate that is determined resolvable but whose closeGate fails
// must count ONLY as an error — never as resolved. Previously resolvedCount was
// incremented the moment r.resolved was true (before the close attempt), so a
// failed close double-counted the gate as BOTH resolved AND error:
// resolved+escalated+errors could exceed checked, and the summary claimed a
// still-open gate was "resolved". This affects the human text and the --json doc
// identically.
//
// MUTATION-VERIFY: make the non-dry-run branch return (1, 1) on close error (the
// old double-count) and failed_close_counts_only_as_error FAILS.
func TestGateCheckResolveCounts_9zt6z(t *testing.T) {
	t.Run("failed_close_counts_only_as_error", func(t *testing.T) {
		rd, ed := gateCheckResolveCounts(false, errors.New("update failed"))
		if rd != 0 || ed != 1 {
			t.Errorf("a resolvable gate that failed to close must count only as error\n got: resolved=%d error=%d\nwant: resolved=0 error=1", rd, ed)
		}
	})

	t.Run("successful_close_counts_as_resolved", func(t *testing.T) {
		rd, ed := gateCheckResolveCounts(false, nil)
		if rd != 1 || ed != 0 {
			t.Errorf("got resolved=%d error=%d, want resolved=1 error=0", rd, ed)
		}
	})

	t.Run("dry_run_counts_as_resolved_no_error", func(t *testing.T) {
		// Dry-run performs no close; it previews what WOULD resolve.
		rd, ed := gateCheckResolveCounts(true, nil)
		if rd != 1 || ed != 0 {
			t.Errorf("got resolved=%d error=%d, want resolved=1 error=0", rd, ed)
		}
	})
}
