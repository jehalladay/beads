//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestEmbeddedDeferReasonPositional_qvbjq proves `bd defer` maps a repeated
// --reason POSITIONALLY across multiple IDs, matching `bd close`/`bd done`.
//
// Before the fix defer read a single GetString("reason") (cobra last-wins), so
// `bd defer A B --reason r1 --reason r2` dropped r1 and appended r2 to BOTH A
// and B — silent batch data loss. This drives the real embedded store end to
// end and asserts each issue got ITS OWN reason (r1 on A only, r2 on B only),
// a single --reason still broadcasts, a count that is neither 1 nor N is
// rejected, and the empty-reason JSON contract (beads-v02z) still holds.
func TestEmbeddedDeferReasonPositional_qvbjq(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "dp")

	// The core defect: N reasons for N IDs map one-per-ID, not last-wins-broadcast.
	t.Run("reasons_map_positionally", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "positional A", "--type", "task")
		b := bdCreate(t, bd, dir, "positional B", "--type", "task")

		bdDefer(t, bd, dir, a.ID, b.ID, "-r", "reason-for-A", "-r", "reason-for-B")

		gotA := bdShow(t, bd, dir, a.ID)
		gotB := bdShow(t, bd, dir, b.ID)

		if !strings.Contains(gotA.Notes, "reason-for-A") {
			t.Errorf("REGRESSION (beads-qvbjq): A did not get its positional reason; notes=%q", gotA.Notes)
		}
		if strings.Contains(gotA.Notes, "reason-for-B") {
			t.Errorf("REGRESSION (beads-qvbjq): A got B's reason (broadcast last-wins bug); notes=%q", gotA.Notes)
		}
		if !strings.Contains(gotB.Notes, "reason-for-B") {
			t.Errorf("REGRESSION (beads-qvbjq): B did not get its positional reason; notes=%q", gotB.Notes)
		}
		if strings.Contains(gotB.Notes, "reason-for-A") {
			t.Errorf("REGRESSION (beads-qvbjq): B leaked A's reason; notes=%q", gotB.Notes)
		}
	})

	// A single --reason still broadcasts to every ID (backward-compatible).
	t.Run("single_reason_broadcasts", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "broadcast A", "--type", "task")
		b := bdCreate(t, bd, dir, "broadcast B", "--type", "task")

		bdDefer(t, bd, dir, a.ID, b.ID, "--reason", "shared-reason")

		if got := bdShow(t, bd, dir, a.ID); !strings.Contains(got.Notes, "shared-reason") {
			t.Errorf("single --reason did not broadcast to A; notes=%q", got.Notes)
		}
		if got := bdShow(t, bd, dir, b.ID); !strings.Contains(got.Notes, "shared-reason") {
			t.Errorf("single --reason did not broadcast to B; notes=%q", got.Notes)
		}
	})

	// A reason count that is neither 1 nor len(IDs) is rejected, not guessed.
	t.Run("count_mismatch_rejected", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "mismatch A", "--type", "task")
		b := bdCreate(t, bd, dir, "mismatch B", "--type", "task")

		out, err := deferCombined(bd, dir, a.ID, b.ID, "-r", "r1", "-r", "r2", "-r", "r3")
		if err == nil {
			t.Fatalf("3 reasons for 2 IDs must error, got success; out=%q", out)
		}
		if !strings.Contains(out, "3 defer reasons for 2 issue IDs") {
			t.Errorf("count-mismatch error missing the descriptive message; out=%q", out)
		}
		// Neither issue should have been deferred (validation runs before writes).
		if got := bdShow(t, bd, dir, a.ID); got.Status == "deferred" {
			t.Errorf("A was deferred despite the count-mismatch error")
		}
	})

	// The empty-reason JSON contract (beads-v02z) survives the positional flag.
	t.Run("empty_reason_json_error", func(t *testing.T) {
		a := bdCreate(t, bd, dir, "empty reason", "--type", "task")

		out, err := deferCombined(bd, dir, a.ID, "--reason=", "--json")
		if err == nil {
			t.Fatalf("empty --reason must error; out=%q", out)
		}
		// stdout must carry a parseable JSON error object with a non-empty error.
		var obj map[string]any
		dec := json.NewDecoder(strings.NewReader(out))
		var parsed bool
		for {
			if e := dec.Decode(&obj); e != nil {
				break
			}
			if s, ok := obj["error"].(string); ok && s != "" {
				parsed = true
				break
			}
		}
		if !parsed {
			t.Errorf("empty --reason --json did not emit a parseable JSON error with a non-empty \"error\"; out=%q", out)
		}
	})
}

// deferCombined runs `bd defer <args...>` returning combined output + error,
// for negative-path tests that expect a non-zero exit.
func deferCombined(bd, dir string, args ...string) (string, error) {
	full := append([]string{"defer"}, args...)
	cmd := exec.Command(bd, full...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
