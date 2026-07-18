package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// beads-l3po: the shared-test-server speedup (BEADS_TEST_SHARED_SERVER=1) was
// silently DISABLED on any node without `nc` — the readiness check used `nc -z`,
// which errors "command not found" so the probe never succeeds and test.sh falls
// back to per-package dolt servers (the slow ~10min merge gate). The fix routes
// readiness through beads_port_open (scripts/ci/lib/port-open.sh: nc → bash
// /dev/tcp → python3), so activation no longer HARD-depends on nc.
//
// This is the END-TO-END guard: it runs test.sh's real shared-server bring-up
// and asserts the server STARTS ("Shared test Dolt server started") rather than
// FALLING BACK ("failed to start, falling back to per-package"). The nc-less
// path is what matters, and the /fsx refinery nodes (this test's target) ship no
// nc — so on those nodes this runs the real regression with the real PATH.
//
// We deliberately do NOT curate an artificial minimal PATH to "simulate" nc
// absence: test.sh transitively needs many coreutils (paste, grep, sed, dolt,
// go, ...) and an allowlist is brittle (a missing tool fails test.sh for an
// unrelated reason, not the probe). Instead: run with the real environment; if
// nc happens to be installed on the runner we SKIP (can't robustly remove it
// without breaking test.sh), and the unit-level TestPortOpen_* tests cover the
// /dev/tcp and python backends deterministically on every box.
func TestSharedServer_ReadinessProbeWorksWithoutNc(t *testing.T) {
	// OPT-IN ONLY (beads-l3po / z4lt gate-fail). This E2E forks a full nested
	// `scripts/test.sh` run (embedded dolt + shared-server bring-up) that fires
	// on exactly the merge-gate node profile (nc absent + dolt + python3). Run
	// as part of the default `make test`, it self-fires a heavyweight nested
	// suite inside the already-parallel outer full-suite and fails
	// deterministically under contention — bouncing every MR through the gate.
	// The per-backend readiness logic is covered deterministically on every box
	// by the TestPortOpen_* unit tests; this end-to-end run is a bonus, so we
	// gate it behind an explicit opt-in and skip by default.
	if os.Getenv("BEADS_L3PO_E2E") != "1" {
		t.Skip("opt-in E2E: set BEADS_L3PO_E2E=1 to run the nested shared-server bring-up (excluded from the default gate to avoid a nested-suite self-fire; TestPortOpen_* covers the backends)")
	}

	repo := repoRoot(t)

	if _, err := exec.LookPath("dolt"); err != nil {
		t.Skip("dolt not on PATH; shared-server bring-up needs a real dolt")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH; test.sh port-pick needs it")
	}
	if _, err := exec.LookPath("nc"); err == nil {
		t.Skip("nc present on this runner; cannot robustly simulate the nc-less /fsx node here — the /dev/tcp + python backends are covered by TestPortOpen_*")
	}

	// Real environment (nc is genuinely absent here). Run a single fast
	// embedded-dolt test under shared-server mode and assert activation.
	//
	// Assertion signal: test.sh writes the shared-server OUTCOME to the file named
	// by BEADS_TEST_SHARED_SERVER_OUTCOME_FILE — "activated <port>" on the success
	// branch, "fallback" on the nc-absence-style fallback. We assert "activated",
	// which is the robust functional signal (the human-readable ">&2" start
	// message can be reordered/interleaved with the go-test stream under the EXIT
	// trap, so we don't parse stdout/stderr text).
	outcomeFile := filepath.Join(t.TempDir(), "outcome")
	cmd := exec.Command("bash", "scripts/test.sh", "-run", "^TestEmbeddedChildren$", "-count=1", "./cmd/bd/")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(),
		"BEADS_TEST_SHARED_SERVER=1",
		"BEADS_TEST_EMBEDDED_DOLT=1",
		"BEADS_TEST_SHARED_SERVER_OUTCOME_FILE="+outcomeFile,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test.sh under shared-server mode failed: %v\n%s", err, tailLines(string(out), 20))
	}

	data, readErr := os.ReadFile(outcomeFile)
	if readErr != nil {
		t.Fatalf("shared-server outcome file not written (block never reached success/fallback branch): %v\n%s", readErr, tailLines(string(out), 20))
	}
	outcome := strings.TrimSpace(string(data))
	if strings.HasPrefix(outcome, "fallback") {
		t.Fatalf("shared server fell back to per-package despite the beads_port_open probe (nc-absence regression): outcome=%q\n%s", outcome, tailLines(string(out), 20))
	}
	if !strings.HasPrefix(outcome, "activated") {
		t.Fatalf("shared server did not activate without nc: outcome=%q\n%s", outcome, tailLines(string(out), 20))
	}
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
