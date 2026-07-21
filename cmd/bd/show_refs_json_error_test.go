//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestShowRefsJSONErrorContract_0rlll is the error-contract teeth for
// beads-0rlll — the sibling that yj1n2 missed. `bd show --refs` (showIssueRefs)
// is a helper invoked from the SAME show RunE governed by the beads-fg6/92tz/8lqh
// contract, but it emitted per-item failures as bare plain-text
// `fmt.Fprintf(os.Stderr, ...)` regardless of --json — an un-parseable line on
// the stream a `bd show <id> --refs --json 2>&1 | jq` consumer sees, plus the
// 92tz empty-stdout + stderr-error shape on total failure.
//
// The contract (mirrors showIssueChildren/showIssueAsOf, yj1n2) on 2>&1:
//
//  1. PARTIAL success (one id resolves, one is bogus): stdout carries the refs
//     payload; the bogus id's error flushes to stderr as a JSON object, never
//     an interleaved plain-text line — the whole 2>&1 stream is parseable JSON.
//  2. WHOLLY-failed (every id bogus): the single stdout JSON error object is the
//     sole error and stderr stays clean — 2>&1 is exactly one JSON object.
func TestShowRefsJSONErrorContract_0rlll(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "rf")

	// scanJSONValues asserts the combined 2>&1 stream is a sequence of one or
	// more parseable JSON values with NO interleaved plain text (a bare
	// "Error ..." line is not a JSON value, so the decoder trips on it).
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
				t.Fatalf("combined 2>&1 stream has a non-JSON (interleaved plain text) token — breaks --json consumers (beads-0rlll):\n%v\ncombined:\n%s", derr, combined)
			}
			count++
		}
		return count
	}

	// A real issue so --refs has a payload to emit on partial success.
	real := bdCreate(t, bd, dir, "0rlll refs target", "--type", "task")

	t.Run("refs_wholly_failed_is_single_json_object", func(t *testing.T) {
		cmd := exec.Command(bd, "show", "NOPE-999", "--refs", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		combined, cerr := cmd.CombinedOutput()
		if cerr == nil {
			t.Fatalf("expected non-zero exit when every id fails --refs\ncombined:\n%s", combined)
		}
		if n := scanJSONValues(t, string(combined)); n != 1 {
			t.Fatalf("wholly-failed --refs --json 2>&1 must be exactly ONE JSON error object (clean stderr), got %d values:\n%s", n, combined)
		}
		var obj map[string]any
		if jerr := json.Unmarshal([]byte(strings.TrimSpace(string(combined))), &obj); jerr != nil {
			t.Fatalf("wholly-failed --refs stdout is not a JSON object: %v\n%s", jerr, combined)
		}
		if _, ok := obj["error"]; !ok {
			t.Errorf("expected an \"error\" field on wholly-failed --refs --json stdout, got: %s", combined)
		}
	})

	t.Run("refs_partial_success_no_interleaved_plaintext", func(t *testing.T) {
		cmd := exec.Command(bd, "show", real.ID, "NOPE-999", "--refs", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		combined, _ := cmd.CombinedOutput()
		// The whole 2>&1 stream must parse as JSON values (refs payload + the
		// deferred per-item error object), no interleaved plain text.
		if n := scanJSONValues(t, string(combined)); n < 2 {
			t.Fatalf("partial-success --refs --json 2>&1 must carry the payload AND the deferred per-item error as JSON (>=2 values), got %d:\n%s", n, combined)
		}
	})
}
