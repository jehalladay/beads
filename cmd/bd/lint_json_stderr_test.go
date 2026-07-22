//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestLintJSONStderrContract is the clean-stderr per-item teeth for the lint
// args-loop leg (beads-iwy1k, 8lqh json-error-contract family;
// sibling of the fg6/92tz/en28/n96g show/update/label/reopen/undefer/close
// sweep).
//
// `bd lint <ids...>` accepts multiple ids and its resolve loop (lint.go)
// `continue`s past ids that don't resolve, and it ALWAYS emits its results
// envelope on stdout under --json (the `if jsonOutput { outputJSON(...) }`
// block). Before this fix the loop wrote the per-id failure as RAW plaintext to
// os.Stderr ("Error getting %s" / "Issue not found: %s") even under --json, so
// `bd lint $IDS --json 2>&1 | jq` interleaved plaintext with the JSON object ->
// parse failure (the beads-p3y5 stderr write was the last args-loop odd-one-out
// still bypassing the json-aware reportItemError helper).
//
// The fix routes both writes through reportItemError (errors.go:250): under
// --json each per-item error is emitted as its OWN JSON object to stderr (so a
// `2>&1` stream is a sequence of JSON objects, each individually parseable),
// plaintext otherwise.
//
// Mutation-verify: revert either reportItemError call to fmt.Fprintf(os.Stderr,
// ...) and json_stream_parses goes RED (the leading "Error getting"/"Issue not
// found:" plaintext trips the per-object json.Decoder on the combined stream).
func TestLintJSONStderrContract(t *testing.T) {
	bd := buildEmbeddedBD(t)

	// (1) A missing id under --json: the combined (2>&1) stream must be a
	//     sequence of parseable JSON values (the stderr per-item error object +
	//     the stdout results envelope), with NO interleaved plaintext. A
	//     dangling-decoder walk asserts every token parses.
	t.Run("missing_id_json_stream_parses", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "lja")
		stdout, stderr := runLintJSONStderr(t, bd, dir,
			"lint", "lja-does-not-exist", "--json")
		combined := strings.TrimSpace(stdout + stderr)
		if combined == "" {
			t.Fatalf("beads-iwy1k: combined 2>&1 stream is empty under `bd lint <missing> --json`")
		}
		// The combined stream is a concatenation of independent JSON values
		// (one stderr error object + the stdout results object). A raw plaintext
		// advisory leak makes the decoder fail on a non-JSON token.
		dec := json.NewDecoder(strings.NewReader(combined))
		count := 0
		for {
			var v json.RawMessage
			if err := dec.Decode(&v); err != nil {
				if err.Error() == "EOF" {
					break
				}
				t.Fatalf("beads-iwy1k: `bd lint <missing> --json` 2>&1 is NOT a clean "+
					"JSON stream (a per-item advisory leaked plaintext to stderr): %v\ncombined:\n%s",
					err, combined)
			}
			count++
		}
		if count == 0 {
			t.Fatalf("beads-iwy1k: no JSON values decoded from the combined stream:\n%s", combined)
		}
	})

	// (2) The stderr error object under --json must carry the failed id so a
	//     --json consumer can see WHICH id failed (previously the id existed
	//     only in the exit code + the plaintext line).
	t.Run("missing_id_error_object_names_id", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "ljb")
		_, stderr := runLintJSONStderr(t, bd, dir,
			"lint", "ljb-ghost-9999", "--json")
		if !strings.Contains(stderr, "ljb-ghost-9999") {
			t.Errorf("beads-iwy1k: --json stderr error object must name the failed id; stderr:\n%s", stderr)
		}
		// The stderr object must be JSON (an "error" field), not plaintext.
		var obj map[string]any
		if jerr := json.Unmarshal([]byte(strings.TrimSpace(stderr)), &obj); jerr != nil {
			t.Fatalf("beads-iwy1k: --json per-item stderr is not a JSON object: %v\nstderr:\n%s", jerr, stderr)
		}
		if _, ok := obj["error"]; !ok {
			t.Errorf("beads-iwy1k: --json per-item stderr object lacks an \"error\" field; got: %s", stderr)
		}
	})

	// (3) Positive control: WITHOUT --json a missing id still prints the plain
	//     advisory to stderr (fix suppresses raw text only under --json). Proves
	//     the not-found branch is genuinely reachable — otherwise (1)/(2) could
	//     pass vacuously.
	t.Run("missing_id_plain_keeps_advisory", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "ljc")
		_, stderr := runLintJSONStderr(t, bd, dir,
			"lint", "ljc-ghost-plain")
		// A missing id surfaces via backend.getIssue returning an error
		// ("Error getting <id>: not found ...") or issue==nil ("Issue not
		// found: <id>") — either way the plain path writes a raw advisory to
		// stderr naming the id.
		if !strings.Contains(stderr, "ljc-ghost-plain") ||
			!(strings.Contains(stderr, "Issue not found") || strings.Contains(stderr, "Error getting")) {
			t.Errorf("beads-iwy1k: plain (non-json) `bd lint <missing>` must still print "+
				"the not-found advisory to stderr; stderr:\n%s", stderr)
		}
	})
}

// runLintJSONStderr runs a bd subprocess capturing stdout and stderr SEPARATELY
// (the defect is a stderr write, so a stdout-only helper would false-green). A
// missing id makes lint exit non-zero (beads-p3y5), which is expected here.
func runLintJSONStderr(t *testing.T, bd, dir string, args ...string) (stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(bd, args...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	so, se, _ := runCommandBuffers(t, cmd)
	// A non-nil error (rc=1 on an unresolvable id) is expected; assert on the
	// streams, not the exit code.
	return so.String(), se.String()
}
