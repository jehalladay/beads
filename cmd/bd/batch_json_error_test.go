//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestBatchJSONSetupErrorsEmitStdoutObject is the beads-2yhq error-contract
// teeth (8lqh / uc71 / z2b4 / nqv0 class). `bd batch` honors the persistent
// --json on its success path (outputJSON) and its per-op error path
// (outputJSONError), but three SETUP errors returned a bare fmt.Errorf:
// store==nil (batch.go:91), "open batch file" (:106), and "parsing batch input"
// (:116). Under --json (SilenceErrors) those printed plain text to stderr with
// an EMPTY stdout, unparseable by a consumer. They now route through
// HandleErrorRespectJSON (matching sql.go:86). Two of the three fire without a
// server store, so they are reachable in the embedded default mode.
func TestBatchJSONSetupErrorsEmitStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	assertJSONErrorOnStdout := func(t *testing.T, stdout, stderr string) {
		t.Helper()
		out := strings.TrimSpace(stdout)
		if out == "" {
			t.Fatalf("stdout is EMPTY on a failing `bd batch --json` — the error must be a JSON object on stdout (beads-2yhq; plain-text stderr breaks parsers)\nstderr:\n%s", stderr)
		}
		var obj map[string]interface{}
		if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
			t.Fatalf("stdout is not a JSON object on a failing `bd batch --json`: %v\nstdout:\n%s", jerr, out)
		}
		msg, ok := obj["error"].(string)
		if !ok {
			if data, dok := obj["data"].(map[string]interface{}); dok {
				msg, ok = data["error"].(string)
			}
		}
		if !ok || msg == "" {
			t.Errorf("expected a non-empty \"error\" field in failing `bd batch --json` stdout, got: %s", out)
		}
	}

	t.Run("parse_error_bad_command", func(t *testing.T) {
		// An unrecognized batch command makes parseBatchScript fail (batch.go:116).
		cmd := exec.Command(bd, "batch", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		cmd.Stdin = strings.NewReader("frobnicate beads-nope\n")
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err == nil {
			t.Fatalf("`bd batch --json` on a bad script unexpectedly succeeded\nstdout:\n%s", stdout.String())
		}
		assertJSONErrorOnStdout(t, stdout.String(), stderr.String())
	})

	t.Run("open_file_error_missing_path", func(t *testing.T) {
		// A nonexistent --file makes os.Open fail (batch.go:106).
		cmd := exec.Command(bd, "batch", "--json", "--file", "/nonexistent/batch-2yhq.txt")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err == nil {
			t.Fatalf("`bd batch --json --file <missing>` unexpectedly succeeded\nstdout:\n%s", stdout.String())
		}
		assertJSONErrorOnStdout(t, stdout.String(), stderr.String())
	})
}
