//go:build cgo

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestRepoSyncFalseSuccess_tgufj is the teeth for beads-tgufj, a sibling of
// beads-o35h0 (federation sync false-success): `bd repo sync` returned RC=0 on a
// per-repo hydration FAILURE and over-counted repos_synced in --json.
//
// runReposSync's per-repo loop printed "Warning: ..." (human-only) then
// `continue`d, and the function ended with an unconditional `return nil` — so a
// failed hydration exited 0. The --json payload reported {"synced":true} with a
// repos_synced count of len(Additional)-totalSkipped, i.e. INCLUDING the failed
// repos, and carried no failure signal at all. `bd repo sync` is a data-movement
// command (multi-repo issue hydration); a false-success exit code + a
// no-error/over-counted JSON means automation (`bd repo sync && ...`, $?-checks)
// cannot detect a failed hydration.
//
// Repro: add a remote peer pointing at a non-existent file:// URL, then sync —
// the remote fetch (cache.Ensure) fails, so the import never happens. The fix
// must (a) exit non-zero on both legs and (b) surface a top-level failed:true +
// non-empty errors[] and exclude the failed repo from repos_synced in --json.
func TestRepoSyncFalseSuccess_tgufj(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt repo-sync tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "tgufj")

	// A remote repo that cannot be fetched (no dolt repo at this file:// path) →
	// hydration fails at cache.Ensure. `bd repo add` accepts a remote URL without
	// an existence check, so this lands cleanly in repos.additional.
	badURL := "file://" + t.TempDir() + "/does-not-exist"
	if _, err := bdRunWithFlockRetry(t, bd, dir, "repo", "add", badURL); err != nil {
		t.Fatalf("repo add %s: %v", badURL, err)
	}

	// runSync executes `bd repo sync` with the given extra args and returns
	// stdout (isolated for JSON parsing), combined stdout+stderr (for human-leg
	// string checks), and the exit code (0 on success). The human "failed to
	// sync" line + per-repo Warning go to stderr; the --json payload goes to
	// stdout — so JSON parsing must use stdout only, not the combined stream.
	runSync := func(extra ...string) (stdout, combined string, code int) {
		t.Helper()
		args := append([]string{"repo", "sync"}, extra...)
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		var outBuf, errBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
		err := cmd.Run()
		code = 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				t.Fatalf("repo sync exec error (not an exit error): %v\nstdout:\n%s\nstderr:\n%s", err, outBuf.String(), errBuf.String())
			}
		}
		return outBuf.String(), outBuf.String() + errBuf.String(), code
	}

	// ── HUMAN leg: a failed hydration must exit non-zero (was RC=0). ──
	_, humanOut, humanCode := runSync()
	if !strings.Contains(humanOut, "failed to sync") {
		t.Fatalf("expected a failure line for a failed repo sync; got:\n%s", humanOut)
	}
	if humanCode == 0 {
		t.Errorf("tgufj: `bd repo sync` with a FAILED repo hydration must exit non-zero; got RC=0\n%s", humanOut)
	}

	// ── JSON leg: the failure must be visible + repos_synced must exclude it. ──
	jsonOut, _, jsonCode := runSync("--json")
	if jsonCode == 0 {
		t.Errorf("tgufj: `bd repo sync --json` on a FAILED hydration must exit non-zero; got RC=0\n%s", jsonOut)
	}
	var payload struct {
		Synced      bool     `json:"synced"`
		Failed      bool     `json:"failed"`
		ReposSynced int      `json:"repos_synced"`
		ReposFailed int      `json:"repos_failed"`
		Errors      []string `json:"errors"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &payload); err != nil {
		t.Fatalf("repo sync --json should emit valid JSON: %v\n%s", err, jsonOut)
	}
	if payload.Synced {
		t.Errorf("tgufj: --json synced must be false when a repo failed; got:\n%s", jsonOut)
	}
	if !payload.Failed {
		t.Errorf("tgufj: --json must carry top-level failed:true on a failed sync; got:\n%s", jsonOut)
	}
	if payload.ReposFailed < 1 {
		t.Errorf("tgufj: --json repos_failed must be >= 1; got:\n%s", jsonOut)
	}
	if payload.ReposSynced != 0 {
		t.Errorf("tgufj: the single failed repo must NOT be counted in repos_synced (was len(Additional)-skipped, over-counting failures); got repos_synced=%d\n%s", payload.ReposSynced, jsonOut)
	}
	if len(payload.Errors) == 0 {
		t.Errorf("tgufj: --json must carry a non-empty errors[] naming the failed repo; got:\n%s", jsonOut)
	}
}
