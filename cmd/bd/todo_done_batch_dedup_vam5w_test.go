//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestTodoDoneBatchDedup_vam5w pins the beads-vam5w fix: a repeated issue id in
// ONE `bd todo done X X` batch must be closed and reported exactly once. This is
// the in-batch-dup class (delete / hzg2y label / cncgt reopen / 4k0d8 defer) for
// `bd todo done`. Without the uniqueStrings(args) dedup at the command entry:
//
//   - the {"closed":[...]} --json envelope double-COUNTS: iter-1 closes the issue
//     and appends it to closedIDs; iter-2 re-resolves the SAME (now-closed) id and
//     — todo done has NO already-closed guard, unlike its documented parent
//     `bd close` (dr3, close.go:145) — re-runs CloseIssue + auditStatusChange +
//     autoCloseCompletedMolecule and appends the id AGAIN.
//
// The dedup keeps first occurrence; genuinely-distinct ids are unaffected.
//
// Mutation check: remove `args = uniqueStrings(args)` in todo.go's todo-done RunE
// and repeated_id_closes_once goes RED (the closed array has 2 entries for one id).
func TestTodoDoneBatchDedup_vam5w(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "td")

	// parseClosed extracts the {"closed":[...]} array `bd todo done --json` emits.
	type doneEnvelope struct {
		Closed []string `json:"closed"`
	}
	runTodoDoneJSON := func(t *testing.T, ids ...string) doneEnvelope {
		t.Helper()
		full := append([]string{"todo", "done"}, ids...)
		full = append(full, "--json")
		cmd := exec.Command(bd, full...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bd todo done %v failed: %v\n%s", ids, err, out)
		}
		s := strings.TrimSpace(string(out))
		start := strings.Index(s, "{")
		if start < 0 {
			t.Fatalf("no JSON object in todo done output: %s", s)
		}
		var env doneEnvelope
		if err := json.Unmarshal([]byte(s[start:]), &env); err != nil {
			t.Fatalf("parse todo done JSON: %v\nstdout: %s", err, s)
		}
		return env
	}

	// A repeated id in one batch must appear exactly once in "closed".
	t.Run("repeated_id_closes_once", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "todo dup id", "--type", "task")
		env := runTodoDoneJSON(t, issue.ID, issue.ID)
		if len(env.Closed) != 1 {
			t.Fatalf("expected exactly 1 closed entry for a repeated id, got %d: %+v", len(env.Closed), env.Closed)
		}
		if env.Closed[0] != issue.ID {
			t.Errorf("expected %s closed, got %+v", issue.ID, env.Closed)
		}
	})

	// Negative control: two DISTINCT ids in one batch must both be reported.
	t.Run("distinct_ids_both_closed", func(t *testing.T) {
		i1 := bdCreate(t, bd, dir, "todo distinct 1", "--type", "task")
		i2 := bdCreate(t, bd, dir, "todo distinct 2", "--type", "task")
		env := runTodoDoneJSON(t, i1.ID, i2.ID)
		if len(env.Closed) != 2 {
			t.Fatalf("expected 2 closed entries for 2 distinct ids, got %d: %+v", len(env.Closed), env.Closed)
		}
		seen := map[string]bool{}
		for _, id := range env.Closed {
			seen[id] = true
		}
		if !seen[i1.ID] || !seen[i2.ID] {
			t.Errorf("expected both %s and %s closed, got %+v", i1.ID, i2.ID, env.Closed)
		}
	})
}
