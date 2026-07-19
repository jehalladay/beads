//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestRenamePrefixJSONContract_qpiw is the beads-qpiw regression. `bd
// rename-prefix` has a --json success branch (rename_prefix.go) emitting a JSON
// result object, but it was doubly broken under --json:
//  1. human progress/success text was fmt.Printf'd to stdout BEFORE the JSON
//     object, so `bd rename-prefix X --json | jq` failed to parse the happy
//     path ("Renaming...\n{...}"), and
//  2. every error return used HandleError/HandleErrorWithHint (plain-text
//     stderr, EMPTY stdout), unparseable by a --json consumer.
//
// The fix gates the human prints behind !jsonOutput (ado.go precedent) and
// routes the reachable error returns through HandleErrorRespectJSON. Both
// subtests run the real binary in embedded mode.
func TestRenamePrefixJSONContract_qpiw(t *testing.T) {
	bd := buildEmbeddedBD(t)

	t.Run("success_stdout_is_pure_json", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "rpa")
		bdCreate(t, bd, dir, "qpiw seed", "--type", "task")

		cmd := exec.Command(bd, "rename-prefix", "rpb", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("`bd rename-prefix rpb --json` failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		out := strings.TrimSpace(stdout.String())
		// Must be a single clean JSON object — no leading "Renaming..." text.
		var obj map[string]interface{}
		if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
			t.Fatalf("stdout is not pure JSON on a successful `bd rename-prefix --json` (human text leaked before the object?): %v\nstdout:\n%s", jerr, out)
		}
		if obj["new_prefix"] != "rpb" {
			t.Errorf("expected new_prefix=rpb in JSON, got: %s", out)
		}
	})

	t.Run("error_stdout_is_json_object", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "rpc")
		bdCreate(t, bd, dir, "qpiw seed", "--type", "task")

		// An invalid prefix trips validatePrefix (fires after the store is
		// active), a reachable error under --json.
		cmd := exec.Command(bd, "rename-prefix", "Bad_Prefix!", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err == nil {
			t.Fatalf("`bd rename-prefix Bad_Prefix! --json` unexpectedly succeeded\nstdout:\n%s", stdout.String())
		}
		out := strings.TrimSpace(stdout.String())
		if out == "" {
			t.Fatalf("stdout is EMPTY on a failing `bd rename-prefix --json` — the error must be a JSON object on stdout (beads-qpiw)\nstderr:\n%s", stderr.String())
		}
		var obj map[string]interface{}
		if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
			t.Fatalf("stdout is not a JSON object on a failing `bd rename-prefix --json`: %v\nstdout:\n%s", jerr, out)
		}
		msg, ok := obj["error"].(string)
		if !ok {
			if data, dok := obj["data"].(map[string]interface{}); dok {
				msg, ok = data["error"].(string)
			}
		}
		if !ok || msg == "" {
			t.Errorf("expected a non-empty \"error\" field in failing `bd rename-prefix --json` stdout, got: %s", out)
		}
	})
}
