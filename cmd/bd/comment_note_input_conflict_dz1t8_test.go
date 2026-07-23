//go:build cgo

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCommentNoteInputSourceConflict_dz1t8 pins the beads-dz1t8 fix: `bd comment`
// and `bd note` take their text from at most ONE source. Positional text combined
// with --stdin or --file used to be silently resolved by the switch (flag wins,
// positional dropped) — a user who typed both lost their inline text with no
// signal. --stdin/--file are already MarkFlagsMutuallyExclusive, and `bd create`
// rejects the same positional+--file clash; these verbs must too.
//
// The canonical long-form `bd comments add <id> <text>` (a separate impl with
// only --file) had the same silent drop and is covered here too.
//
// Mutation check: remove the `len(textArgs) > 0 && ...` guards in comment.go /
// note.go (and the `fileFlag != "" && len(args) > 1` guard in comments.go) and the
// *_rejected subtests go RED (the command succeeds rc0 and the positional is
// silently dropped).
func TestCommentNoteInputSourceConflict_dz1t8(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "cn")

	fileText := "text-from-file"
	fpath := filepath.Join(dir, "body.txt")
	if err := os.WriteFile(fpath, []byte(fileText), 0o644); err != nil {
		t.Fatalf("write body file: %v", err)
	}

	// runVerb runs `bd <verb> ...args`, returns combined output + whether it exited
	// non-zero (a rejection).
	runVerb := func(t *testing.T, verb string, args ...string) (string, bool) {
		t.Helper()
		full := append([]string{verb}, args...)
		cmd := exec.Command(bd, full...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		return string(out), err != nil
	}

	for _, verb := range []string{"comment", "note"} {
		verb := verb
		t.Run(verb+"_positional_plus_file_rejected", func(t *testing.T) {
			issue := bdCreate(t, bd, dir, verb+" file target", "--type", "task")
			out, failed := runVerb(t, verb, issue.ID, "inline positional", "--file", fpath)
			if !failed {
				t.Fatalf("bd %s <id> \"inline\" --file must be rejected (conflicting input sources), got success:\n%s", verb, out)
			}
			if !strings.Contains(out, "cannot specify both positional text and --file") {
				t.Errorf("expected a 'cannot specify both positional text and --file' error, got:\n%s", out)
			}
		})

		t.Run(verb+"_positional_plus_stdin_rejected", func(t *testing.T) {
			issue := bdCreate(t, bd, dir, verb+" stdin target", "--type", "task")
			full := []string{verb, issue.ID, "inline positional", "--stdin"}
			cmd := exec.Command(bd, full...)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			cmd.Stdin = strings.NewReader("text-from-stdin\n")
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("bd %s <id> \"inline\" --stdin must be rejected (conflicting input sources), got success:\n%s", verb, out)
			}
			if !strings.Contains(string(out), "cannot specify both positional text and --stdin") {
				t.Errorf("expected a 'cannot specify both positional text and --stdin' error, got:\n%s", out)
			}
		})

		// Regression guard: each source still works on its own.
		t.Run(verb+"_positional_only_ok", func(t *testing.T) {
			issue := bdCreate(t, bd, dir, verb+" pos-only target", "--type", "task")
			if out, failed := runVerb(t, verb, issue.ID, "just positional"); failed {
				t.Fatalf("bd %s <id> \"just positional\" (alone) must succeed, got failure:\n%s", verb, out)
			}
		})

		t.Run(verb+"_file_only_ok", func(t *testing.T) {
			issue := bdCreate(t, bd, dir, verb+" file-only target", "--type", "task")
			if out, failed := runVerb(t, verb, issue.ID, "--file", fpath); failed {
				t.Fatalf("bd %s <id> --file (alone) must succeed, got failure:\n%s", verb, out)
			}
		})
	}

	// beads-dz1t8: the canonical long-form `bd comments add <id> <text>` is a
	// SEPARATE implementation (commentsAddCmd, independent of the singular
	// `bd comment` shorthand) with only -f/--file (no --stdin). It had the same
	// silent positional-drop when both a positional and --file were given.
	runComments := func(t *testing.T, args ...string) (string, bool) {
		t.Helper()
		full := append([]string{"comments", "add"}, args...)
		cmd := exec.Command(bd, full...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		return string(out), err != nil
	}

	t.Run("comments_add_positional_plus_file_rejected", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "comments add file target", "--type", "task")
		out, failed := runComments(t, issue.ID, "inline positional", "--file", fpath)
		if !failed {
			t.Fatalf("bd comments add <id> \"inline\" --file must be rejected (conflicting input sources), got success:\n%s", out)
		}
		if !strings.Contains(out, "cannot specify both positional text and --file") {
			t.Errorf("expected a 'cannot specify both positional text and --file' error, got:\n%s", out)
		}
	})

	t.Run("comments_add_positional_only_ok", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "comments add pos-only target", "--type", "task")
		if out, failed := runComments(t, issue.ID, "just positional"); failed {
			t.Fatalf("bd comments add <id> \"just positional\" (alone) must succeed, got failure:\n%s", out)
		}
	})

	t.Run("comments_add_file_only_ok", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "comments add file-only target", "--type", "task")
		if out, failed := runComments(t, issue.ID, "--file", fpath); failed {
			t.Fatalf("bd comments add <id> --file (alone) must succeed, got failure:\n%s", out)
		}
	})
}
