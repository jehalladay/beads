package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// beads-qre: the pre-commit hook ran `golangci-lint … --allow-serial-runners`,
// which waits on golangci-lint's HARDCODED global /tmp/golangci-lint.lock. But
// that wait is counted against golangci's own analysis timeout, so under
// concurrent-commit load a queued run's timeout elapses WHILE it is still
// blocked on the lock — the hook rejects the commit (rc=1) even though analysis
// never started, pushing crew back to `git commit --no-verify` (the exact
// gate-bypass the hook exists to prevent).
//
// Fix: the hook acquires a bounded flock (`flock -w <wait>`) OUTSIDE the linter
// timeout, so the lock-wait no longer eats the analysis budget. If the bounded
// wait is exceeded (another sweep held the lock too long), the hook
// DEGRADES-TO-WARN and lets the commit proceed — same philosophy as the
// existing "couldn't obtain the pinned linter" fallback; CI remains the hard
// gate. These tests drive .githooks/pre-commit with a stub linter and a
// deliberately-held lock to pin that contract.

// runPrecommitHook runs .githooks/pre-commit in a throwaway git repo containing
// one staged .go file, with a stub `golangci-lint` (and stub `go`/`gofmt`) on
// PATH. Returns combined output and the exit error (nil on success).
func runPrecommitHook(t *testing.T, linterBody string, extraEnv ...string) (string, error) {
	t.Helper()

	hookSrc := filepath.Join(sourceRepoRoot(t), ".githooks", "pre-commit")
	hookData, err := os.ReadFile(hookSrc)
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}

	repo := t.TempDir()
	// A minimal git repo with one staged Go file so the hook has work to do.
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "user.email", "t@t.local")
	runGit(t, repo, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "a.go")
	// A base commit so --new-from-rev=HEAD has a HEAD to diff against.
	runGit(t, repo, "commit", "-q", "-m", "base", "--no-verify")
	// Re-stage a change so the hook sees staged Go files.
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n\n// change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "a.go")

	hook := filepath.Join(repo, "pre-commit")
	if err := os.WriteFile(hook, hookData, 0o755); err != nil {
		t.Fatal(err)
	}

	// Stub bin dir. golangci-lint reports the pinned version so resolve_linter
	// path 1 selects it; a stub `go`/`gofmt` keep the hook hermetic.
	bin := filepath.Join(repo, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(bin, "golangci-lint"), linterBody)
	writeExecutable(t, filepath.Join(bin, "gofmt"), "#!/bin/sh\nexit 0\n")
	// `go env GOPATH` is consulted; everything else is a no-op success.
	writeExecutable(t, filepath.Join(bin, "go"), "#!/bin/sh\nif [ \"$1\" = env ]; then echo /tmp/gopath-stub; exit 0; fi\nexit 0\n")

	cmd := exec.Command(hook)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

// pinnedLinterStub is a golangci-lint stub reporting the version the hook pins.
func pinnedLinterStub(t *testing.T, runBody string) string {
	t.Helper()
	pin := readPinnedVersion(t)
	return "#!/bin/sh\n" +
		"if [ \"$1\" = --version ]; then echo \"golangci-lint has version " + pin + " built from …\"; exit 0; fi\n" +
		"if [ \"$1\" = run ]; then\n" + runBody + "\nfi\n" +
		"exit 0\n"
}

// readPinnedVersion pulls PINNED_BARE out of the hook so the stub always
// matches whatever the hook currently pins.
func readPinnedVersion(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(sourceRepoRoot(t), ".githooks", "pre-commit"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "PINNED_VERSION=") {
			v := strings.Trim(strings.TrimPrefix(line, "PINNED_VERSION="), "\"' ")
			return strings.TrimPrefix(v, "v")
		}
	}
	t.Fatal("PINNED_VERSION not found in hook")
	return ""
}

// TestPrecommitLintPassesWhenLockFree: with no lock contention, a clean linter
// run lets the hook succeed.
func TestPrecommitLintPassesWhenLockFree(t *testing.T) {
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skip("flock not available")
	}
	out, err := runPrecommitHook(t, pinnedLinterStub(t, "exit 0"),
		"BEADS_PRECOMMIT_LINT_LOCK=/tmp/beads-qre-test-free.lock")
	if err != nil {
		t.Fatalf("hook should pass when lint is clean and lock is free: %v\n%s", err, out)
	}
}

// TestPrecommitLintStillBlocksOnRealFailure: a genuine lint failure (linter
// runs and exits non-zero) must still block the commit — the serialization
// wrapper must not swallow real findings. This guards against the degrade path
// masking actual failures.
func TestPrecommitLintStillBlocksOnRealFailure(t *testing.T) {
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skip("flock not available")
	}
	out, err := runPrecommitHook(t,
		pinnedLinterStub(t, "echo 'a.go:1: real finding' >&2; exit 1"),
		"BEADS_PRECOMMIT_LINT_LOCK="+filepath.Join(t.TempDir(), "free.lock"))
	if err == nil {
		t.Fatalf("hook must block (non-zero) on a real lint failure; got success:\n%s", out)
	}
}

// TestPrecommitLintDegradesToWarnWhenLockHeld: when the serialization lock is
// held longer than the bounded wait, the hook must NOT block the commit — it
// degrades to warn (exit 0) rather than forcing --no-verify.
func TestPrecommitLintDegradesToWarnWhenLockHeld(t *testing.T) {
	flockPath, err := exec.LookPath("flock")
	if err != nil {
		t.Skip("flock not available")
	}

	lock := filepath.Join(t.TempDir(), "held.lock")
	// Hold the lock for 10s in the background.
	holder := exec.Command(flockPath, lock, "-c", "sleep 10")
	if err := holder.Start(); err != nil {
		t.Fatalf("start lock holder: %v", err)
	}
	defer func() {
		_ = holder.Process.Kill()
		_, _ = holder.Process.Wait()
	}()
	// Give the holder a moment to actually acquire the lock.
	waitForLockHeld(t, flockPath, lock)

	// The linter stub would FAIL if it ran (so a "block" would be observable),
	// but it must never run because the bounded wait (1s) expires first.
	out, err := runPrecommitHook(t,
		pinnedLinterStub(t, "echo LINTER-RAN >&2; exit 1"),
		"BEADS_PRECOMMIT_LINT_LOCK="+lock,
		"BEADS_PRECOMMIT_LINT_LOCK_WAIT=1",
	)
	if err != nil {
		t.Fatalf("hook must degrade-to-warn (exit 0), not block, when the lock is held past the wait: %v\n%s", err, out)
	}
	if strings.Contains(out, "LINTER-RAN") {
		t.Fatalf("linter should NOT have run while the lock was held; got:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "lint") {
		t.Fatalf("degraded run should print a warning mentioning lint; got:\n%s", out)
	}
}

// waitForLockHeld blocks until `lock` is observably held (a non-blocking
// acquire fails), so the test doesn't race the background holder.
func waitForLockHeld(t *testing.T, flockPath, lock string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		c := exec.Command(flockPath, "-n", lock, "-c", "true")
		if err := c.Run(); err != nil {
			return // acquire failed => lock is held
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("background holder never acquired the lock")
}
