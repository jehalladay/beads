//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestTodoDoneJSONStderrContract is the clean-stderr per-item teeth for the
// `bd todo done` args loop (beads-scl2z, 8lqh json-error-contract family;
// sibling of the fg6/92tz/en28/n96g/iwy1k show/update/label/reopen/undefer/
// close/lint sweep).
//
// `bd todo done <ids...>` accepts multiple ids and its resolve/close loop
// (todo.go doneTodoCmd, and the proxied twin todo_proxied_server.go
// runTodoDoneProxiedServer) `continue`s past ids that fail to resolve/close, and
// ALWAYS emits its {"closed":[...],"reason":...} envelope on stdout under --json.
// Before this fix the loop wrote each per-id failure as RAW plaintext to
// os.Stderr ("Error: issue %s not found", "Error: cannot close %s: ...") even
// under --json, so `bd todo done $IDS --json 2>&1 | jq` interleaved plaintext
// with the JSON object -> parse failure, AND the failed id was invisible to a
// --json consumer (it survived only in the plaintext + the exit code). `bd close`
// — which this verb documents itself as a convenience wrapper for — already
// routes per-item errors through the json-aware reportCloseItemError/
// reportItemError; todo done was the odd-one-out still bypassing it.
//
// The fix routes every per-id write through reportItemError (errors.go:250):
// under --json each per-item error is emitted as its OWN JSON object to stderr
// (so a `2>&1` stream is a sequence of parseable JSON values), plaintext
// otherwise.
//
// Mutation-verify: revert any reportItemError call to fmt.Fprintf(os.Stderr,
// ...) and json_stream_parses goes RED (the leading "Error:" plaintext trips the
// per-object json.Decoder on the combined stream).
func TestTodoDoneJSONStderrContract(t *testing.T) {
	bd := buildEmbeddedBD(t)

	// (1) A missing id under --json: the combined (2>&1) stream must be a
	//     sequence of parseable JSON values (the stderr per-item error object +
	//     the stdout {"closed":...} envelope), with NO interleaved plaintext. A
	//     dangling-decoder walk asserts every token parses.
	t.Run("missing_id_json_stream_parses", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "tja")
		stdout, stderr := runTodoDoneJSONStderr(t, bd, dir,
			"todo", "done", "tja-does-not-exist", "--json")
		combined := strings.TrimSpace(stdout + stderr)
		if combined == "" {
			t.Fatalf("beads-scl2z: combined 2>&1 stream is empty under `bd todo done <missing> --json`")
		}
		// The combined stream is a concatenation of independent JSON values (one
		// stderr error object + the stdout {"closed":...} object). A raw plaintext
		// advisory leak makes the decoder fail on a non-JSON token.
		dec := json.NewDecoder(strings.NewReader(combined))
		count := 0
		for {
			var v json.RawMessage
			if err := dec.Decode(&v); err != nil {
				if err.Error() == "EOF" {
					break
				}
				t.Fatalf("beads-scl2z: `bd todo done <missing> --json` 2>&1 is NOT a clean "+
					"JSON stream (a per-item advisory leaked plaintext to stderr): %v\ncombined:\n%s",
					err, combined)
			}
			count++
		}
		if count == 0 {
			t.Fatalf("beads-scl2z: no JSON values decoded from the combined stream:\n%s", combined)
		}
	})

	// (2) The stderr error object under --json must carry the failed id so a
	//     --json consumer can see WHICH id failed (previously the id existed only
	//     in the exit code + the plaintext line).
	t.Run("missing_id_error_object_names_id", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "tjb")
		_, stderr := runTodoDoneJSONStderr(t, bd, dir,
			"todo", "done", "tjb-ghost-9999", "--json")
		if !strings.Contains(stderr, "tjb-ghost-9999") {
			t.Errorf("beads-scl2z: --json stderr error object must name the failed id; stderr:\n%s", stderr)
		}
		// The stderr object must be JSON (an "error" field), not plaintext.
		var obj map[string]any
		if jerr := json.Unmarshal([]byte(strings.TrimSpace(stderr)), &obj); jerr != nil {
			t.Fatalf("beads-scl2z: --json per-item stderr is not a JSON object: %v\nstderr:\n%s", jerr, stderr)
		}
		if _, ok := obj["error"]; !ok {
			t.Errorf("beads-scl2z: --json per-item stderr object lacks an \"error\" field; got: %s", stderr)
		}
	})

	// (3) Positive control: WITHOUT --json a missing id still prints the plain
	//     advisory to stderr (the fix suppresses raw text only under --json).
	//     Proves the not-found branch is genuinely reachable — otherwise (1)/(2)
	//     could pass vacuously.
	t.Run("missing_id_plain_keeps_advisory", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "tjc")
		_, stderr := runTodoDoneJSONStderr(t, bd, dir,
			"todo", "done", "tjc-ghost-plain")
		// A missing id surfaces via GetIssue returning an error ("Error: failed to
		// get issue <id>: ...") or a nil issue ("Error: issue <id> not found") —
		// either way the plain path writes a raw advisory to stderr naming the id.
		if !strings.Contains(stderr, "tjc-ghost-plain") ||
			!(strings.Contains(stderr, "not found") || strings.Contains(stderr, "failed to get issue")) {
			t.Errorf("beads-scl2z: plain (non-json) `bd todo done <missing>` must still print "+
				"the not-found advisory to stderr; stderr:\n%s", stderr)
		}
	})
}

// runTodoDoneJSONStderr runs a bd subprocess capturing stdout and stderr
// SEPARATELY (the defect is a stderr write, so a stdout-only helper would
// false-green). A missing id makes todo done exit non-zero (beads-xi35), which
// is expected here.
func runTodoDoneJSONStderr(t *testing.T, bd, dir string, args ...string) (stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(bd, args...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	so, se, _ := runCommandBuffers(t, cmd)
	// A non-nil error (rc=1 on an unresolvable id) is expected; assert on the
	// streams, not the exit code.
	return so.String(), se.String()
}
