//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestDeferJSON_FlagValidationErrorsEmitStdoutObject is the error-contract teeth
// for the beads-xwjg residual 8lqh sweep, defer.go leg. `bd defer` honors the
// persistent --json on its success path (outputJSON of the deferred issues), but
// its flag-validation error paths (--until parse failure, empty --reason) used
// plain HandleError — plain text on stderr, EMPTY stdout — so under --json a
// validation failure produced empty stdout, breaking JSON parsers. The fix
// routes those through HandleErrorRespectJSON, matching the ID-resolve path in
// the same command (which already respected --json, beads-0l4c).
//
// Both triggers are deterministic and server-free: they are validated in RunE
// before any store access, so the teeth need only a bd binary + an inited dir.
// RED (revert either HandleErrorRespectJSON→HandleError): stdout is EMPTY on the
// corresponding subtest and the JSON parse fails.
func TestDeferJSON_FlagValidationErrorsEmitStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "dj")

	// A valid target id is irrelevant — the flag validation fires first. Use a
	// throwaway id so the command doesn't fail on missing positional args.
	issue := bdCreate(t, bd, dir, "defer json-error target", "--type", "task")

	assertStdoutJSONError := func(t *testing.T, args ...string) {
		t.Helper()
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err == nil {
			t.Fatalf("`%s` unexpectedly succeeded\nstdout:\n%s", strings.Join(args, " "), stdout.String())
		}
		out := strings.TrimSpace(stdout.String())
		if out == "" {
			t.Fatalf("stdout is EMPTY on a failing `%s` — the error must be a JSON object on stdout, not plain stderr text (beads-xwjg)\nstderr:\n%s",
				strings.Join(args, " "), stderr.String())
		}
		var obj map[string]any
		if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
			t.Fatalf("stdout is not a JSON object on a failing `%s`: %v\nstdout:\n%s", strings.Join(args, " "), jerr, out)
		}
		msg, ok := obj["error"].(string)
		if !ok {
			if data, dok := obj["data"].(map[string]any); dok {
				msg, ok = data["error"].(string)
			}
		}
		if !ok || msg == "" {
			t.Errorf("expected a non-empty \"error\" field in failing `%s` stdout, got: %s", strings.Join(args, " "), out)
		}
	}

	t.Run("invalid_until_emits_stdout_json", func(t *testing.T) {
		assertStdoutJSONError(t, "defer", issue.ID, "--until", "not-a-real-date-xyz", "--json")
	})

	t.Run("empty_reason_emits_stdout_json", func(t *testing.T) {
		assertStdoutJSONError(t, "defer", issue.ID, "--reason", "", "--json")
	})
}
