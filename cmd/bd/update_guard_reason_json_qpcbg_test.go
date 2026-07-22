//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// beads-qpcbg: `bd update --status closed --json` (and --type demote, --status
// open reopen) where the SOLE failure is an integrity-guard rejection
// (open-child / blocked-by / closed-parent / superseded / duplicate /
// molecule-close-fail) must surface the REAL reason on stdout — not the generic
// "no issues updated matching the provided IDs", which misleads a --json
// consumer into thinking the ID did not exist. 9c0o fixed this for
// invalid-field-value; the close/demote/reopen guards predated the
// reportUpdateItemError wrapper and used the bare reportItemError (stderr-only),
// so their reason never reached deferredItemErrors → generic stdout on sole
// failure. Fix routes all guard rejections through reportUpdateItemError.
//
// Preserves the beads-92tz one-object contract (single JSON object on stdout,
// no competing JSON object on stderr).

// assertGuardReasonOnStdout runs `bd <args...> --json` expecting a non-zero exit
// and asserts the stdout JSON error carries wantReason and NOT the generic
// no-match string, with a clean (non-JSON) stderr.
func assertGuardReasonOnStdout(t *testing.T, bd, dir, wantReason string, args ...string) {
	t.Helper()
	full := append(append([]string{}, args...), "--json")
	cmd := exec.Command(bd, full...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err == nil {
		t.Fatalf("expected non-zero exit for guard rejection %v; stdout=%s stderr=%s", args, stdout.String(), stderr.String())
	}
	out := strings.TrimSpace(stdout.String())
	var obj map[string]any
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a single JSON object: %v\nstdout:\n%s", jerr, out)
	}
	errVal, _ := obj["error"].(string)
	if errVal == "" {
		t.Fatalf("expected a non-empty \"error\" field, got: %s", out)
	}
	// MUTATION-VERIFY: revert the guard callsite from reportUpdateItemError back
	// to the bare reportItemError and this FAILS — the guard reason goes only to
	// stderr, deferredItemErrors stays empty, and the all-failed path emits the
	// generic no-match string on stdout instead of wantReason.
	if strings.Contains(errVal, "no issues updated matching the provided IDs") {
		t.Errorf("--json error masks the guard reason with the generic no-match message (beads-qpcbg): %q", errVal)
	}
	if !strings.Contains(errVal, wantReason) {
		t.Errorf("expected the real guard reason %q in the --json error, got: %q", wantReason, errVal)
	}
	// beads-92tz: stderr must not carry a competing JSON object.
	errStr := strings.TrimSpace(stderr.String())
	if errStr != "" && json.Valid([]byte(errStr)) {
		t.Errorf("stderr must be clean of a competing JSON object (beads-92tz); got:\n%s", errStr)
	}
}

func TestEmbeddedUpdateGuardReasonJSON_qpcbg(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt update tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// Close guard: epic with an open child. `bd update E --status closed --json`
	// must surface "open child", not the generic no-match.
	t.Run("close_open_child_guard_reason_on_stdout", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "ugc")
		epic := bdCreate(t, bd, dir, "parent epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "open child", "--type", "task")
		bdDep(t, bd, dir, "add", child.ID, epic.ID, "--type", "parent-child")

		assertGuardReasonOnStdout(t, bd, dir, "open child", "update", epic.ID, "--status", "closed")
	})

	// Demote guard: demoting an epic with an open child to task. `bd update E
	// --type task --json` must surface "cannot demote", not the generic no-match.
	t.Run("demote_open_child_guard_reason_on_stdout", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "ugd")
		epic := bdCreate(t, bd, dir, "demote epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "open child d", "--type", "task")
		bdDep(t, bd, dir, "add", child.ID, epic.ID, "--type", "parent-child")

		assertGuardReasonOnStdout(t, bd, dir, "cannot demote", "update", epic.ID, "--type", "task")
	})

	// Reopen guard: reopening a closed child whose parent epic is closed. `bd
	// update C --status open --json` must surface "parent", not the generic.
	t.Run("reopen_closed_parent_guard_reason_on_stdout", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "ugr")
		epic := bdCreate(t, bd, dir, "reopen epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "reopen child", "--type", "task")
		bdDep(t, bd, dir, "add", child.ID, epic.ID, "--type", "parent-child")
		// Close the child, then the epic (all children closed → epic closeable).
		runUpdateOK_qpcbg(t, bd, dir, child.ID, "--status", "closed")
		runUpdateOK_qpcbg(t, bd, dir, epic.ID, "--status", "closed")

		// Reopening the child while its parent epic is closed → guard rejection.
		assertGuardReasonOnStdout(t, bd, dir, "parent", "update", child.ID, "--status", "open")
	})
}

// runUpdateOK_qpcbg runs `bd update <id> <args...>` expecting success.
func runUpdateOK_qpcbg(t *testing.T, bd, dir, id string, args ...string) {
	t.Helper()
	full := append([]string{"update", id}, args...)
	cmd := exec.Command(bd, full...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	if stdout, stderr, err := runCommandBuffers(t, cmd); err != nil {
		t.Fatalf("bd update %s %v failed: %v\nstdout:\n%s\nstderr:\n%s", id, args, err, stdout.String(), stderr.String())
	}
}
