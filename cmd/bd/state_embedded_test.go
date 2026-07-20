//go:build cgo

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// bdState runs "bd state" with the given args and returns stdout.
func bdState(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"state"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd state %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

// bdSetState runs "bd set-state" with the given args and returns stdout.
func bdSetState(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"set-state"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd set-state %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func TestEmbeddedState(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "st")

	issue := bdCreate(t, bd, dir, "State test issue", "--type", "task")

	// ===== set-state =====

	t.Run("set_state_basic", func(t *testing.T) {
		out := bdSetState(t, bd, dir, issue.ID, "phase=planning")
		if !strings.Contains(out, "planning") {
			t.Logf("set-state output: %s", out)
		}
	})

	t.Run("set_state_json", func(t *testing.T) {
		cmd := exec.Command(bd, "set-state", issue.ID, "env=staging", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd set-state --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		s := strings.TrimSpace(stdout.String())
		start := strings.Index(s, "{")
		if start >= 0 {
			var m map[string]interface{}
			if jsonErr := json.Unmarshal([]byte(s[start:]), &m); jsonErr != nil {
				t.Errorf("invalid JSON: %v\n%s", jsonErr, s)
			}
		}
	})

	// beads-wd2x4: `bd set-state --json` must emit the SAME key-set on the
	// change and no-op (same-value) legs. Previously change → {issue_id,
	// dimension,old_value,new_value,event_id,changed} while a no-op → {issue_id,
	// dimension,value,changed} — the value payload moved between keys, so a
	// consumer reading .new_value got nothing on a no-op. The fix drops the lone
	// "value" key and makes the no-op emit old_value/new_value/event_id too.
	t.Run("set_state_json_stable_keyset_change_vs_noop", func(t *testing.T) {
		si := bdCreate(t, bd, dir, "wd2x4 keyset", "--type", "task")

		runSetStateJSON := func(t *testing.T) map[string]interface{} {
			t.Helper()
			cmd := exec.Command(bd, "set-state", si.ID, "tier=gold", "--json")
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			stdout, stderr, err := runCommandBuffers(t, cmd)
			if err != nil {
				t.Fatalf("set-state --json failed: %v\nstdout:%s\nstderr:%s", err, stdout.String(), stderr.String())
			}
			s := strings.TrimSpace(stdout.String())
			start := strings.Index(s, "{")
			if start < 0 {
				t.Fatalf("set-state --json emitted no JSON object:\n%s", s)
			}
			var m map[string]interface{}
			if e := json.Unmarshal([]byte(s[start:]), &m); e != nil {
				t.Fatalf("set-state --json invalid JSON: %v\n%s", e, s)
			}
			return m
		}

		keySet := func(m map[string]interface{}) map[string]bool {
			ks := map[string]bool{}
			for k := range m {
				if k == "schema_version" { // envelope-injected, not part of the cmd key-set
					continue
				}
				ks[k] = true
			}
			return ks
		}

		change := runSetStateJSON(t) // first set: real change
		if c, _ := change["changed"].(bool); !c {
			t.Errorf("expected changed:true on the first set, got %v", change["changed"])
		}
		noop := runSetStateJSON(t) // second set of same value: no-op
		if c, _ := noop["changed"].(bool); c {
			t.Errorf("expected changed:false on the no-op set, got %v", noop["changed"])
		}

		ck, nk := keySet(change), keySet(noop)
		if !reflect.DeepEqual(ck, nk) {
			t.Errorf("set-state --json key-set flips by outcome (beads-wd2x4):\n  change keys: %v\n  no-op keys:  %v", ck, nk)
		}
		for _, want := range []string{"issue_id", "dimension", "old_value", "new_value", "event_id", "changed"} {
			if !ck[want] {
				t.Errorf("change leg missing stable key %q; keys=%v", want, ck)
			}
			if !nk[want] {
				t.Errorf("no-op leg missing stable key %q; keys=%v", want, nk)
			}
		}
		// The lone "value" key must be gone (redundant with new_value).
		if nk["value"] {
			t.Errorf("no-op leg still emits the redundant \"value\" key (beads-wd2x4); keys=%v", nk)
		}
	})

	t.Run("set_state_with_reason", func(t *testing.T) {
		out := bdSetState(t, bd, dir, issue.ID, "risk=high", "--reason", "New vulnerability found")
		_ = out
	})

	// beads-brk7c: set-state must remove ALL existing labels for the dimension,
	// not just the first. A dimension can carry 2+ labels via the public
	// `bd label add <dim>:X` command; a break-on-first-match cleanup left stale
	// siblings, so `bd state <dim>` (first-match) and `bd state list`
	// (map last-wins) then disagreed on the value.
	t.Run("set_state_clears_all_duplicate_dimension_labels", func(t *testing.T) {
		si := bdCreate(t, bd, dir, "brk7c multi-label", "--type", "task")

		// Seed two same-dimension labels: set-state one, then add a sibling via
		// the public label command.
		bdSetState(t, bd, dir, si.ID, "mode=normal")
		bdLabelAdd := func(t *testing.T, lbl string) {
			t.Helper()
			cmd := exec.Command(bd, "label", "add", si.ID, lbl)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			if stdout, stderr, err := runCommandBuffers(t, cmd); err != nil {
				t.Fatalf("label add %s failed: %v\n%s\n%s", lbl, err, stdout.String(), stderr.String())
			}
		}
		bdLabelAdd(t, "mode:degraded")

		// Now set-state to a third value: it must clear BOTH mode:normal and
		// mode:degraded, leaving exactly one mode: label.
		bdSetState(t, bd, dir, si.ID, "mode=failing")

		// state list is the map-last-wins reader; state <dim> is first-match.
		// Both must agree post-fix (the bug made them diverge).
		single := strings.TrimSpace(bdState(t, bd, dir, si.ID, "mode"))
		listOut := bdState(t, bd, dir, "list", si.ID)
		if !strings.Contains(single, "failing") {
			t.Errorf("bd state mode should be 'failing', got: %q", single)
		}
		// The stale siblings (normal, degraded) must be gone from BOTH readers.
		if strings.Contains(single, "normal") || strings.Contains(single, "degraded") {
			t.Errorf("bd state mode leaked a stale sibling value: %q", single)
		}
		if strings.Contains(listOut, "normal") || strings.Contains(listOut, "degraded") {
			t.Errorf("bd state list leaked a stale sibling label (brk7c): %s", listOut)
		}
		if !strings.Contains(listOut, "failing") {
			t.Errorf("bd state list should show mode=failing, got: %s", listOut)
		}

		// Belt-and-suspenders: the JSON label list on the issue must hold exactly
		// one mode: label. `bd show --json` emits a JSON array of issues.
		cmd := exec.Command(bd, "show", si.ID, "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd show --json failed: %v\n%s", err, stderr.String())
		}
		s := stdout.String()
		start := strings.Index(s, "[")
		if start < 0 {
			t.Fatalf("bd show --json emitted no array:\n%s", s)
		}
		var arr []struct {
			Labels []string `json:"labels"`
		}
		if e := json.Unmarshal([]byte(s[start:]), &arr); e != nil {
			t.Fatalf("bd show --json invalid: %v\n%s", e, s)
		}
		if len(arr) != 1 {
			t.Fatalf("bd show --json expected 1 issue, got %d:\n%s", len(arr), s)
		}
		modeCount := 0
		for _, l := range arr[0].Labels {
			if strings.HasPrefix(l, "mode:") {
				modeCount++
			}
		}
		if modeCount != 1 {
			t.Errorf("expected exactly one mode: label after set-state, got %d: %v", modeCount, arr[0].Labels)
		}
	})

	t.Run("set_state_overwrites", func(t *testing.T) {
		bdSetState(t, bd, dir, issue.ID, "phase=development")
		bdSetState(t, bd, dir, issue.ID, "phase=testing")

		out := bdState(t, bd, dir, issue.ID, "phase")
		if !strings.Contains(out, "testing") {
			t.Errorf("expected 'testing' after overwrite, got: %s", out)
		}
	})

	// ===== state query =====

	t.Run("state_query", func(t *testing.T) {
		out := bdState(t, bd, dir, issue.ID, "phase")
		if !strings.Contains(out, "testing") {
			t.Logf("state query output: %s", out)
		}
	})

	t.Run("state_query_json", func(t *testing.T) {
		cmd := exec.Command(bd, "state", issue.ID, "phase", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd state --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		_ = stdout.String()
	})

	t.Run("state_query_nonexistent_dimension", func(t *testing.T) {
		out := bdState(t, bd, dir, issue.ID, "nonexistent")
		// Should return empty/not-set, not error
		_ = out
	})

	// ===== state list =====

	t.Run("state_list", func(t *testing.T) {
		out := bdState(t, bd, dir, "list", issue.ID)
		// Should show the dimensions we set
		if !strings.Contains(out, "phase") {
			t.Logf("state list output: %s", out)
		}
	})

	t.Run("state_list_json", func(t *testing.T) {
		cmd := exec.Command(bd, "state", "list", issue.ID, "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd state list --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		_ = stdout.String()
	})

	t.Run("state_list_no_dimensions", func(t *testing.T) {
		fresh := bdCreate(t, bd, dir, "No state", "--type", "task")
		out := bdState(t, bd, dir, "list", fresh.ID)
		_ = out
	})
}

// TestEmbeddedStateConcurrent exercises set-state concurrently on different dimensions.
func TestEmbeddedStateConcurrent(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "sx")

	issue := bdCreate(t, bd, dir, "Concurrent state", "--type", "task")

	const numWorkers = 6

	type workerResult struct {
		worker int
		err    error
	}

	results := make([]workerResult, numWorkers)
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for w := 0; w < numWorkers; w++ {
		go func(worker int) {
			defer wg.Done()
			r := workerResult{worker: worker}

			dim := fmt.Sprintf("dim%d=val%d", worker, worker)
			cmd := exec.Command(bd, "set-state", issue.ID, dim)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			out, err := cmd.CombinedOutput()
			if err != nil {
				r.err = fmt.Errorf("set-state %s: %v\n%s", dim, err, out)
			}

			results[worker] = r
		}(w)
	}
	wg.Wait()

	for _, r := range results {
		if r.err != nil && !strings.Contains(r.err.Error(), "one writer at a time") {
			t.Errorf("worker %d failed: %v", r.worker, r.err)
		}
	}
}
