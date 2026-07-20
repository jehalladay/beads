//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestCommentAuthorSanitize_i8dsb is the sanitize teeth for the comment.Author
// terminal-escape sink (beads-i8dsb, 7n9y sibling axis). A comment's author is
// getActorWithGit() — sourced from --actor / BEADS_ACTOR / BD_ACTOR / git
// user.name / $USER, none of which validate control characters — so a hostile
// actor string carries OSC/CSI escapes into the stored comment.Author. The
// `bd show` COMMENTS loop (show.go:416) and `bd comments <id>` (comments.go:107)
// rendered it raw, injecting terminal control sequences on display.
//
// End-to-end through the real print path: add a comment with an escape-laden
// BEADS_ACTOR, then render via `bd show` and `bd comments`, asserting no raw
// ESC/BEL reaches stdout while the visible author text survives.
func TestCommentAuthorSanitize_i8dsb(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	const csi = "\x1b[31m"
	const osc52 = "\x1b]52;c;cHduZWQ=\x07"
	evilActor := "evilA" + csi + osc52 + "userZ"

	task := bdCreate(t, bd, dir, "task with a comment", "--type", "task")

	// Add a comment whose author is the hostile actor via the global --actor
	// flag (getActorWithGit applies no control-char validation to it).
	addCmd := exec.Command(bd, "comment", task.ID, "a comment body", "--actor", evilActor)
	addCmd.Dir = dir
	addCmd.Env = bdEnv(dir)
	if out, err := addCmd.CombinedOutput(); err != nil {
		t.Fatalf("bd comment add failed: %v\n%s", err, out)
	}

	assertClean := func(t *testing.T, label string, args ...string) {
		t.Helper()
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bd %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
		got := string(out)
		if strings.ContainsRune(got, '\x1b') {
			t.Errorf("%s leaked a raw ESC (0x1b) — comment.Author not sanitized:\n%q", label, got)
		}
		if strings.ContainsRune(got, '\x07') {
			t.Errorf("%s leaked a raw BEL (0x07) — comment.Author not sanitized:\n%q", label, got)
		}
		if !strings.Contains(got, "userZ") {
			t.Errorf("%s dropped expected visible author text; output:\n%s", label, got)
		}
	}

	t.Run("show", func(t *testing.T) {
		assertClean(t, "bd show", "show", task.ID)
	})
	t.Run("comments", func(t *testing.T) {
		assertClean(t, "bd comments", "comments", task.ID)
	})
}
