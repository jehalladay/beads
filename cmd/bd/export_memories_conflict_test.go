//go:build cgo

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestEmbeddedExportMemoriesFlagConflict is the beads-1bw1q teeth.
//
// `bd export --include-memories --no-memories` (both set) silently excluded
// memories: the resolution `(exportIncludeMemories || exportAll) &&
// !exportNoMemories` let the deprecated --no-memories override the explicit
// --include-memories with no error — the user got the OPPOSITE of what they
// asked. Reject the contradictory combination (a0nmp/7f3g/9sdix class); each
// flag alone stays valid.
func TestEmbeddedExportMemoriesFlagConflict(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "emc")

	t.Run("both_flags_rejected", func(t *testing.T) {
		cmd := exec.Command(bd, "export", "--include-memories", "--no-memories")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected `bd export --include-memories --no-memories` to fail (contradictory), got success:\n%s", out)
		}
		if !strings.Contains(string(out), "cannot be combined with --no-memories") {
			t.Errorf("expected the conflict error, got: %s", out)
		}
	})

	// Each flag alone must still succeed (surgical no-regression).
	for _, args := range [][]string{
		{"export", "--include-memories"},
		{"export", "--no-memories"},
		{"export"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			cmd := exec.Command(bd, args...)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("`bd %s` should succeed, got error: %v\n%s", strings.Join(args, " "), err, out)
			}
		})
	}
}
