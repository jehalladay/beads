//go:build cgo

package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestLabelPartialJSONSingleDoc_uctf is the end-to-end regression for
// beads-uctf: on a PARTIAL batch (≥1 resolvable id + ≥1 unresolvable) the
// direct `bd label add`/`remove --json` path wrote TWO concatenated JSON docs
// to STDOUT — (1) the success results array, then (2) a terminal
// HandleErrorRespectJSON error object — so `json.load(stdout)` failed with
// "Extra data". The fix routes the terminal partial-failure summary to STDERR
// and exits non-zero, leaving exactly one JSON doc (the results array) on
// stdout, matching the update.go partial-batch contract (beads-92tz/fg6/4i20).
//
// Asserts, for both add and remove: STDOUT is exactly one parseable JSON array
// (the successes), the process exits non-zero, and stdout is NOT the terminal
// error object.
func TestLabelPartialJSONSingleDoc_uctf(t *testing.T) {
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "lp")

	// A real id (resolvable) + a ghost id (unresolvable) = partial batch.
	real := bdCreate(t, bd, dir, "partial target", "--type", "task")

	t.Run("add", func(t *testing.T) {
		stdout, _, err := runLabelPartial(t, bd, dir, "add", real.ID, "ghost-nope-999", "plbl")
		assertSingleJSONArrayStdout(t, "label add", stdout, err, "added", real.ID, "plbl")
	})

	t.Run("remove", func(t *testing.T) {
		// Seed a label to remove so the real id is a genuine success.
		bdLabel(t, bd, dir, "add", real.ID, "rmlbl")
		stdout, _, err := runLabelPartial(t, bd, dir, "remove", real.ID, "ghost-nope-999", "rmlbl")
		assertSingleJSONArrayStdout(t, "label remove", stdout, err, "removed", real.ID, "rmlbl")
	})
}

// runLabelPartial runs `bd label <args> --json` capturing stdout and stderr
// separately (so we can assert stdout is a single doc, independent of stderr).
func runLabelPartial(t *testing.T, bd, dir string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	full := append([]string{"label"}, args...)
	full = append(full, "--json")
	cmd := exec.Command(bd, full...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	err = cmd.Run()
	return so.String(), se.String(), err
}

// assertSingleJSONArrayStdout verifies stdout is exactly one JSON array holding
// the success record for wantID/wantLabel with wantStatus, the process failed
// (partial batch → rc != 0), and stdout has no trailing second document.
func assertSingleJSONArrayStdout(t *testing.T, label, stdout string, runErr error, wantStatus, wantID, wantLabel string) {
	t.Helper()

	// Partial batch must exit non-zero (rc1) — a caller needs to see the failure.
	if runErr == nil {
		t.Fatalf("%s --json partial batch exited 0; want non-zero so scripts see the failed id\nstdout:\n%s", label, stdout)
	}

	s := strings.TrimSpace(stdout)
	start := strings.IndexByte(s, '[')
	if start != 0 {
		// stdout must START with the array (no leading error object / text).
		t.Fatalf("%s --json stdout does not start with the results array (first byte off): the terminal error must go to stderr, not stdout\nstdout:\n%s", label, stdout)
	}

	// The ENTIRE stdout must decode as ONE array with no trailing data — the
	// pre-uctf bug left a 2nd concatenated object here ("Extra data").
	dec := json.NewDecoder(strings.NewReader(s))
	var results []map[string]interface{}
	if derr := dec.Decode(&results); derr != nil {
		t.Fatalf("%s --json stdout first doc is not a decodable array: %v\nstdout:\n%s", label, derr, stdout)
	}
	if dec.More() {
		t.Fatalf("%s --json stdout has a SECOND JSON document (the uctf double-emit): the terminal error object must go to stderr\nstdout:\n%s", label, stdout)
	}

	// The single success record must be present and shaped right.
	if len(results) != 1 {
		t.Fatalf("%s --json results array = %d records, want 1 (the one resolvable id)\nstdout:\n%s", label, len(results), stdout)
	}
	r := results[0]
	if r["issue_id"] != wantID || r["label"] != wantLabel || r["status"] != wantStatus {
		t.Fatalf("%s --json success record = %+v, want issue_id=%s label=%s status=%s\nstdout:\n%s", label, r, wantID, wantLabel, wantStatus, stdout)
	}
}
