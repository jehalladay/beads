//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestDoltRemoteJSONErrorEmitsStdoutObject is the beads-0bxgs error-contract
// teeth (8lqh / vgb94 / 2yhq / outputJSONError class). `bd dolt remote
// add/list/remove` honor the persistent --json on their success paths
// (outputJSON -> stdout), but their error legs hand-rolled
// `if jsonOutput { outputJSONError(err, code) } else { Fprintf(stderr) }; os.Exit(1)`.
// outputJSONError (output.go:128) encodes the error object to os.STDERR, so
// under --json stdout was EMPTY and the JSON error landed on stderr — the exact
// banned failure mode a --json consumer reading stdout cannot parse. The three
// sites now route through FatalErrorRespectJSON (the os.Exit-path twin of
// HandleErrorRespectJSON): a single {error, schema_version} JSON object on
// STDOUT, clean stderr, exit 1.
//
// `remote remove` of a nonexistent remote makes DOLT_REMOTE('remove', name)
// fail, reachable in the embedded default mode.
func TestDoltRemoteJSONErrorEmitsStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	cmd := exec.Command(bd, "dolt", "remote", "remove", "no-such-remote", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err == nil {
		t.Fatalf("`bd dolt remote remove <missing> --json` unexpectedly succeeded\nstdout:\n%s", stdout.String())
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("stdout is EMPTY on a failing `bd dolt remote remove --json` — the error must be a JSON object on stdout (beads-0bxgs; plain-text/JSON-on-stderr breaks parsers)\nstderr:\n%s", stderr.String())
	}
	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on a failing `bd dolt remote remove --json`: %v\nstdout:\n%s", jerr, out)
	}
	msg, ok := obj["error"].(string)
	if !ok {
		if data, dok := obj["data"].(map[string]interface{}); dok {
			msg, ok = data["error"].(string)
		}
	}
	if !ok || msg == "" {
		t.Errorf("expected a non-empty \"error\" field in failing `bd dolt remote remove --json` stdout, got: %s", out)
	}
}
