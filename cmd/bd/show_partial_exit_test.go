//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// bdShowExpectFail runs "bd show ..." expecting a nonzero exit and returns the
// combined output.
func bdShowExpectFail(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"show"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected bd show %s to fail, but succeeded:\n%s", strings.Join(args, " "), out)
	}
	return string(out)
}

// bdShowExpectOK runs "bd show ..." expecting a zero exit and returns the
// combined output.
func bdShowExpectOK(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"show"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected bd show %s to succeed, but failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// TestEmbeddedShowPartialExitCode covers beads-sw7l: bd show accepts multiple
// ids and its loop `continue`s past unresolvable ones. It historically exited
// rc=0 whenever ANY id resolved, so `bd show <valid> <ghost>` silently returned
// success while a requested id was missing — inconsistent with the all-failed
// case (rc=1) and with the single-id path. It must exit non-zero when any id
// fails, while still displaying the issues that were found (partial display
// preserved).
func TestEmbeddedShowPartialExitCode(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "sw")

	a := bdCreate(t, bd, dir, "show valid a", "--type", "task")
	b := bdCreate(t, bd, dir, "show valid b", "--type", "task")

	// No regression: single valid id and all-valid multi both exit zero.
	t.Run("single_valid_exits_zero", func(t *testing.T) {
		bdShowExpectOK(t, bd, dir, a.ID)
	})
	t.Run("multi_all_valid_exits_zero", func(t *testing.T) {
		bdShowExpectOK(t, bd, dir, a.ID, b.ID)
	})

	// Sanity: all-bogus already exits non-zero.
	t.Run("multi_all_bogus_exits_nonzero", func(t *testing.T) {
		bdShowExpectFail(t, bd, dir, "sw-ghost-a", "sw-ghost-b")
	})

	// The bug: valid + ghost must exit non-zero, but the valid issue is still
	// displayed (partial display preserved).
	t.Run("multi_partial_exits_nonzero_still_shows_valid", func(t *testing.T) {
		out := bdShowExpectFail(t, bd, dir, a.ID, "sw-ghost")
		if !strings.Contains(out, a.ID) {
			t.Errorf("expected valid issue %s to still be shown on partial failure, got:\n%s", a.ID, out)
		}
	})

	// The bug on the --json path: valid + ghost must exit non-zero, and stdout
	// must still carry a parseable array containing the valid issue.
	t.Run("multi_partial_json_exits_nonzero_still_emits_valid", func(t *testing.T) {
		cmd := exec.Command(bd, "show", a.ID, "sw-ghost", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err == nil {
			t.Fatalf("expected bd show %s sw-ghost --json to fail, but succeeded\nstdout:\n%s", a.ID, stdout.String())
		}
		s := strings.TrimSpace(stdout.String())
		start := strings.IndexByte(s, '[')
		if start < 0 {
			t.Fatalf("expected a JSON array on stdout carrying the found issue, got:\n%s", s)
		}
		var arr []map[string]interface{}
		if jerr := json.Unmarshal([]byte(s[start:]), &arr); jerr != nil {
			t.Fatalf("stdout is not a parseable JSON array: %v\n%s", jerr, s)
		}
		if len(arr) != 1 || arr[0]["id"] != a.ID {
			t.Errorf("expected the found issue %s in the stdout array, got: %v", a.ID, arr)
		}
	})
}
