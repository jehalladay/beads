//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// beads-71jpi (EARLY-VALIDATION-PARITY, read against bd federation sync):
// `bd vc merge --strategy <bad>` never validated the flag up-front. runVCMerge
// stored vcMergeStrategy and passed it to store.ResolveConflicts ONLY on the
// conflict path, so an invalid value (e.g. the typo 'our') was:
//   (1) silently ACCEPTED on a clean merge — never consulted, "Successfully
//       merged" RC=0 (the user believes their strategy was honored; it wasn't);
//   (2) failed LATE on a conflicting merge, inside ResolveConflicts, after
//       store.Merge already mutated the working set.
// bd federation sync (identical --strategy ours|theirs flag) rejects a bad value
// RC!=0 BEFORE any work; the fix mirrors that guard in runVCMerge before
// store.Merge.
//
// These run end-to-end through the real embedded-dolt bd subprocess. The clean
// case is the load-bearing one: BEFORE the fix a garbage strategy on a
// no-conflict merge printed "Successfully merged" and exited 0 (RED — a
// fail-fast guard would have rejected it); AFTER the fix it exits non-zero with
// the accurate "invalid strategy" message BEFORE the merge runs.
func TestVCMergeStrategyValidatedUpFront_71jpi(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt vc tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// A valid --strategy must still work on a clean merge (parity: the guard
	// rejects only the invalid values, mirroring federation's ours|theirs set).
	t.Run("valid_strategy_clean_merge_ok", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "vcok")
		bdBranch(t, bd, dir, "feat-ok")
		bdCreateSilent(t, bd, dir, "main issue")

		out := bdVC(t, bd, dir, "merge", "feat-ok", "--strategy", "ours")
		if !strings.Contains(out, "merged") && !strings.Contains(out, "Merged") {
			t.Errorf("valid --strategy ours on a clean merge should succeed, got: %s", out)
		}
	})

	// The bug: a bad strategy on a CLEAN merge was silently accepted (RC=0,
	// "Successfully merged"). It must now fail up-front, non-zero, and the merge
	// message must NOT be printed.
	t.Run("invalid_strategy_clean_merge_rejected", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "vcbad")
		bdBranch(t, bd, dir, "feat-bad")
		bdCreateSilent(t, bd, dir, "main issue")

		out := bdVCFail(t, bd, dir, "merge", "feat-bad", "--strategy", "BOGUS")
		if !strings.Contains(out, "invalid strategy") || !strings.Contains(out, "must be 'ours' or 'theirs'") {
			t.Errorf("want up-front \"invalid strategy ...: must be 'ours' or 'theirs'\", got: %s", out)
		}
		if strings.Contains(out, "Successfully merged") || strings.Contains(out, "merged feat-bad") {
			t.Errorf("invalid --strategy must be rejected BEFORE the merge runs — no success message allowed, got: %s", out)
		}
	})

	// The specific typo from the bead repro ('our' — a dropped 's').
	t.Run("typo_our_rejected", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "vctypo")
		bdBranch(t, bd, dir, "feat-typo")
		bdCreateSilent(t, bd, dir, "main issue")

		out := bdVCFail(t, bd, dir, "merge", "feat-typo", "--strategy", "our")
		if !strings.Contains(out, "invalid strategy") {
			t.Errorf("the typo 'our' must be rejected up-front, got: %s", out)
		}
	})

	// Under --json the guard must emit a parseable {error} object on STDOUT
	// (HandleErrorRespectJSON contract), not the misdiagnosis or a bare success.
	t.Run("invalid_strategy_json_error", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "vcbadj")
		bdBranch(t, bd, dir, "feat-badj")
		bdCreateSilent(t, bd, dir, "main issue")

		cmd := exec.Command(bd, "vc", "merge", "feat-badj", "--strategy", "BOGUS", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, _, err := runCommandBuffers(t, cmd)
		if err == nil {
			t.Fatalf("bd vc merge --strategy BOGUS --json should fail; got success.\nstdout:\n%s", stdout.String())
		}
		s := strings.TrimSpace(stdout.String())
		var m map[string]interface{}
		if jerr := json.Unmarshal([]byte(s), &m); jerr != nil {
			t.Fatalf("want a JSON {error} object on STDOUT, parse failed: %v\nstdout:\n%s", jerr, s)
		}
		if _, ok := m["error"]; !ok {
			t.Errorf("JSON error envelope missing \"error\" key: %s", s)
		}
		if e, _ := m["error"].(string); !strings.Contains(e, "invalid strategy") {
			t.Errorf("JSON error should carry the accurate message, got: %s", s)
		}
	})
}
