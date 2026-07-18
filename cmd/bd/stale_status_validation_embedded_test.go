//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// bdStaleExpectFail runs "bd stale ..." expecting a nonzero exit and returns the
// combined output.
func bdStaleExpectFail(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"stale"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected bd stale %s to fail, but succeeded:\n%s", strings.Join(args, " "), out)
	}
	return string(out)
}

// bdStaleExpectOK runs "bd stale ..." expecting a zero exit and returns the
// combined output.
func bdStaleExpectOK(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"stale"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected bd stale %s to succeed, but failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// TestEmbeddedStaleStatusValidation covers beads-de4r: `bd stale --status`
// validated against a HARDCODED literal set (open/in_progress/blocked/deferred),
// OVER-rejecting `closed`, `pinned`, `hooked`, and any repo-configured custom
// status — all valid Status values that `bd list --status <x>` accepts and that
// the storage layer applies as a plain `status = ?` clause. This is the mirror
// image of the under-validation enum-reject family (a valid status was rejected,
// rather than an invalid one silently accepted). The fix reuses the shared
// custom-status-aware validation (IsValidWithCustom + validStatusList) like
// bd list/count/search/lint/human list, and treats `all` as no status filter.
func TestEmbeddedStaleStatusValidation(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "st")

	// The bug: a valid built-in status that bd list accepts must NOT be rejected
	// by bd stale. Historically these errored with the hardcoded "Valid values"
	// message.
	t.Run("closed_accepted", func(t *testing.T) {
		bdStaleExpectOK(t, bd, dir, "--status", "closed")
	})
	t.Run("pinned_accepted", func(t *testing.T) {
		bdStaleExpectOK(t, bd, dir, "--status", "pinned")
	})
	t.Run("hooked_accepted", func(t *testing.T) {
		bdStaleExpectOK(t, bd, dir, "--status", "hooked")
	})

	// No regression: the statuses the old guard already allowed still pass.
	t.Run("open_accepted", func(t *testing.T) {
		bdStaleExpectOK(t, bd, dir, "--status", "open")
	})
	t.Run("in_progress_accepted", func(t *testing.T) {
		bdStaleExpectOK(t, bd, dir, "--status", "in_progress")
	})

	// 'all' means no status filter (consistent with the other read commands),
	// not a literal status value.
	t.Run("all_accepted", func(t *testing.T) {
		bdStaleExpectOK(t, bd, dir, "--status", "all")
	})

	// Default (no --status) unchanged.
	t.Run("default_no_status_ok", func(t *testing.T) {
		bdStaleExpectOK(t, bd, dir)
	})

	// A genuine typo must still fail loud (rc!=0) with the shared message.
	t.Run("typo_rejected", func(t *testing.T) {
		out := bdStaleExpectFail(t, bd, dir, "--status", "opne")
		if !strings.Contains(out, "invalid status") {
			t.Errorf("expected an 'invalid status' error, got:\n%s", out)
		}
	})
}
