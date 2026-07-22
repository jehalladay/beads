//go:build cgo

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestConfigEnumBrickM83zh guards beads-m83zh: an out-of-domain value for an
// enum config key (dolt.auto-commit, valid: off|on|batch) must NOT be able to
// brick the workspace.
//
// dolt.auto-commit is consumed by getDoltAutoCommitMode() from
// PersistentPreRunE on EVERY command. Before the fix, `bd config set` wrote any
// string, and getDoltAutoCommitMode hard-failed on an out-of-domain value — so
// a bad persisted value made every command (including the `bd config set` that
// would fix it) error before its RunE ran, bricking the whole workspace.
//
// Two legs are exercised end-to-end:
//   - Leg 1 (write-path): `bd config set dolt.auto-commit garbage` fails and
//     persists nothing; the workspace stays usable.
//   - Leg 2 (load-path resilience): an already-persisted bad value (as an older
//     binary could have written) degrades to the safe default with a warning so
//     `bd list` still works, and `bd config set dolt.auto-commit off` recovers.
//   - Leg 2b (explicit flag): a per-invocation `--dolt-auto-commit=garbage`
//     flag is user-supplied and SHOULD hard-fail loudly (rc≠0).
func TestConfigEnumBrickM83zh(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt config tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	t.Run("set_rejects_bad_enum_and_persists_nothing", func(t *testing.T) {
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "m83zh1")

		// Leg 1: the write-path rejects an out-of-domain value.
		out := bdConfigFail(t, bd, dir, "set", "dolt.auto-commit", "garbage")
		if !strings.Contains(out, "invalid value") || !strings.Contains(out, "off, on, batch") {
			t.Fatalf("expected an enum-domain rejection listing valid values, got:\n%s", out)
		}

		// Nothing may have been persisted.
		cfg := readConfigYAML(t, beadsDir)
		if strings.Contains(cfg, "garbage") {
			t.Fatalf("rejected value must NOT persist to config.yaml:\n%s", cfg)
		}

		// The workspace must remain usable (this is the brick that m83zh guards).
		if _, err := bdRunWithFlockRetry(t, bd, dir, "list"); err != nil {
			t.Fatalf("bd list should succeed after a rejected config set (workspace must not be bricked): %v", err)
		}

		// A valid value still sets cleanly.
		bdConfig(t, bd, dir, "set", "dolt.auto-commit", "batch")
		if got := readConfigYAML(t, beadsDir); !strings.Contains(got, "dolt.auto-commit: \"batch\"") {
			t.Fatalf("valid enum value should persist; config.yaml:\n%s", got)
		}
	})

	t.Run("bad_persisted_value_degrades_and_recovers", func(t *testing.T) {
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "m83zh2")

		// Leg 2: simulate a bad value persisted by an older binary that lacked
		// the write-path guard.
		cfgPath := filepath.Join(beadsDir, "config.yaml")
		appendToFile(t, cfgPath, "dolt.auto-commit: \"garbage\"\n")

		// bd must still run: degrade to the safe default with a warning, not brick.
		cmd := exec.Command(bd, "list")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd list must survive a bad persisted dolt.auto-commit (self-recover, not brick): %v\nstdout:\n%s\nstderr:\n%s",
				err, stdout.String(), stderr.String())
		}
		if !strings.Contains(stderr.String(), "invalid dolt.auto-commit") {
			t.Fatalf("expected a load-path warning about the bad persisted value on stderr, got:\n%s", stderr.String())
		}

		// And the workspace can be repaired via the normal command.
		bdConfig(t, bd, dir, "set", "dolt.auto-commit", "off")
		if got := readConfigYAML(t, beadsDir); !strings.Contains(got, "dolt.auto-commit: \"off\"") {
			t.Fatalf("bd config set should recover the bad value; config.yaml:\n%s", got)
		}
	})

	t.Run("explicit_bad_flag_hard_fails", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "m83zh3")

		// Leg 2b: an explicit per-invocation flag is user-supplied and must
		// hard-fail loudly (unlike a bad persisted config value).
		cmd := exec.Command(bd, "--dolt-auto-commit=garbage", "list")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err == nil {
			t.Fatalf("an explicit --dolt-auto-commit=garbage flag must hard-fail; stdout:\n%s\nstderr:\n%s",
				stdout.String(), stderr.String())
		}
		if !strings.Contains(stderr.String(), "invalid --dolt-auto-commit") {
			t.Fatalf("expected an explicit-flag rejection on stderr, got:\n%s", stderr.String())
		}
	})
}

// readConfigYAML returns the contents of .beads/config.yaml (fatals on error).
func readConfigYAML(t *testing.T, beadsDir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(beadsDir, "config.yaml"))
	if err != nil {
		t.Fatalf("read config.yaml: %v", err)
	}
	return string(b)
}

// appendToFile appends s to the file at path (fatals on error).
func appendToFile(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open %s for append: %v", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatalf("append to %s: %v", path, err)
	}
}
