//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestCloseJSONStderrContract_n96g is the error-contract teeth for beads-n96g.
// The DIRECT (non-proxied) path of `bd close` emitted UNGATED plain-text
// per-item VALIDATION errors to STDERR under --json (close.go: epic-open-child,
// gate, blockers, close-error, validateIssueClosable). resolveCloseTargets is
// atomic — a nonexistent id bails the whole batch EARLIER via
// HandleErrorRespectJSON — so the gap is in-loop guard failures on EXISTING ids.
//
// Two directions of the beads-fg6/en28 --json contract must hold on the
// COMBINED (2>&1) stream a `bd close ... --json 2>&1 | json.load` consumer sees:
//
//  1. PARTIAL success (one id closes, one is guard-rejected): stdout carries
//     the closed[] array, so the rejected id's message must NOT interleave as
//     plain text — it flushes to stderr as a JSON object. The whole 2>&1 stream
//     must be a sequence of parseable JSON values (no bare plain-text line).
//  2. WHOLLY-failed (every id guard-rejected): the single stdout JSON error
//     object is the sole error and stderr stays clean — the 2>&1 stream is
//     exactly one JSON object.
//
// Before the fix, both cases interleaved a plain "cannot close ..." line onto
// the stream and json parsing broke.
func TestCloseJSONStderrContract_n96g(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "nc")

	// scanJSONValues asserts the combined 2>&1 stream is a sequence of one or
	// more parseable JSON values with NO interleaved plain text. An interleaved
	// "cannot close ..." line is not a JSON value, so the decoder trips on it.
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
				t.Fatalf("combined 2>&1 stream has a non-JSON (interleaved plain text) token — breaks --json consumers (beads-n96g):\n%v\ncombined:\n%s", derr, combined)
			}
			count++
		}
		return count
	}

	t.Run("wholly_failed_2>&1_is_single_json_object", func(t *testing.T) {
		blocker := bdCreate(t, bd, dir, "n96g blocker A", "--type", "task")
		blocked1 := bdCreate(t, bd, dir, "n96g blocked A1", "--type", "task")
		blocked2 := bdCreate(t, bd, dir, "n96g blocked A2", "--type", "task")
		bdDep(t, bd, dir, "add", blocked1.ID, "--blocked-by", blocker.ID)
		bdDep(t, bd, dir, "add", blocked2.ID, "--blocked-by", blocker.ID)

		cmd := exec.Command(bd, "close", blocked1.ID, blocked2.ID, "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		combined, cerr := cmd.CombinedOutput()
		if cerr == nil {
			t.Fatalf("expected non-zero exit when every id is guard-rejected\ncombined:\n%s", combined)
		}
		n := scanJSONValues(t, string(combined))
		// Wholly-failed: exactly the terminal stdout JSON error object, nothing
		// on stderr (deferred item errors are intentionally NOT flushed).
		if n != 1 {
			t.Fatalf("expected exactly ONE JSON object on wholly-failed --json 2>&1 (terminal error only, clean stderr), got %d values:\n%s", n, combined)
		}
	})

	t.Run("partial_success_2>&1_no_interleaved_plaintext", func(t *testing.T) {
		blocker := bdCreate(t, bd, dir, "n96g blocker B", "--type", "task")
		blocked := bdCreate(t, bd, dir, "n96g blocked B", "--type", "task")
		okToClose := bdCreate(t, bd, dir, "n96g closeable B", "--type", "task")
		bdDep(t, bd, dir, "add", blocked.ID, "--blocked-by", blocker.ID)

		// okToClose closes; blocked is guard-rejected -> partial success. stdout
		// gets the closed[] array; the rejection must flush to stderr as JSON,
		// never as an interleaved plain-text line.
		cmd := exec.Command(bd, "close", okToClose.ID, blocked.ID, "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		combined, _ := cmd.CombinedOutput()
		// The whole 2>&1 stream must parse as JSON values (array + error object),
		// with no interleaved plain text.
		n := scanJSONValues(t, string(combined))
		if n < 2 {
			t.Fatalf("expected the closed[] array AND the deferred per-item error as JSON on the 2>&1 stream (>=2 values), got %d:\n%s", n, combined)
		}
	})
}
