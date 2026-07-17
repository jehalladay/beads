package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// beads-cn5: on shared cluster nodes many crew each run `go test`, which by
// default builds and runs with GOMAXPROCS = all cores and no nice level. N crew
// × all-cores oversubscribes the node (measured load 4x nproc) and every crew's
// TDD loop stalls. scripts/test.sh (the crew-local dev runner; CI uses sharding
// via pr-core.sh, not this script) now degrades gracefully:
//
//   - runs `go test` under `nice` (default -n 10) so concurrent crew loops share
//     CPU fairly instead of thrashing (BEADS_TEST_NICE="" disables);
//   - caps per-invocation GOMAXPROCS to ~half the cores (min 2) so N concurrent
//     builds don't each grab every core (BEADS_TEST_GOMAXPROCS="" disables,
//     BEADS_TEST_GOMAXPROCS=<n> overrides).
//
// These tests exercise the command-construction logic via TEST_PRINT_CMD=1,
// which prints the fully-assembled command and exits before running any real
// build — so they are fast and need no toolchain work.

// runTestScriptDryRun runs scripts/test.sh in print-command mode and returns
// the printed command line. extraEnv entries (KEY=VALUE) are appended to the
// child environment.
func runTestScriptDryRun(t *testing.T, repo string, extraEnv ...string) string {
	t.Helper()
	cmd := exec.Command("bash", "scripts/test.sh", "./internal/query/")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "TEST_PRINT_CMD=1")
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test.sh dry-run failed: %v\noutput:\n%s", err, out)
	}
	// The command line is the last non-empty line (earlier lines are the
	// "Running:"/"Skipping:" diagnostics written to stderr, also captured).
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 {
		t.Fatalf("no output from test.sh dry-run:\n%s", out)
	}
	return lines[len(lines)-1]
}

func repoRoot(t *testing.T) string {
	t.Helper()
	// This test lives in <repo>/scripts, so the repo root is one level up from
	// the test's working directory.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Dir(wd)
	if _, err := os.Stat(filepath.Join(root, "scripts", "test.sh")); err != nil {
		t.Fatalf("could not locate scripts/test.sh from %s: %v", root, err)
	}
	return root
}

func TestTestScript_DefaultNicesAndCapsProcs(t *testing.T) {
	repo := repoRoot(t)
	line := runTestScriptDryRun(t, repo)

	if !strings.Contains(line, "nice -n 10") {
		t.Errorf("expected default command to run under `nice -n 10`, got:\n%s", line)
	}
	if !strings.Contains(line, "GOMAXPROCS=") {
		t.Errorf("expected default command to cap GOMAXPROCS, got:\n%s", line)
	}
	if !strings.Contains(line, "go test") {
		t.Errorf("expected command to invoke `go test`, got:\n%s", line)
	}
}

func TestTestScript_NiceDisabledWhenEmpty(t *testing.T) {
	repo := repoRoot(t)
	line := runTestScriptDryRun(t, repo, "BEADS_TEST_NICE=")

	if strings.Contains(line, "nice ") {
		t.Errorf("BEADS_TEST_NICE= should disable nice, got:\n%s", line)
	}
	// GOMAXPROCS cap is independent and should still apply.
	if !strings.Contains(line, "GOMAXPROCS=") {
		t.Errorf("GOMAXPROCS cap should still apply, got:\n%s", line)
	}
}

func TestTestScript_GomaxprocsDisabledWhenEmpty(t *testing.T) {
	repo := repoRoot(t)
	line := runTestScriptDryRun(t, repo, "BEADS_TEST_GOMAXPROCS=")

	if strings.Contains(line, "GOMAXPROCS=") {
		t.Errorf("BEADS_TEST_GOMAXPROCS= should disable the proc cap, got:\n%s", line)
	}
}

func TestTestScript_GomaxprocsOverride(t *testing.T) {
	repo := repoRoot(t)
	line := runTestScriptDryRun(t, repo, "BEADS_TEST_GOMAXPROCS=3")

	if !strings.Contains(line, "GOMAXPROCS=3") {
		t.Errorf("BEADS_TEST_GOMAXPROCS=3 should set GOMAXPROCS=3, got:\n%s", line)
	}
}

func TestTestScript_NiceOverride(t *testing.T) {
	repo := repoRoot(t)
	line := runTestScriptDryRun(t, repo, "BEADS_TEST_NICE=5")

	if !strings.Contains(line, "nice -n 5") {
		t.Errorf("BEADS_TEST_NICE=5 should set `nice -n 5`, got:\n%s", line)
	}
}
