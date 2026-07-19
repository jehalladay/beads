//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestUndeferJSONStderrContract_bqs9 is the error-contract teeth for
// beads-bqs9. The DIRECT (non-proxied) path of `bd undefer` emitted an UNGATED
// plain-text per-item error to STDERR inside its resolve/mutate loop under
// --json — specifically the "is not deferred (status: ...)" guard, which
// rejects a resolvable-but-open issue. `bd undefer` ALSO emits a terminal
// HandleErrorRespectJSON JSON object to stdout on a wholly-failed batch AND an
// outputJSON array on partial success, so a `bd undefer <ids> --json 2>&1 |
// json.load` consumer got a MIXED stream (plain text + JSON) → JSONDecodeError.
//
// Direction-2 (extra interleaved stderr) sibling of beads-en28 (label/reopen)
// and beads-n96g (close).
//
// SCOPE NOTE: the in-loop "Error resolving" branches in both defer.go and
// undefer.go are DEAD — an atomic `utils.ResolvePartialIDs` pre-check
// (beads-0l4c / beads-7pcm) bails the whole batch on any unresolvable id BEFORE
// the loop, so a nonexistent id never reaches the per-item resolve. defer.go
// has NO other deterministically-reachable in-loop rejection (its remaining
// stderr sites are reason-gated GetIssue/UpdateIssue TOCTOU), so its deferral
// would be unreachable veneer and is intentionally NOT changed. undefer.go's
// "is not deferred" guard IS reachable (a resolvable open issue), which is what
// this test exercises.
//
// Two invariants on the COMBINED (2>&1) stream:
//  1. WHOLLY-failed (every id rejected): exactly ONE JSON object (terminal
//     stdout error); stderr stays clean (deferred item errors NOT flushed).
//  2. PARTIAL success (one undeferred, one rejected): stdout carries the
//     result array; the rejection flushes to stderr as a JSON object, never as
//     an interleaved plain-text line. The whole 2>&1 stream parses as JSON.
func TestUndeferJSONStderrContract_bqs9(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "du")

	// scanJSONValues asserts the combined 2>&1 stream is a sequence of one or
	// more parseable JSON values with NO interleaved plain text. An interleaved
	// "X is not deferred ..." line is not a JSON value, so the decoder trips.
	scanJSONValues := func(t *testing.T, combined string) int {
		t.Helper()
		trimmed := strings.TrimSpace(combined)
		if trimmed == "" {
			t.Fatalf("combined 2>&1 stream is EMPTY — expected JSON output")
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
				t.Fatalf("combined 2>&1 stream has a non-JSON (interleaved plain text) token — breaks --json consumers (beads-bqs9):\n%v\ncombined:\n%s", derr, combined)
			}
			count++
		}
		return count
	}

	t.Run("wholly_failed_2>&1_is_single_json_object", func(t *testing.T) {
		// A GENUINELY-failed batch (every id unresolvable) bails at the top
		// atomic ResolvePartialIDs pre-check -> a single terminal stdout JSON
		// error, clean stderr. beads-36iz0 NOTE: two resolvable-but-not-deferred
		// ids are NO LONGER a wholly-failed batch — not-deferred is now an
		// idempotent rc0 no-op (covered by TestEmbeddedUndefer), so this invariant
		// is exercised with unresolvable ids, which is the real wholly-failed path.
		cmd := exec.Command(bd, "undefer", "du-nope-a1", "du-nope-a2", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		combined, cerr := cmd.CombinedOutput()
		if cerr == nil {
			t.Fatalf("expected non-zero exit when every id is unresolvable\ncombined:\n%s", combined)
		}
		if n := scanJSONValues(t, string(combined)); n != 1 {
			t.Fatalf("expected exactly ONE JSON object on wholly-failed undefer --json 2>&1 (terminal error only, clean stderr), got %d values:\n%s", n, combined)
		}
	})

	t.Run("partial_success_2>&1_no_interleaved_plaintext", func(t *testing.T) {
		def := bdCreate(t, bd, dir, "bqs9 undefer target", "--type", "task")
		// Defer it first so undefer has a real deferred->open transition.
		deferCmd := exec.Command(bd, "defer", def.ID)
		deferCmd.Dir = dir
		deferCmd.Env = bdEnv(dir)
		if out, derr := deferCmd.CombinedOutput(); derr != nil {
			t.Fatalf("setup defer failed: %v\n%s", derr, out)
		}
		open := bdCreate(t, bd, dir, "bqs9 undefer open B", "--type", "task")
		// def undefers; open hits the not-deferred guard -> partial success.
		cmd := exec.Command(bd, "undefer", def.ID, open.ID, "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		combined, _ := cmd.CombinedOutput()
		if n := scanJSONValues(t, string(combined)); n < 2 {
			t.Fatalf("expected the undeferred[] array AND the not-deferred per-item error as JSON on the 2>&1 stream (>=2 values), got %d:\n%s", n, combined)
		}
	})
}
