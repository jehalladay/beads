//go:build cgo

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// bdReadonlyExpectBlocked runs `bd --readonly <args...>` and asserts it exits
// non-zero with the read-only-mode error. Returns combined output. Teeth for
// beads-q634: config set/unset/set-many, import, and vc commit are write
// channels that bypassed the --readonly sandbox (guard was per-issue-verb only).
func bdReadonlyExpectBlocked(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"--readonly"}, args...)
	cmd := exec.Command(bd, full...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("bd --readonly %s should be BLOCKED (exit 1) but exited 0 (beads-q634 bypass):\n%s",
			strings.Join(args, " "), out)
	}
	if !strings.Contains(string(out), "read-only mode") {
		t.Errorf("bd --readonly %s: expected 'read-only mode' error, got:\n%s", strings.Join(args, " "), out)
	}
	return string(out)
}

// TestReadonlyBlocksWriteChannels is the teeth for beads-q634: the write
// channels that bypassed --readonly (config set/unset/set-many, import,
// vc commit) must now be blocked, while reads stay allowed.
func TestReadonlyBlocksWriteChannels(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ro")

	t.Run("config_set_blocked", func(t *testing.T) {
		bdReadonlyExpectBlocked(t, bd, dir, "config", "set", "custom.q634", "yes")
		// Must NOT have persisted: config get returns empty/unset.
		out, _ := runBDInReadDir(t, bd, dir, "config", "get", "custom.q634")
		if strings.Contains(out, "yes") {
			t.Errorf("config set persisted under --readonly (beads-q634 bypass): %s", out)
		}
	})

	t.Run("config_unset_blocked", func(t *testing.T) {
		bdReadonlyExpectBlocked(t, bd, dir, "config", "unset", "custom.anything")
	})

	t.Run("config_set_many_blocked", func(t *testing.T) {
		bdReadonlyExpectBlocked(t, bd, dir, "config", "set-many", "custom.a=1", "custom.b=2")
	})

	t.Run("import_blocked", func(t *testing.T) {
		roimport := filepath.Join(dir, "ro.jsonl")
		line := `{"id":"ro-roimport1","title":"injected","issue_type":"task","status":"open","priority":4}` + "\n"
		if err := os.WriteFile(roimport, []byte(line), 0o644); err != nil {
			t.Fatalf("write import file: %v", err)
		}
		bdReadonlyExpectBlocked(t, bd, dir, "import", roimport)
		// The WORST bypass: import must not have injected the issue. `bd show`
		// on a nonexistent id errors / says not-found; if the injected title
		// appears, import persisted despite --readonly.
		out, _ := runBDInReadDir(t, bd, dir, "show", "ro-roimport1")
		if strings.Contains(out, "injected") {
			t.Errorf("import persisted an issue under --readonly (beads-q634 WORST bypass): %s", out)
		}
	})

	t.Run("vc_commit_blocked", func(t *testing.T) {
		bdReadonlyExpectBlocked(t, bd, dir, "vc", "commit", "-m", "should be blocked")
	})

	t.Run("vc_merge_blocked", func(t *testing.T) {
		// vc merge is a data-plane write; the branch need not exist — the
		// readonly guard runs before any merge work (beads-q634 sibling).
		bdReadonlyExpectBlocked(t, bd, dir, "vc", "merge", "some-branch")
	})

	// Control: a READ must still work under --readonly.
	t.Run("read_still_allowed", func(t *testing.T) {
		cmd := exec.Command(bd, "--readonly", "list")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("bd --readonly list (a read) must succeed, got err=%v:\n%s", err, out)
		}
	})
}

// runBDInReadDir runs a read command (no --readonly) in dir, returning output.
func runBDInReadDir(t *testing.T, bd, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(bd, args...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
