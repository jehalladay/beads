//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// beads (sibling of beads-9c0o/qpcbg, `bd update` all-failed --json path):
// `bd close <id> --json` where the SOLE failure is an integrity-guard rejection
// (open-child / blocked-by / gate) must surface the REAL reason on stdout — not
// the generic "no issues closed matching the provided IDs", which misleads a
// --json consumer parsing stdout into thinking the ID did not exist when the
// real cause was a guard rejection on an EXISTING id.
//
// close.go defers per-item guard reasons into deferredItemErrors, but the
// deferred flush is guarded by len(closedIssues) > 0 (partial-success only). On
// a WHOLLY-failed batch that flush is skipped, so the reasons reached only
// stderr while stdout got the generic no-match message. The fix joins
// deferredItemErrors into the terminal all-failed --json error, mirroring
// 9c0o/qpcbg's fix for the update verb. Preserves the beads-92tz one-object
// contract (single JSON object on stdout, no competing JSON object on stderr).
//
// MUTATION-VERIFY: revert the terminal all-failed path to the bare generic
// HandleErrorRespectJSON("no issues closed matching the provided IDs") and this
// FAILS — the guard reason goes only to stderr, deferredItemErrors is dropped,
// and stdout leaks the generic no-match string instead of the real reason.

// assertCloseGuardReasonOnStdout runs `bd close <args...> --json` expecting a
// non-zero exit and asserts the stdout JSON error carries wantReason and NOT the
// generic no-match string, with a clean (non-JSON) stderr.
func assertCloseGuardReasonOnStdout(t *testing.T, bd, dir, wantReason string, args ...string) {
	t.Helper()
	full := append(append([]string{"close"}, args...), "--json")
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
	if strings.Contains(errVal, "no issues closed matching the provided IDs") {
		t.Errorf("--json error masks the guard reason with the generic no-match message: %q", errVal)
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

func TestEmbeddedCloseGuardReasonJSON(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt close tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// Open-child close guard: closing an epic with an open child. `bd close E
	// --json` must surface "open child", not the generic no-match.
	t.Run("open_child_guard_reason_on_stdout", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "cgc")
		epic := bdCreate(t, bd, dir, "parent epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "open child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, epic.ID, "--type", "parent-child")

		assertCloseGuardReasonOnStdout(t, bd, dir, "open child", epic.ID)
	})

	// Blocked-by close guard: closing an issue with an open blocker. `bd close X
	// --json` must surface "blocked by", not the generic no-match.
	t.Run("blocked_by_guard_reason_on_stdout", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "cgb")
		blocker := bdCreate(t, bd, dir, "the blocker", "--type", "task")
		target := bdCreate(t, bd, dir, "the target", "--type", "task")
		// target is blocked by blocker (blocker must close first) — same idiom
		// as close_json_stderr_test.go's n96g seeding.
		bdDep(t, bd, dir, "add", target.ID, "--blocked-by", blocker.ID)

		assertCloseGuardReasonOnStdout(t, bd, dir, "blocked by", target.ID)
	})
}
