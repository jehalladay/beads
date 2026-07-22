//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// beads-reopen-json: `bd reopen <id> --json` where the SOLE failure is an
// integrity-guard rejection on an EXISTING id (closed-parent / superseded /
// duplicate) must surface the REAL reason on stdout — not the generic "no
// issues reopened matching the provided IDs", which misleads a --json consumer
// into thinking the ID did not exist and applying the WRONG remediation.
//
// reopen.go defers each guard reason into deferredItemErrors via
// reportReopenItemError, but the deferred FLUSH is guarded by
// len(reopenedIssues) > 0 (PARTIAL-SUCCESS ONLY). On a wholly-failed batch
// (nothing reopened) that flush is skipped and the terminal all-failed --json
// path returned the generic no-match, so the real reason reached only stderr.
// This is the standalone-`bd reopen` sibling of the update path fixed by
// beads-9c0o/qpcbg and the `bd close` path fixed by beads-quodm.
//
// Preserves the beads-92tz one-object contract (single JSON object on stdout,
// no competing JSON object on stderr).

// assertReopenGuardReasonOnStdout runs `bd reopen <args...> --json` expecting a
// non-zero exit and asserts the stdout JSON error carries wantReason and NOT the
// generic no-match string, with a clean (non-JSON) stderr.
func assertReopenGuardReasonOnStdout(t *testing.T, bd, dir, wantReason string, args ...string) {
	t.Helper()
	full := append(append([]string{"reopen"}, args...), "--json")
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
	// MUTATION-VERIFY: drop the strings.Join(deferredItemErrors) branch from the
	// terminal all-failed --json path and this FAILS — the guard reason goes only
	// to stderr, and the all-failed path emits the generic no-match on stdout.
	if strings.Contains(errVal, "no issues reopened matching the provided IDs") {
		t.Errorf("--json error masks the guard reason with the generic no-match message (beads-reopen-json): %q", errVal)
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

func TestEmbeddedReopenGuardReasonJSON(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt reopen tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// Closed-parent guard: reopening a closed child whose parent epic is also
	// closed. `bd reopen C --json` (no --force) must surface "parent", not the
	// generic no-match.
	t.Run("closed_parent_guard_reason_on_stdout", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "rgp")
		epic := bdCreate(t, bd, dir, "reopen parent epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "reopen child", "--type", "task")
		bdDepAdd(t, bd, dir, child.ID, epic.ID, "--type", "parent-child")
		// Close the child, then the epic (all children closed → epic closeable).
		bdClose(t, bd, dir, child.ID)
		bdClose(t, bd, dir, epic.ID)

		// Reopening the child while its parent epic is closed → guard rejection.
		assertReopenGuardReasonOnStdout(t, bd, dir, "parent", child.ID)
	})
}

// TestProxiedReopenGuardReasonJSON proves the PROXIED `bd reopen --json` handler
// (reopen_proxied_server.go) mirrors the direct fix: on a wholly-failed batch
// whose sole failure is a supersede guard rejection, the terminal stdout JSON
// error carries the REAL "superseded" reason, not the generic no-match. The
// existing efyts LEG 2c only asserts that SOME JSON error object appears on
// stdout; this asserts its CONTENT surfaces the guard reason (the direct-vs-
// proxied twin of the leak this bead fixes).
func TestProxiedReopenGuardReasonJSON(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("supersede_guard_reason_on_stdout", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "prg")
		canonical := bdProxiedCreate(t, bd, p.dir, "reopen-json supersede canonical")
		old := bdProxiedCreate(t, bd, p.dir, "reopen-json supersede old")
		// `bd supersede old --with new` closes old + adds the supersedes edge.
		if _, _, err := bdProxiedRunBuffers(t, bd, p.dir, "supersede", old.ID, "--with", canonical.ID); err != nil {
			t.Skipf("could not set up supersede edge in proxied mode (setup, not the SUT): %v", err)
		}
		// Reopening old (no --force) is refused by the supersede guard; wholly
		// failed → the terminal stdout JSON error must carry the reason.
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "reopen", old.ID, "--json")
		if err == nil {
			t.Fatalf("expected non-zero exit for supersede guard; stdout=%s stderr=%s", stdout, stderr)
		}
		out := strings.TrimSpace(stdout)
		start := strings.Index(out, "{")
		if start < 0 {
			t.Fatalf("stdout has no JSON object: %s", out)
		}
		var obj map[string]any
		if jerr := json.Unmarshal([]byte(out[start:]), &obj); jerr != nil {
			t.Fatalf("stdout is not a JSON object: %v\nstdout:\n%s", jerr, out)
		}
		errVal, _ := obj["error"].(string)
		if errVal == "" {
			if data, ok := obj["data"].(map[string]any); ok {
				errVal, _ = data["error"].(string)
			}
		}
		// MUTATION-VERIFY: drop the strings.Join(deferredItemErrors) branch from
		// the proxied terminal path and this FAILS with the generic no-match.
		if strings.Contains(errVal, "no issues reopened matching the provided IDs") {
			t.Errorf("proxied --json error masks the guard reason with the generic no-match (beads-reopen-json): %q", errVal)
		}
		if !strings.Contains(errVal, "superseded") {
			t.Errorf("expected the real 'superseded' guard reason in the proxied --json error, got: %q", errVal)
		}
	})
}
