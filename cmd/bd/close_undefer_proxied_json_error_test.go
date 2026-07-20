//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestProxiedCloseUndeferJSONStderrContract_2j2og is the error-contract teeth
// for beads-2j2og. The PROXIED-server handlers for `bd close` and `bd undefer`
// emitted their per-item guard/no-op/error legs as BARE plaintext
// fmt.Fprintf(os.Stderr, ...), --json-blind — while their DIRECT twins route
// every per-item message through a JSON-aware reporter that emits {error,
// schema_version} objects under --json. So a proxied-mode (hub-connected) crew
// scripting `bd close <ids> --json` / `bd undefer <ids> --json` got a MIXED
// stream (JSON stdout array + interleaved plain-text stderr) on any partial
// batch → JSONDecodeError.
//
// This is the proxied twin of beads-en28 (label/reopen), beads-n96g (close
// direct), and beads-bqs9 (undefer direct); filed separately because close +
// undefer are disjoint verbs/files, each with its own leg fixed.
//
// The invariant (per verb, on the COMBINED 2>&1 stream):
//   - PARTIAL success (one item succeeds, one hits a per-item guard/no-op):
//     stdout carries the result array; the rejection/no-op flushes to stderr as
//     a JSON object, never as an interleaved plain-text line, so the whole
//     2>&1 stream is a sequence of parseable JSON values.
//
// scanJSONValues (defined in defer_undefer_json_stderr_test.go) is the
// discriminator: a bare plaintext "X is not deferred ..." / "cannot close X:
// blocked by ..." line is not a JSON value, so the decoder trips.
func TestProxiedCloseUndeferJSONStderrContract_2j2og(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// scanJSONValuesProxied mirrors the direct-path scanJSONValues: the combined
	// 2>&1 stream must be one-or-more parseable JSON values with NO interleaved
	// plain text. Duplicated locally (not exported from the direct test) to keep
	// the proxied teeth self-contained.
	scanJSONValuesProxied := func(t *testing.T, combined string) int {
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
				t.Fatalf("combined 2>&1 stream has a non-JSON (interleaved plain text) token — breaks --json consumers (beads-2j2og):\n%v\ncombined:\n%s", derr, combined)
			}
			count++
		}
		return count
	}

	runCombined := func(t *testing.T, dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = bdProxiedEnv(dir)
		combined, _ := cmd.CombinedOutput()
		return string(combined)
	}

	// close: a batch of {a closeable issue, a blocked issue} under --json. The
	// closeable one lands on the stdout closed[] array; the blocked one hits the
	// "cannot close X: blocked by ..." per-item guard, which must flush to stderr
	// as a JSON object (not bare plaintext) so 2>&1 parses cleanly.
	t.Run("close_partial_2>&1_no_interleaved_plaintext", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cj2")
		ok := bdProxiedCreate(t, bd, p.dir, "closeable", "--type", "task")
		blocker := bdProxiedCreate(t, bd, p.dir, "blocker", "--type", "task")
		blocked := bdProxiedCreate(t, bd, p.dir, "blocked", "--type", "task", "--deps", "depends-on:"+blocker.ID)
		combined := runCombined(t, p.dir, "close", ok.ID, blocked.ID, "--json")
		if n := scanJSONValuesProxied(t, combined); n < 2 {
			t.Fatalf("expected the closed[] array AND the blocked per-item error as JSON on the 2>&1 stream (>=2 values), got %d:\n%s", n, combined)
		}
	})

	// undefer: a batch of {a deferred issue, an already-open issue} under --json.
	// The deferred one lands on the stdout undeferred[] array; the open one hits
	// the "is not deferred (status: open)" idempotent no-op, which must flush to
	// stderr as a JSON object (not bare plaintext).
	t.Run("undefer_partial_2>&1_no_interleaved_plaintext", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "uj2")
		deferred := bdProxiedCreate(t, bd, p.dir, "deferred", "--type", "task", "--defer", "+8760h")
		open := bdProxiedCreate(t, bd, p.dir, "already open", "--type", "task")
		combined := runCombined(t, p.dir, "undefer", deferred.ID, open.ID, "--json")
		if n := scanJSONValuesProxied(t, combined); n < 2 {
			t.Fatalf("expected the undeferred[] array AND the not-deferred per-item no-op as JSON on the 2>&1 stream (>=2 values), got %d:\n%s", n, combined)
		}
	})

	// undefer no-op-only: a batch where every id is already open. Nothing is
	// undeferred (empty stdout array), so the advisory no-ops must flush to
	// stderr as JSON objects rather than being dropped or emitted as plaintext.
	// rc stays 0 (idempotent no-op).
	t.Run("undefer_noop_only_2>&1_is_json", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "un2")
		a := bdProxiedCreate(t, bd, p.dir, "open A", "--type", "task")
		b := bdProxiedCreate(t, bd, p.dir, "open B", "--type", "task")
		cmd := exec.Command(bd, "undefer", a.ID, b.ID, "--json")
		cmd.Dir = p.dir
		cmd.Env = bdProxiedEnv(p.dir)
		combined, cerr := cmd.CombinedOutput()
		if cerr != nil {
			t.Fatalf("expected rc=0 on a not-deferred-only undefer --json (idempotent no-op, beads-36iz0), got: %v\ncombined:\n%s", cerr, combined)
		}
		if n := scanJSONValuesProxied(t, string(combined)); n < 2 {
			t.Fatalf("expected both not-deferred no-ops as JSON objects on the 2>&1 stream (>=2 values), got %d:\n%s", n, combined)
		}
	})
}
