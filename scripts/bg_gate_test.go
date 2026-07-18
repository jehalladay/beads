package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// bg-gate.sh (beads-l3po) is a detached, pollable runner for the slow forge
// gate. The refinery today watches the ~10min full-suite+embedded-dolt gate
// INLINE, so the streamed build log fills its context window before it can
// push — the fresh instance ctx-climbs to the watchdog restart band mid-gate
// and re-loops (the rig-wide saturation root, not a wedge). bg-gate.sh lets the
// invoker LAUNCH the gate detached and POLL a tiny status file instead of
// holding the build inline: `start` returns immediately with a run dir and
// emits no build output; `status` prints exactly one word; the full log is only
// surfaced (bounded) via `log` on failure. So the invoker's context stays flat
// regardless of how verbose the underlying gate is.
//
// These tests inject a STUB gate via BEADS_BG_GATE_CMD (default is the real
// scripts/ci/forge-gate.sh) so no real build runs. The stub's behavior
// (pass/fail, line count) is controlled by env so we can assert the two
// properties that matter:
//
//  1. BOUNDED INVOKER OUTPUT: `start` prints only the run dir (1 line) and
//     `status` prints only one word, no matter how many lines the gate emits —
//     this is what keeps refinery ctx from climbing.
//  2. CORRECT TERMINAL STATUS: a passing gate ends `passed` (exit 0 from
//     `wait`), a failing gate ends `failed` (non-zero from `wait`), and the
//     bounded `log --tail` surfaces the failure diagnosis.

// bgGatePath returns the absolute path to scripts/ci/bg-gate.sh, failing the
// test if it is not present (RED before the script is implemented).
func bgGatePath(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	p := filepath.Join(root, "scripts", "ci", "bg-gate.sh")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("scripts/ci/bg-gate.sh not found: %v", err)
	}
	return p
}

// writeStubGate installs a fake gate script that emits `lines` lines of noise
// to stdout+stderr and exits with `code`. Returns the script path to feed to
// BEADS_BG_GATE_CMD.
func writeStubGate(t *testing.T, lines, code int) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "stub-gate.sh")
	// Emit a lot of output to stand in for a ~10min build's log volume, then
	// exit with the requested code. A distinctive marker on the last line lets
	// the `log --tail` test assert the tail is real gate output.
	stub := `#!/usr/bin/env bash
n=` + strconv.Itoa(lines) + `
for i in $(seq 1 "$n"); do
  echo "gate build noise line $i"
done
echo "GATE_LAST_LINE_MARKER"
exit ` + strconv.Itoa(code) + `
`
	if err := os.WriteFile(p, []byte(stub), 0o755); err != nil {
		t.Fatalf("write stub gate: %v", err)
	}
	return p
}

// runBG runs bg-gate.sh with the given args and env, returning combined output.
func runBG(t *testing.T, args []string, extraEnv ...string) (string, error) {
	t.Helper()
	root := repoRoot(t)
	cmd := exec.Command("bash", append([]string{filepath.Join(root, "scripts", "ci", "bg-gate.sh")}, args...)...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), extraEnv...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// waitTerminal polls `status <rundir>` until it is no longer "running" or the
// deadline elapses, returning the final status word.
func waitTerminal(t *testing.T, base, rundir string, deadline time.Duration) string {
	t.Helper()
	stop := time.Now().Add(deadline)
	for time.Now().Before(stop) {
		out, err := runBG(t, []string{"status", rundir}, "BEADS_BG_GATE_DIR="+base)
		if err != nil {
			t.Fatalf("status failed: %v\n%s", err, out)
		}
		s := strings.TrimSpace(out)
		if s != "running" {
			return s
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("gate did not reach terminal status within %s", deadline)
	return ""
}

func TestBGGate_StartIsDetachedAndBounded(t *testing.T) {
	bgGatePath(t)
	base := t.TempDir()
	gate := writeStubGate(t, 5000, 0) // 5000 lines of "build log"

	out, err := runBG(t, []string{"start"},
		"BEADS_BG_GATE_DIR="+base,
		"BEADS_BG_GATE_CMD="+gate,
	)
	if err != nil {
		t.Fatalf("start failed: %v\n%s", err, out)
	}
	// PROPERTY 1: start must NOT stream the gate's output. The invoker sees at
	// most the run dir path — a handful of lines, never the 5000-line log.
	startLines := strings.Split(strings.TrimSpace(out), "\n")
	if len(startLines) > 3 {
		t.Fatalf("start emitted %d lines (expected <=3, the run dir); the gate log is leaking into the invoker's context:\n%s", len(startLines), out)
	}
	rundir := strings.TrimSpace(startLines[len(startLines)-1])
	if rundir == "" {
		t.Fatalf("start did not print a run dir:\n%s", out)
	}
	if strings.Contains(out, "build noise line") {
		t.Fatalf("start leaked gate build output into invoker stdout — refinery ctx would climb:\n%s", out)
	}

	// The gate should reach a terminal PASS, and every status poll is one word.
	final := waitTerminal(t, base, rundir, 30*time.Second)
	if final != "passed" {
		t.Fatalf("expected terminal status 'passed', got %q", final)
	}
	sout, _ := runBG(t, []string{"status", rundir}, "BEADS_BG_GATE_DIR="+base)
	if got := strings.TrimSpace(sout); got != "passed" {
		t.Fatalf("status = %q, want passed", got)
	}
	if len(strings.Fields(strings.TrimSpace(sout))) != 1 {
		t.Fatalf("status output is not a single bounded word:\n%s", sout)
	}
}

func TestBGGate_FailingGateReportsFailedAndBoundedLog(t *testing.T) {
	bgGatePath(t)
	base := t.TempDir()
	gate := writeStubGate(t, 3000, 7) // non-zero exit

	out, err := runBG(t, []string{"start"},
		"BEADS_BG_GATE_DIR="+base,
		"BEADS_BG_GATE_CMD="+gate,
	)
	if err != nil {
		t.Fatalf("start failed: %v\n%s", err, out)
	}
	rundir := strings.TrimSpace(out)

	final := waitTerminal(t, base, rundir, 30*time.Second)
	if final != "failed" {
		t.Fatalf("expected terminal status 'failed', got %q", final)
	}

	// PROPERTY 2: the failure diagnosis is bounded — `log --tail 10` returns at
	// most 10 lines and includes the real gate tail marker.
	logOut, err := runBG(t, []string{"log", rundir, "--tail", "10"}, "BEADS_BG_GATE_DIR="+base)
	if err != nil {
		t.Fatalf("log failed: %v\n%s", err, logOut)
	}
	logLines := strings.Split(strings.TrimSpace(logOut), "\n")
	if len(logLines) > 10 {
		t.Fatalf("log --tail 10 returned %d lines (must be bounded to 10):\n%s", len(logLines), logOut)
	}
	if !strings.Contains(logOut, "GATE_LAST_LINE_MARKER") {
		t.Fatalf("log --tail did not surface the real gate tail:\n%s", logOut)
	}
}

func TestBGGate_WaitBlocksAndExitCodeReflectsResult(t *testing.T) {
	bgGatePath(t)
	base := t.TempDir()

	// passing gate → wait exits 0
	gpass := writeStubGate(t, 100, 0)
	out, err := runBG(t, []string{"start"}, "BEADS_BG_GATE_DIR="+base, "BEADS_BG_GATE_CMD="+gpass)
	if err != nil {
		t.Fatalf("start(pass) failed: %v\n%s", err, out)
	}
	rundir := strings.TrimSpace(out)
	wout, werr := runBG(t, []string{"wait", rundir, "--timeout", "30"}, "BEADS_BG_GATE_DIR="+base)
	if werr != nil {
		t.Fatalf("wait on passing gate should exit 0, got err %v\n%s", werr, wout)
	}
	if got := strings.TrimSpace(wout); !strings.Contains(got, "passed") {
		t.Fatalf("wait(pass) final line = %q, want to contain 'passed'", got)
	}

	// failing gate → wait exits non-zero
	gfail := writeStubGate(t, 100, 5)
	out2, err := runBG(t, []string{"start"}, "BEADS_BG_GATE_DIR="+base, "BEADS_BG_GATE_CMD="+gfail)
	if err != nil {
		t.Fatalf("start(fail) failed: %v\n%s", err, out2)
	}
	rundir2 := strings.TrimSpace(out2)
	_, werr2 := runBG(t, []string{"wait", rundir2, "--timeout", "30"}, "BEADS_BG_GATE_DIR="+base)
	if werr2 == nil {
		t.Fatalf("wait on failing gate must exit non-zero, got nil")
	}
}
