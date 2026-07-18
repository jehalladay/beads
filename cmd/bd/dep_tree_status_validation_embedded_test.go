//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"
)

// TestEmbeddedDepTreeStatusValidation is the beads-p330 regression: `bd dep
// tree --status <invalid>` must error (exit != 0) the same way `bd list
// --status <invalid>` does, instead of silently returning an empty tree
// (exit 0). Before the fix, dep tree passed the raw flag to
// filterTreeByStatus, which matched nothing on a typo'd status and dropped the
// whole tree while reporting success — the same silent-accept gap the
// enum-value-reject family (deud/8cg2/ev8m/pbl7) closed on the other commands.
func TestEmbeddedDepTreeStatusValidation(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "dts")

	epic := bdCreate(t, bd, dir, "p330 epic", "--type", "epic")
	child := bdCreate(t, bd, dir, "p330 child", "--type", "task")
	// parent-child edge so the tree is non-trivial (proves the invalid-status
	// path aborts BEFORE filtering, not just because there are no matches).
	bdDep(t, bd, dir, "add", child.ID, epic.ID, "--type", "parent-child")

	t.Run("invalid_status_errors_text", func(t *testing.T) {
		out := bdDepFail(t, bd, dir, "tree", epic.ID, "--status", "bogusxyz")
		if !strings.Contains(out, "invalid status") {
			t.Errorf("expected 'invalid status' error, got: %s", out)
		}
	})

	t.Run("invalid_status_errors_json", func(t *testing.T) {
		// dep tree honors --format json; assert the structured error shape and a
		// nonzero exit (bdDepFail asserts the nonzero exit).
		out := bdDepFail(t, bd, dir, "tree", epic.ID, "--status", "bogusxyz", "--format", "json")
		if !strings.Contains(out, "invalid status") {
			t.Errorf("expected 'invalid status' in json error, got: %s", out)
		}
	})

	t.Run("valid_status_succeeds", func(t *testing.T) {
		out := bdDep(t, bd, dir, "tree", epic.ID, "--status", "open")
		if !strings.Contains(out, epic.ID) {
			t.Errorf("expected epic id in tree output for valid status: %s", out)
		}
	})

	t.Run("valid_status_case_normalized", func(t *testing.T) {
		// `--status OPEN` normalizes to open and must not error (parity with bd list).
		out := bdDep(t, bd, dir, "tree", epic.ID, "--status", "OPEN")
		if !strings.Contains(out, epic.ID) {
			t.Errorf("expected epic id in tree output for OPEN (normalized): %s", out)
		}
	})
}
