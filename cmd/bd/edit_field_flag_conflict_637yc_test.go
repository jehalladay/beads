//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestEditFieldFlagConflict_637yc pins the beads-637yc fix: `bd edit` opens a
// SINGLE field in $EDITOR, and editFieldFromFlags (shared by the direct
// edit.go path and the proxied runEditProxiedServer path) is a priority switch
// title>design>notes>acceptance>description. When a user set more than one
// field flag (e.g. `bd edit X --title --design`), only the highest-precedence
// field was edited and the rest were SILENTLY discarded with rc0 and no
// diagnostic — the dz1t8 input-source silent-drop class. The fix rejects >1
// field flag at cobra parse time via MarkFlagsMutuallyExclusive, matching
// create's mutually-exclusive field flags; being pre-store it is twin-safe
// (covers direct + proxied identically).
//
// Mutation check: remove the
//   editCmd.MarkFlagsMutuallyExclusive("title","description","design","notes","acceptance")
// line in edit.go's init() and the *_rejected subtests go RED (the combined
// form succeeds rc0 and the lower-precedence flag is silently dropped).
func TestEditFieldFlagConflict_637yc(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ef")

	// runEdit runs `bd edit <args...>` with EDITOR=true (a no-op editor so a
	// single-field edit is a clean "No changes made" and never blocks on a real
	// editor). Returns combined output + whether it exited non-zero.
	runEdit := func(t *testing.T, args ...string) (string, bool) {
		t.Helper()
		full := append([]string{"edit"}, args...)
		cmd := exec.Command(bd, full...)
		cmd.Dir = dir
		cmd.Env = append(bdEnv(dir), "EDITOR=true")
		out, err := cmd.CombinedOutput()
		return string(out), err != nil
	}

	// Each combination of two field flags must be rejected loudly (mutually
	// exclusive), not silently resolve to one field.
	conflictPairs := [][2]string{
		{"--title", "--design"},
		{"--notes", "--acceptance"},
		{"--description", "--title"},
		{"--design", "--notes"},
	}
	for _, pair := range conflictPairs {
		pair := pair
		t.Run("reject"+pair[0]+pair[1], func(t *testing.T) {
			issue := bdCreate(t, bd, dir, "edit conflict target", "--type", "task")
			out, failed := runEdit(t, issue.ID, pair[0], pair[1])
			if !failed {
				t.Fatalf("bd edit %s %s %s must be rejected (mutually-exclusive field flags), got success:\n%s",
					issue.ID, pair[0], pair[1], out)
			}
			// cobra's mutually-exclusive error names the flag group; assert a
			// diagnostic mentioning both flags rather than a silent drop.
			if !strings.Contains(out, "none of the others can be") {
				t.Errorf("expected a mutually-exclusive rejection naming the conflicting flags, got:\n%s", out)
			}
		})
	}

	// Regression guard: a SINGLE field flag must still be accepted (the guard
	// must not over-reject the normal one-field edit).
	t.Run("single_title_flag_ok", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "edit single-flag target", "--type", "task")
		out, failed := runEdit(t, issue.ID, "--title")
		if failed {
			t.Fatalf("bd edit %s --title (single flag) must succeed, got failure:\n%s", issue.ID, out)
		}
	})

	// Regression guard: no field flag at all (default = description) still works.
	t.Run("no_flag_default_ok", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "edit no-flag target", "--type", "task")
		out, failed := runEdit(t, issue.ID)
		if failed {
			t.Fatalf("bd edit %s (no flag, default description) must succeed, got failure:\n%s", issue.ID, out)
		}
	})
}
