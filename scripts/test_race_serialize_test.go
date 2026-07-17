package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The race tier is the dominant driver of shared-node CPU oversubscription:
// concurrent `go test -race` sweeps (each ~7-10x slower and 30m-timeout)
// grabbed all cores and drove node load to ~4x, stalling every crew's TDD
// loop (beads-cn5). scripts/test.sh serializes the race tier behind a
// node-wide flock and runs it under `nice` so concurrent race sweeps degrade
// gracefully instead of thrashing — the same lock-serialize approach beads-ub3
// used for golangci-lint. These tests pin that wiring via the TEST_PRINT_CMD
// dry-run (prints the assembled command and exits, without running go test).

// dryRunOpts configures a scripts/test.sh dry run.
type dryRunOpts struct {
	args []string // extra CLI args (e.g. "--race")
	env  []string // extra env (e.g. "TEST_RACE_LOCK=")
}

// runTestScriptDryRun runs scripts/test.sh in dry-run mode (TEST_PRINT_CMD=1)
// and returns the printed command line. The env sandbox is disabled so the
// dry run stays fast and hermetic (no temp dirs / dolt / go env probing).
func runTestScriptDryRun(t *testing.T, opts dryRunOpts) string {
	t.Helper()
	root := sourceRepoRoot(t)
	script := filepath.Join(root, "scripts", "test.sh")
	args := append(append([]string{}, opts.args...), "./internal/version")
	cmd := exec.Command(script, args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"TEST_PRINT_CMD=1",
		"BEADS_TEST_ENV_DISABLE=1",
		"CGO_ENABLED=1", // race tier is rejected under CGO_ENABLED=0
	)
	cmd.Env = append(cmd.Env, opts.env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test.sh dry run failed: %v\n%s", err, out)
	}
	return string(out)
}

// printedCmdLine extracts the "CMD: ..." line emitted by the dry run.
func printedCmdLine(t *testing.T, output string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "CMD: ") {
			return strings.TrimPrefix(line, "CMD: ")
		}
	}
	t.Fatalf("no 'CMD: ' line in dry-run output:\n%s", output)
	return ""
}

func TestRaceTierIsSerializedAndNiced(t *testing.T) {
	cmdLine := printedCmdLine(t, runTestScriptDryRun(t, dryRunOpts{args: []string{"--race"}}))

	if !strings.Contains(cmdLine, "flock") {
		t.Errorf("race tier command should be wrapped in flock for node-wide serialization; got:\n%s", cmdLine)
	}
	if !strings.Contains(cmdLine, "nice") {
		t.Errorf("race tier command should run under nice to deprioritize the heavy sweep; got:\n%s", cmdLine)
	}
	// The wrapper must precede the actual `go test ... -race` invocation.
	flockIdx := strings.Index(cmdLine, "flock")
	goTestIdx := strings.Index(cmdLine, "go test")
	if flockIdx < 0 || goTestIdx < 0 || flockIdx > goTestIdx {
		t.Errorf("flock wrapper must precede `go test`; got:\n%s", cmdLine)
	}
	if !strings.Contains(cmdLine, "-race") {
		t.Errorf("dry run under --race should still include the -race flag; got:\n%s", cmdLine)
	}
}

func TestNonRaceTierIsNotSerialized(t *testing.T) {
	cmdLine := printedCmdLine(t, runTestScriptDryRun(t, dryRunOpts{}))

	if strings.Contains(cmdLine, "flock") {
		t.Errorf("non-race tier must NOT be flock-serialized (baseline builds are tolerable); got:\n%s", cmdLine)
	}
	if strings.Contains(cmdLine, "nice") {
		t.Errorf("non-race tier must NOT be niced; got:\n%s", cmdLine)
	}
	if !strings.HasPrefix(cmdLine, "go test") {
		t.Errorf("non-race command should start with `go test`; got:\n%s", cmdLine)
	}
}

func TestRaceSerializationIsOptOut(t *testing.T) {
	// An empty TEST_RACE_LOCK disables the flock serialization (e.g. for a
	// caller that owns scheduling itself or runs on a dedicated host).
	cmdLine := printedCmdLine(t, runTestScriptDryRun(t, dryRunOpts{args: []string{"--race"}, env: []string{"TEST_RACE_LOCK="}}))

	if strings.Contains(cmdLine, "flock") {
		t.Errorf("empty TEST_RACE_LOCK should disable flock serialization; got:\n%s", cmdLine)
	}
	if !strings.Contains(cmdLine, "-race") {
		t.Errorf("opting out of the lock must not drop the -race flag; got:\n%s", cmdLine)
	}
}
