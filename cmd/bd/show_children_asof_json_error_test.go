//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestShowChildrenAsOfJSONErrorContract_yj1n2 is the error-contract teeth for
// beads-yj1n2. `bd show --children` (showIssueChildren) and `bd show --as-of`
// (showIssueAsOf) are sibling helpers invoked from the SAME show RunE that the
// beads-fg6/92tz/8lqh contract governs, but they emitted per-item failures as
// bare plain-text `fmt.Fprintf(os.Stderr, ...)` regardless of --json — an
// un-parseable line on the stream a `bd show --children X --json 2>&1 | jq`
// consumer sees.
//
// The contract (mirrors the main bd show loop + close.go n96g) on the combined
// 2>&1 stream:
//
//  1. PARTIAL success (one id resolves, one is bogus): stdout carries the
//     payload; the bogus id's error flushes to stderr as a JSON object, never
//     an interleaved plain-text line. The whole 2>&1 stream is a sequence of
//     parseable JSON values.
//  2. WHOLLY-failed (every id bogus): the single stdout JSON error object is
//     the sole error and stderr stays clean — the 2>&1 stream is exactly one
//     JSON object (no empty-stdout + stderr-error 92tz break).
func TestShowChildrenAsOfJSONErrorContract_yj1n2(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "yj")

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
				t.Fatalf("combined 2>&1 stream has a non-JSON (interleaved plain text) token — breaks --json consumers (beads-yj1n2):\n%v\ncombined:\n%s", derr, combined)
			}
			count++
		}
		return count
	}

	// A real parent with one child, so --children has a payload to emit.
	parent := bdCreate(t, bd, dir, "yj1n2 parent", "--type", "epic")
	child := bdCreate(t, bd, dir, "yj1n2 child", "--type", "task")
	bdDep(t, bd, dir, "add", child.ID, parent.ID, "--type", "parent-child")

	// ===== bd show --children =====
	t.Run("children_wholly_failed_is_single_json_object", func(t *testing.T) {
		cmd := exec.Command(bd, "show", "--children", "NOPE-999", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		combined, cerr := cmd.CombinedOutput()
		if cerr == nil {
			t.Fatalf("expected non-zero exit when every id fails --children\ncombined:\n%s", combined)
		}
		if n := scanJSONValues(t, string(combined)); n != 1 {
			t.Fatalf("wholly-failed --children --json 2>&1 must be exactly ONE JSON error object (clean stderr), got %d values:\n%s", n, combined)
		}
		// It must carry an "error" key on stdout.
		var obj map[string]any
		if jerr := json.Unmarshal([]byte(strings.TrimSpace(string(combined))), &obj); jerr != nil {
			t.Fatalf("wholly-failed stdout is not a JSON object: %v\n%s", jerr, combined)
		}
		if _, ok := obj["error"]; !ok {
			t.Errorf("expected an \"error\" field on wholly-failed --children --json stdout, got: %s", combined)
		}
	})

	t.Run("children_partial_success_no_interleaved_plaintext", func(t *testing.T) {
		cmd := exec.Command(bd, "show", "--children", parent.ID, "NOPE-999", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		combined, _ := cmd.CombinedOutput()
		// The whole 2>&1 stream must parse as JSON values (children payload +
		// the deferred per-item error object), no interleaved plain text.
		if n := scanJSONValues(t, string(combined)); n < 2 {
			t.Fatalf("partial-success --children --json 2>&1 must carry the payload AND the deferred per-item error as JSON (>=2 values), got %d:\n%s", n, combined)
		}
	})

	// ===== bd show --as-of HEAD =====
	t.Run("asof_wholly_failed_is_single_json_object", func(t *testing.T) {
		cmd := exec.Command(bd, "show", "--as-of", "HEAD", "NOPE-999", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		combined, cerr := cmd.CombinedOutput()
		if cerr == nil {
			t.Fatalf("expected non-zero exit when every id fails --as-of\ncombined:\n%s", combined)
		}
		if n := scanJSONValues(t, string(combined)); n != 1 {
			t.Fatalf("wholly-failed --as-of --json 2>&1 must be exactly ONE JSON error object (clean stderr), got %d values:\n%s", n, combined)
		}
		var obj map[string]any
		if jerr := json.Unmarshal([]byte(strings.TrimSpace(string(combined))), &obj); jerr != nil {
			t.Fatalf("wholly-failed --as-of stdout is not a JSON object: %v\n%s", jerr, combined)
		}
		if _, ok := obj["error"]; !ok {
			t.Errorf("expected an \"error\" field on wholly-failed --as-of --json stdout, got: %s", combined)
		}
	})

	t.Run("asof_partial_success_no_interleaved_plaintext", func(t *testing.T) {
		cmd := exec.Command(bd, "show", "--as-of", "HEAD", parent.ID, "NOPE-999", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		combined, _ := cmd.CombinedOutput()
		if n := scanJSONValues(t, string(combined)); n < 2 {
			t.Fatalf("partial-success --as-of --json 2>&1 must carry the fetched issue(s) AND the deferred per-item error as JSON (>=2 values), got %d:\n%s", n, combined)
		}
	})
}
