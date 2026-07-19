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

// TestReadonlyCentralGate is the behavioral teeth for beads-tjlq: the central
// default-deny gate must block write commands that had NO per-verb
// CheckReadonly guard at all (the leak-by-omission that q634's per-verb approach
// couldn't prevent). `bd branch <name>` (creates a branch) and `bd doctor --fix`
// (mutates the DB) both shipped unguarded; the central gate now refuses them.
// Reads (including conditional-write verbs invoked in their read form) still run.
func TestReadonlyCentralGate(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "cg")

	t.Run("branch_create_blocked", func(t *testing.T) {
		bdReadonlyExpectBlocked(t, bd, dir, "branch", "cg-leak-branch")
		// Must NOT have created the branch: bare `bd branch` lists branches.
		out, _ := runBDInReadDir(t, bd, dir, "branch")
		if strings.Contains(out, "cg-leak-branch") {
			t.Errorf("branch created under --readonly (beads-tjlq central-gate hole): %s", out)
		}
	})

	t.Run("doctor_fix_blocked", func(t *testing.T) {
		// doctor --fix mutates the database; --readonly must refuse it before
		// any repair runs. (Bare `bd doctor` diagnostics are also gated — the
		// whole command is a conditional write with no per-verb guard, so the
		// central gate treats it as a write; sandboxed callers use read verbs.)
		bdReadonlyExpectBlocked(t, bd, dir, "doctor", "--fix", "--yes")
	})

	t.Run("config_apply_blocked", func(t *testing.T) {
		// config apply (no --dry-run) applies config/hooks — a write with no
		// pre-tjlq per-verb guard.
		bdReadonlyExpectBlocked(t, bd, dir, "config", "apply")
	})

	// Controls: representative reads (and a conditional-write verb in read form)
	// must still succeed under --readonly.
	for _, args := range [][]string{
		{"count"},
		{"query", "status=open"},
		{"config", "get", "issue.prefix"},
		{"ready"}, // conditional write only with --claim; plain form is a read
	} {
		args := args
		t.Run("read_allowed_"+strings.Join(args, "_"), func(t *testing.T) {
			cmd := exec.Command(bd, append([]string{"--readonly"}, args...)...)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("bd --readonly %s (a read) must succeed, got err=%v:\n%s",
					strings.Join(args, " "), err, out)
			}
		})
	}
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
