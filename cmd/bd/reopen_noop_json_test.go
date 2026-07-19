//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestReopenAlreadyOpenNoopJSON_hxc2 is the error-contract teeth for
// beads-hxc2. `bd reopen <already-open> --json` is an idempotent no-op SUCCESS
// (exit 0 — the issue is already in reopen's target state), yet the DIRECT path
// used to emit an {"error": "<id> is already open"} JSON object on stderr with
// EMPTY stdout. That mislabels a success as an error and is asymmetric with
// `bd close <already-closed> --json`, which reflects the (unchanged) state in
// the success payload on stdout.
//
// The fix reflects the already-open issue in the reopen success array on stdout
// (mirroring close.go's already-closed path) instead of the error-keyed stderr
// object. This test pins:
//  1. exit 0 (no-op is not a failure);
//  2. stdout carries a JSON array that INCLUDES the already-open issue;
//  3. stderr emits NO {"error":...}-keyed object for the already-open no-op.
func TestReopenAlreadyOpenNoopJSON_hxc2(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ro")

	issue := bdCreate(t, bd, dir, "hxc2 already-open target", "--type", "task")

	cmd := exec.Command(bd, "reopen", issue.ID, "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	rerr := cmd.Run()
	stdout := stdoutBuf.String()
	stderr := stderrBuf.String()

	// 1. exit 0: an already-open reopen is an idempotent no-op success.
	if rerr != nil {
		t.Fatalf("expected exit 0 for already-open reopen no-op, got err=%v\nstdout=%q\nstderr=%q", rerr, stdout, stderr)
	}

	// 2. stdout carries a JSON array that includes the already-open issue.
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		t.Fatalf("beads-hxc2: stdout is EMPTY on already-open reopen --json — the no-op success must reflect state on stdout, not an error-keyed stderr object\nstderr=%q", stderr)
	}
	var arr []map[string]interface{}
	if jerr := json.Unmarshal([]byte(trimmed), &arr); jerr != nil {
		t.Fatalf("beads-hxc2: stdout is not a JSON array: %v\nstdout=%q", jerr, stdout)
	}
	found := false
	for _, o := range arr {
		if id, _ := o["id"].(string); id == issue.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("beads-hxc2: already-open issue %s not reflected in the reopen --json payload\nstdout=%q", issue.ID, stdout)
	}

	// 3. stderr must NOT carry an {"error":...}-keyed object for the no-op:
	// mislabeling an exit-0 success as an error breaks consumers that flag any
	// {"error":...} payload as a failure.
	if s := strings.TrimSpace(stderr); s != "" {
		var obj map[string]interface{}
		if json.Unmarshal([]byte(s), &obj) == nil {
			if _, ok := obj["error"]; ok {
				t.Fatalf("beads-hxc2: already-open reopen no-op emitted an {\"error\":...}-keyed JSON object on stderr for an exit-0 success:\nstderr=%q", stderr)
			}
		}
	}
}
