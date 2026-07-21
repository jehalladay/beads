//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestEmbeddedDepListExitCode covers beads-116e: `bd dep list <id>...` in batch
// mode (len(args)>1) warn+continued on each failed resolve and returned nil at
// every terminal, so rc=0 even when a valid+ghost mix partially failed OR every
// id was a ghost ("no resolvable issues in batch" still returned nil). This
// diverged from the single-id path (rc=1, errors at resolve). Because a
// `bd dep list $ids || alert` guard is a common script pattern, the silent
// success meant a typo'd/missing id proceeded as if listed. The command must
// exit non-zero when any id fails to resolve in batch, while still listing the
// resolvable ids (partial output preserved). Same silent-partial-failure
// exit-code class as beads-sw7l / beads-2svv (bd show) and beads-xi35
// (bd todo done).
func TestEmbeddedDepListExitCode(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "dl")

	a := bdCreate(t, bd, dir, "dl real A", "--type", "task")
	b := bdCreate(t, bd, dir, "dl real B", "--type", "task")

	t.Run("single_valid_exits_zero", func(t *testing.T) {
		// baseline: a single resolvable id succeeds.
		bdDep(t, bd, dir, "list", a.ID)
	})

	t.Run("single_ghost_exits_nonzero", func(t *testing.T) {
		// baseline for the correct contract: single-id resolve failure = rc!=0.
		bdDepFail(t, bd, dir, "list", "dl-ghost-x")
	})

	t.Run("batch_all_ghost_exits_nonzero", func(t *testing.T) {
		bdDepFail(t, bd, dir, "list", "dl-ghost-a", "dl-ghost-b")
	})

	t.Run("batch_partial_exits_nonzero_still_lists_valid", func(t *testing.T) {
		// valid id + a ghost: must exit non-zero, but the valid id must still be
		// resolved/listed (partial output preserved). The valid id's line is on
		// stdout; the ghost warning is on stderr — CombinedOutput sees both.
		out := bdDepFail(t, bd, dir, "list", a.ID, "dl-ghost-z")
		if !strings.Contains(out, a.ID) {
			t.Errorf("expected valid issue %s still listed on partial failure, got:\n%s", a.ID, out)
		}
		if !strings.Contains(out, "dl-ghost-z") {
			t.Errorf("expected the skipped ghost id reported, got:\n%s", out)
		}
	})

	// beads-7kxly: under --json, a partial/all-ghost batch skip must not leak a
	// bare-plaintext "warning: ...(skipped)" line onto stderr — that corrupts a
	// consumer scraping the --json stream via 2>&1. The per-item skip must route
	// through reportItemError so every non-empty stderr line is a parseable JSON
	// object (the fg6/2j2og/en28 per-item-error contract), while stdout stays the
	// flat JSON array. Mutation-verified LOAD-BEARING: reverting either dep.go
	// skip leg back to fmt.Fprintf(os.Stderr, "warning: ...") makes this RED
	// ("stderr line is not JSON"). Non-JSON stderr shape is unchanged (covered by
	// batch_partial_exits_nonzero_still_lists_valid above).
	t.Run("batch_json_partial_2>&1_is_all_json", func(t *testing.T) {
		// The whole combined 2>&1 stream a `bd dep list ... --json 2>&1 | json`
		// consumer sees must be a sequence of parseable JSON values: the flat
		// dependency array on stdout AND the per-item skip error object on stderr
		// (via reportItemError), with NO interleaved bare-plaintext "warning:
		// ...(skipped)" line. A json.Decoder trips on any non-JSON token — a bare
		// warning line is not a JSON value — so this is RED on the pre-fix
		// fmt.Fprintf(os.Stderr, "warning: ...") legs.
		cmd := exec.Command(bd, "dep", "list", a.ID, "dl-ghost-z", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		combined, cerr := cmd.CombinedOutput()
		if cerr == nil {
			t.Fatalf("expected non-zero exit on a partial batch under --json, got success:\n%s", combined)
		}
		trimmed := strings.TrimSpace(string(combined))
		if trimmed == "" {
			t.Fatalf("combined 2>&1 stream is EMPTY under --json")
		}
		dec := json.NewDecoder(strings.NewReader(trimmed))
		count := 0
		for {
			var v json.RawMessage
			derr := dec.Decode(&v)
			if derr != nil {
				if derr.Error() == "EOF" {
					break
				}
				t.Fatalf("combined 2>&1 stream has a non-JSON (interleaved plain-text warning) token — breaks --json consumers (beads-7kxly):\n%v\ncombined:\n%s", derr, combined)
			}
			count++
		}
		// The dep array (stdout) + the skip error object (stderr) => >=2 values.
		if count < 2 {
			t.Fatalf("expected the dep array AND the per-item skip error as JSON on the 2>&1 stream (>=2 values), got %d:\n%s", count, combined)
		}
	})

	t.Run("batch_all_valid_exits_zero", func(t *testing.T) {
		// two resolvable ids, no failures: rc=0 (regression guard — the fix must
		// not turn a clean batch into a failure).
		bdDep(t, bd, dir, "list", a.ID, b.ID)
	})

	// beads-etz9: an invalid --direction must fail loud, not silently behave as
	// "down". dep list only branches on == "down" / == "up", so a typo'd value
	// used to fall through as the down default and return wrong-direction results
	// with rc=0 (bd dep tree already validated this; dep list did not).
	t.Run("invalid_direction_exits_nonzero", func(t *testing.T) {
		out := bdDepFail(t, bd, dir, "list", a.ID, "--direction", "sideways")
		if !strings.Contains(out, "direction") {
			t.Errorf("expected an invalid-direction error mentioning 'direction', got:\n%s", out)
		}
	})

	t.Run("valid_directions_exit_zero", func(t *testing.T) {
		// both accepted values still work (regression guard: the validation must
		// not reject the legitimate up/down).
		bdDep(t, bd, dir, "list", a.ID, "--direction", "down")
		bdDep(t, bd, dir, "list", a.ID, "--direction", "up")
	})
}
