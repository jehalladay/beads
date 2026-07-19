//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestEmbeddedRootFormatValidation is the beads-f34g regression: the hidden
// root `--format` alias (main.go) only honored "json"; every other value fell
// through silently and the command ran as plain text with rc=0 — a false-green
// footgun for a scripted `bd ... --format json | jq` that typos the value. It
// now fails loud on a non-json/non-empty value, matching `bd dep tree --format`
// (beads-n95d) and the silently-ignored-value class (mz2p / pbl7). "json" and
// the no-flag default are unaffected.
func TestEmbeddedRootFormatValidation(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "rfv")
	bdCreate(t, bd, dir, "f34g seed", "--type", "task")

	run := func(t *testing.T, args ...string) (string, error) {
		t.Helper()
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	t.Run("invalid_format_errors", func(t *testing.T) {
		out, err := run(t, "status", "--format", "bogusxyz")
		if err == nil {
			t.Fatalf("expected `bd status --format bogusxyz` to fail, but succeeded:\n%s", out)
		}
		if !strings.Contains(out, "invalid") || !strings.Contains(out, "format") {
			t.Errorf("expected 'invalid ... format' error, got: %s", out)
		}
	})

	t.Run("yaml_not_supported_by_root_alias", func(t *testing.T) {
		// A user carrying a `--format yaml` mental model must get a hard error,
		// not silent default text (the exact false-green in the bead repro).
		out, err := run(t, "status", "--format", "yaml")
		if err == nil {
			t.Fatalf("expected `bd status --format yaml` to fail, but succeeded:\n%s", out)
		}
		if !strings.Contains(out, "invalid") || !strings.Contains(out, "format") {
			t.Errorf("expected 'invalid ... format' error for yaml, got: %s", out)
		}
	})

	t.Run("json_still_succeeds", func(t *testing.T) {
		if out, err := run(t, "status", "--format", "json"); err != nil {
			t.Fatalf("`bd status --format json` should succeed, got err=%v:\n%s", err, out)
		}
	})

	t.Run("json_case_insensitive_still_succeeds", func(t *testing.T) {
		if out, err := run(t, "status", "--format", "JSON"); err != nil {
			t.Fatalf("`bd status --format JSON` should succeed (case-insensitive), got err=%v:\n%s", err, out)
		}
	})

	t.Run("no_format_default_succeeds", func(t *testing.T) {
		if out, err := run(t, "status"); err != nil {
			t.Fatalf("`bd status` (no --format) should succeed, got err=%v:\n%s", err, out)
		}
	})
}
