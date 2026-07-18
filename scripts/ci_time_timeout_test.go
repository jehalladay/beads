package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// beads-0lu9: ci_time can bound a wrapped command with an EXTERNAL OS-level
// timeout (BEADS_CI_STEP_TIMEOUT), so a hung/blocked step (e.g. golangci-lint at
// 0% CPU holding the shared flock — the 2026-07-18 refinery-gate wedge) is
// killed instead of stalling the whole merge queue. A tool's OWN --timeout only
// covers active work, not a process blocked on I/O; the external timeout covers
// that gap.

// runCiTime sources scripts/ci/lib/timing.sh and invokes ci_time with the given
// env, returning combined output, the elapsed wall time, and the exit error.
func runCiTime(t *testing.T, env []string, ciTimeArgs string) (string, time.Duration, error) {
	t.Helper()
	timingSh := filepath.Join(sourceRepoRoot(t), "scripts", "ci", "lib", "timing.sh")
	script := "source " + timingSh + "\n" + ciTimeArgs + "\n"
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(), env...)
	start := time.Now()
	out, err := cmd.CombinedOutput()
	return string(out), time.Since(start), err
}

// TestCiTimeExternalTimeoutKillsHungStep: a wrapped command that runs longer
// than BEADS_CI_STEP_TIMEOUT must be terminated at ~the timeout (not run to
// completion), and ci_time must return the timeout exit code (124).
func TestCiTimeExternalTimeoutKillsHungStep(t *testing.T) {
	if _, err := exec.LookPath("timeout"); err != nil {
		t.Skip("timeout not available")
	}
	out, elapsed, err := runCiTime(t,
		[]string{"BEADS_CI_STEP_TIMEOUT=1"},
		`ci_time "hang" -- sleep 20`,
	)
	if err == nil {
		t.Fatalf("ci_time must fail when the wrapped command exceeds the timeout; got success:\n%s", out)
	}
	if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 124 {
		t.Fatalf("expected exit code 124 (timeout), got %v\n%s", err, out)
	}
	// Must have been killed near the 1s bound, not run the full 20s sleep.
	if elapsed > 10*time.Second {
		t.Fatalf("wrapped command was not killed promptly (elapsed %v, expected ~1s)\n%s", elapsed, out)
	}
	if !strings.Contains(out, "beads-0lu9") {
		t.Errorf("expected the timeout notice mentioning beads-0lu9; got:\n%s", out)
	}
}

// TestCiTimeNoTimeoutWhenUnset: without BEADS_CI_STEP_TIMEOUT, behavior is
// unchanged — a normal (fast) command succeeds and ci_time returns 0.
func TestCiTimeNoTimeoutWhenUnset(t *testing.T) {
	out, _, err := runCiTime(t, nil, `ci_time "fast" -- true`)
	if err != nil {
		t.Fatalf("ci_time should succeed for a fast command with no timeout set: %v\n%s", err, out)
	}
	if !strings.Contains(out, "succeeded") {
		t.Errorf("expected success line; got:\n%s", out)
	}
}

// TestCiTimeTimeoutAllowsFastStep: with a generous BEADS_CI_STEP_TIMEOUT, a
// command that finishes well within the bound still succeeds (the timeout only
// kills genuine hangs, not normal runs).
func TestCiTimeTimeoutAllowsFastStep(t *testing.T) {
	if _, err := exec.LookPath("timeout"); err != nil {
		t.Skip("timeout not available")
	}
	out, _, err := runCiTime(t,
		[]string{"BEADS_CI_STEP_TIMEOUT=30"},
		`ci_time "quick" -- true`,
	)
	if err != nil {
		t.Fatalf("a fast command under a generous timeout must still succeed: %v\n%s", err, out)
	}
}
