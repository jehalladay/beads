//go:build cgo

package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// beads-z2b4: `bd worktree remove/create/list --json` returned their domain
// errors via a bare `fmt.Errorf`, so cobra printed a plaintext "Error: ..."
// line to stderr and exited 1 with EMPTY stdout — unparseable by a --json
// consumer — while the sibling success paths (and lav0) already emit JSON.
// The fix routes the reachable-under-json error paths through
// HandleErrorRespectJSON.
//
// This drives runWorktreeRemove against a real (but throwaway) git repo with a
// ghost worktree name — the exact repro in the bead (`bd worktree remove
// ghost-wt --json`). resolveWorktreePath returns "worktree not found", which
// the handler now surfaces as a JSON error object. Hermetic: needs only `git`
// on PATH, no Dolt server / network.
func TestWorktreeRemoveJSONErrorContract_z2b4(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	// A committed HEAD isn't required to hit the not-found path, but init a
	// minimal identity so any incidental git call is well-formed.
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "test")

	t.Chdir(repo)

	prevJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = prevJSON })

	out, err := captureStdoutExpectErr(t, func() error {
		return runWorktreeRemove(worktreeRemoveCmd, []string{"ghost-wt-z2b4"})
	})
	if err == nil {
		t.Fatalf("expected a non-nil error removing a ghost worktree, got nil (stdout=%q)", out)
	}

	s := strings.TrimSpace(out)
	if s == "" {
		t.Fatalf("stdout empty on `bd worktree remove <ghost> --json` — must emit a JSON error object (beads-z2b4)")
	}
	var obj map[string]any
	if jerr := json.Unmarshal([]byte(s), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on --json error: %v\nstdout:\n%s", jerr, s)
	}
	if _, ok := obj["error"]; !ok {
		t.Errorf("expected an \"error\" field in the --json stdout object, got: %s", s)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}
