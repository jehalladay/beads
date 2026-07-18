//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// bdBurnFail runs "bd mol burn ..." expecting a nonzero exit and returns the
// combined output.
func bdBurnFail(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"mol", "burn"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected bd mol burn %s to fail, but succeeded:\n%s", strings.Join(args, " "), out)
	}
	return string(out)
}

// bdBurnOK runs "bd mol burn ..." expecting a zero exit and returns the
// combined output.
func bdBurnOK(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"mol", "burn"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected bd mol burn %s to succeed, but failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// TestEmbeddedMolBurnExitCode covers beads-uscf: the multi-molecule burn path
// used to swallow id-resolution / load failures to rc=0, silently proceeding on
// a destructive delete. It must exit non-zero when any id fails (partial-apply
// preserved), matching the correct single-molecule path.
func TestEmbeddedMolBurnExitCode(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "bn")

	// Partial: one valid + one ghost must exit non-zero, but the valid molecule
	// is still burned (partial-apply preserved).
	t.Run("multi_partial_resolution_fail_exits_nonzero", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "burn partial", "--type", "task")
		bdBurnFail(t, bd, dir, issue.ID, "ghost-does-not-exist", "--force")
		// The valid molecule must be gone despite the partial failure.
		cmd := exec.Command(bd, "show", issue.ID)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if out, err := cmd.CombinedOutput(); err == nil {
			t.Errorf("expected %s to be burned despite partial failure, but show succeeded:\n%s", issue.ID, out)
		}
	})

	// All ids fail to resolve → non-zero ("No valid molecules to burn").
	t.Run("multi_all_bogus_exits_nonzero", func(t *testing.T) {
		bdBurnFail(t, bd, dir, "ghost-a", "ghost-b", "--force")
	})

	// No regression: an all-valid multi burn still exits zero.
	t.Run("multi_all_valid_exits_zero", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "burn valid a", "--type", "task")
		b := bdCreate(t, bd, dir, "burn valid b", "--type", "task")
		bdBurnOK(t, bd, dir, a.ID, b.ID, "--force")
	})
}
