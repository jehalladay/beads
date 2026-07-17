package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// beads-367: `make test-race` (scripts/test.sh --race) inherited the 3m default
// timeout. Race instrumentation is ~7-10x slower, so subprocess/Dolt-backed
// tests hit a FALSE "panic: test timed out" instead of surfacing real races.
// The fix bumps an UNSET timeout to a race-appropriate default (30m, matching
// the nightly full race run) while always honoring an explicit -timeout /
// TEST_TIMEOUT. These tests lock that contract by parsing the `Running: ...`
// line test.sh prints (to stderr) for the assembled `go test` command, using a
// stub `go` so no real test binary runs.

// writeNoopStubGo installs a fake `go` that succeeds immediately for any args,
// so test.sh reaches its "Running: <cmd>" echo without executing a real suite.
// Returns the bin dir to prepend to PATH.
func writeNoopStubGo(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	stub := "#!/usr/bin/env bash\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(stub), 0o755); err != nil {
		t.Fatalf("write stub go: %v", err)
	}
	return bin
}

// runTestSh runs scripts/test.sh with the given extra env and args, returning
// combined output. CGO_ENABLED=1 so the race tier is not rejected.
func runTestSh(t *testing.T, extraEnv []string, args ...string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// scripts/ is the test package dir; test.sh lives here.
	script := filepath.Join(wd, "test.sh")
	if _, err := os.Stat(script); err != nil {
		t.Skipf("test.sh not found at %s: %v", script, err)
	}
	bin := writeNoopStubGo(t)
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	env := append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"CGO_ENABLED=1",
	)
	env = append(env, extraEnv...)
	cmd.Env = env
	out, _ := cmd.CombinedOutput() // stub exits 0; we only assert the Running line
	return string(out)
}

// timeoutFromOutput extracts the "-timeout X" value from the "Running:" line.
func timeoutFromOutput(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		// The "Running:" line may carry a nice/flock prefix on the race tier
		// (beads-cn5), so match the prefix + the embedded `go test`, not a
		// fixed "Running: go test".
		if !strings.HasPrefix(line, "Running:") || !strings.Contains(line, "go test") {
			continue
		}
		fields := strings.Fields(line)
		for i, f := range fields {
			if f == "-timeout" && i+1 < len(fields) {
				return fields[i+1]
			}
		}
	}
	t.Fatalf("no '-timeout <val>' found in test.sh output:\n%s", out)
	return ""
}

func TestRaceTierBumpsUnsetTimeout(t *testing.T) {
	// --race with no explicit timeout must NOT use the 3m default (that is the
	// beads-367 false-timeout bug); it must use the race default (30m).
	out := runTestSh(t, nil, "--race", "./internal/version/...")
	if got := timeoutFromOutput(t, out); got != "30m" {
		t.Errorf("race tier default timeout = %q, want 30m (beads-367)", got)
	}
}

func TestRaceTierHonorsExplicitFlagTimeout(t *testing.T) {
	// An explicit -timeout must always win over the race bump.
	out := runTestSh(t, nil, "--race", "-timeout", "5m", "./internal/version/...")
	if got := timeoutFromOutput(t, out); got != "5m" {
		t.Errorf("explicit -timeout under --race = %q, want 5m", got)
	}
}

func TestRaceTierHonorsExplicitEnvTimeout(t *testing.T) {
	// TEST_TIMEOUT is an explicit choice and must win over the race bump.
	out := runTestSh(t, []string{"TEST_TIMEOUT=10m"}, "--race", "./internal/version/...")
	if got := timeoutFromOutput(t, out); got != "10m" {
		t.Errorf("TEST_TIMEOUT under --race = %q, want 10m", got)
	}
}

func TestNonRaceTimeoutUnchanged(t *testing.T) {
	// Without --race the default stays 3m (no regression).
	out := runTestSh(t, nil, "./internal/version/...")
	if got := timeoutFromOutput(t, out); got != "3m" {
		t.Errorf("non-race default timeout = %q, want 3m", got)
	}
}

func TestRaceTimeoutIsConfigurable(t *testing.T) {
	// TEST_RACE_TIMEOUT lets an operator tune the race default.
	out := runTestSh(t, []string{"TEST_RACE_TIMEOUT=20m"}, "--race", "./internal/version/...")
	if got := timeoutFromOutput(t, out); got != "20m" {
		t.Errorf("TEST_RACE_TIMEOUT override = %q, want 20m", got)
	}
}
