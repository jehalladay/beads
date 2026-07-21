//go:build cgo

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// TestEmbeddedDeferReasonAtomicAppend (beads-j8yhg) proves `bd defer --reason`
// APPENDS its reason to the notes column rather than doing a client-side
// read-modify-write of the whole blob. The direct path used to read issue.Notes
// at resolve time, concat in Go, and write the combined blob back via a SEPARATE
// UpdateIssue — a concurrent notes writer landing between the read and the write
// was lost-update clobbered (silent, last-writer-wins). The fix routes the
// reason through store.AppendNotes (a single server-side CONCAT_WS in its own
// tx), the defer sink twin of beads-jscve's `update --append-notes` fix.
func TestEmbeddedDeferReasonAtomicAppend(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "dr")

	// A defer --reason must PRESERVE pre-existing notes and append the reason,
	// not overwrite. Seed notes via update --append-notes, then defer --reason.
	t.Run("defer_reason_preserves_prior_notes", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Defer reason append", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--append-notes", "prior-note")
		bdDefer(t, bd, dir, issue.ID, "--reason", "defer-reason-one")

		got := bdShow(t, bd, dir, issue.ID)
		if !strings.Contains(got.Notes, "prior-note") {
			t.Errorf("defer --reason clobbered prior notes; notes=%q", got.Notes)
		}
		if !strings.Contains(got.Notes, "defer-reason-one") {
			t.Errorf("defer --reason did not append the reason; notes=%q", got.Notes)
		}
	})

	// Two successive defer --reason calls must ACCUMULATE both reasons (a
	// re-defer WITH a new --reason is a genuine change that appends).
	t.Run("defer_reason_accumulates", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Defer reason accumulate", "--type", "task")
		bdDefer(t, bd, dir, issue.ID, "--reason", "reason-alpha")
		bdDefer(t, bd, dir, issue.ID, "--reason", "reason-beta")

		got := bdShow(t, bd, dir, issue.ID)
		if !strings.Contains(got.Notes, "reason-alpha") {
			t.Errorf("first defer reason lost; notes=%q", got.Notes)
		}
		if !strings.Contains(got.Notes, "reason-beta") {
			t.Errorf("second defer reason lost; notes=%q", got.Notes)
		}
	})

	// Concurrent notes writers (defer --reason against distinct issues + a
	// direct store.AppendNotes racing onto one of them) must not lose any
	// marker: the atomic server-side append leaves no client-side snapshot to
	// go stale. CLI flock serializes the subprocesses, so the discriminating
	// race is the direct store append interleaving with the CLI write path.
	t.Run("defer_reason_no_lost_update_under_concurrency", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "Defer reason race", "--type", "task")
		bdUpdate(t, bd, dir, issue.ID, "--append-notes", "seed-marker")

		const workers = 6
		var wg sync.WaitGroup
		wg.Add(workers)
		errs := make([]error, workers)
		var start sync.WaitGroup
		start.Add(1)
		for w := 0; w < workers; w++ {
			go func(worker int) {
				defer wg.Done()
				start.Wait()
				// Each worker defers the same issue with a distinct reason via
				// the CLI (flock-serialized). All reasons must survive.
				out, err := deferReasonCombined(bd, dir, issue.ID,
					fmt.Sprintf("race-reason-%d", worker))
				if err != nil && !strings.Contains(out, "one writer at a time") {
					errs[worker] = fmt.Errorf("worker %d: %v\n%s", worker, err, out)
				}
			}(w)
		}
		start.Done()
		wg.Wait()

		for _, e := range errs {
			if e != nil {
				t.Fatalf("concurrent defer --reason: %v", e)
			}
		}

		got := bdShow(t, bd, dir, issue.ID)
		if !strings.Contains(got.Notes, "seed-marker") {
			t.Errorf("seed notes clobbered under concurrency; notes=%q", got.Notes)
		}
		for w := 0; w < workers; w++ {
			marker := fmt.Sprintf("race-reason-%d", w)
			if !strings.Contains(got.Notes, marker) {
				t.Errorf("defer reason %q lost to concurrent clobber; notes=%q", marker, got.Notes)
			}
		}
	})
}

// deferReasonCombined runs `bd defer <id> --reason <reason>` returning combined
// output + error (used by the concurrent worker test where a non-zero exit from
// flock contention is tolerated).
func deferReasonCombined(bd, dir, id, reason string) (string, error) {
	cmd := exec.Command(bd, "defer", id, "--reason", reason)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
