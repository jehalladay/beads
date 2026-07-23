//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestCloseReasonAliasConflict_5vkqz pins the beads-5vkqz fix: `bd close`
// documents --resolution, --message, and --comment as ALIASES for --reason, and
// collectCloseReasonFlags resolves them by first-match priority (reason >
// resolution > message > comment). So `bd close X --reason A --resolution B`
// silently used A and DISCARDED B with rc0 and no diagnostic — the dz1t8
// input-source silent-drop class (same as 637yc bd-edit field flags, bscdj
// dep-add aliases, comment/note positional-vs-flag). The fix rejects >1
// DIFFERENT alias at cobra parse time via MarkFlagsMutuallyExclusive.
//
// Repeating a SINGLE member (`--reason a --reason b` for per-issue batch close
// reasons) is one group member set N times, NOT a conflict — the batch subtest
// guards that MarkFlagsMutuallyExclusive does not over-reject it.
//
// Mutation check: remove the
//   closeCmd.MarkFlagsMutuallyExclusive("reason", "resolution", "message", "comment")
// line in close.go's init() and the *_rejected subtests go RED (the command
// succeeds rc0, --reason wins, and the alias is silently dropped).
func TestCloseReasonAliasConflict_5vkqz(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "cr")

	runClose := func(t *testing.T, args ...string) (string, bool) {
		t.Helper()
		full := append([]string{"close"}, args...)
		cmd := exec.Command(bd, full...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		return string(out), err != nil
	}

	// Each combination of two DIFFERENT reason aliases must be rejected loudly,
	// not silently resolve to one.
	conflictPairs := [][2]string{
		{"--reason", "--resolution"},
		{"--reason", "--message"},
		{"--resolution", "--message"},
		{"--message", "--comment"},
	}
	for _, pair := range conflictPairs {
		pair := pair
		t.Run("reject"+pair[0]+pair[1], func(t *testing.T) {
			issue := bdCreate(t, bd, dir, "close conflict target", "--type", "task")
			out, failed := runClose(t, issue.ID, pair[0], "valA", pair[1], "valB")
			if !failed {
				t.Fatalf("bd close %s %s valA %s valB must be rejected (aliases are mutually exclusive), got success:\n%s",
					issue.ID, pair[0], pair[1], out)
			}
			if !strings.Contains(out, "none of the others can be") {
				t.Errorf("expected a mutually-exclusive rejection naming the reason aliases, got:\n%s", out)
			}
		})
	}

	// Regression: each alias alone still closes the issue.
	for _, flag := range []string{"--reason", "--resolution", "--message", "--comment"} {
		flag := flag
		t.Run("alone"+flag+"_ok", func(t *testing.T) {
			issue := bdCreate(t, bd, dir, "close solo target", "--type", "task")
			if out, failed := runClose(t, issue.ID, flag, "solo reason"); failed {
				t.Fatalf("bd close %s %s (single alias) must succeed, got failure:\n%s", issue.ID, flag, out)
			}
		})
	}

	// Regression: repeating a SINGLE member for a batch close (one per-issue
	// reason each) must still work — MarkFlagsMutuallyExclusive must not treat a
	// repeated single flag as a conflict.
	t.Run("multi_reason_batch_ok", func(t *testing.T) {
		p := bdCreate(t, bd, dir, "batch P", "--type", "task")
		q := bdCreate(t, bd, dir, "batch Q", "--type", "task")
		if out, failed := runClose(t, p.ID, q.ID, "--reason", "reasonP", "--reason", "reasonQ"); failed {
			t.Fatalf("bd close %s %s --reason reasonP --reason reasonQ (batch) must succeed, got failure:\n%s", p.ID, q.ID, out)
		}
	})
}
