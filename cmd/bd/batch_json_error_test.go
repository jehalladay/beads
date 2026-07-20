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

// TestBatchJSONPerOpErrorsEmitStdoutObject is the beads-vgb94 error-contract
// teeth — the PER-OP / transaction-error twin of beads-2yhq (which fixed only
// the SETUP legs). The two per-op legs (guardBatchCloses at batch.go:176 and the
// transact loop at :196) routed their error through outputJSONError, which
// writes the JSON to os.Stderr (output.go:145) and leaves STDOUT EMPTY — exactly
// the failure TestBatchJSONSetupErrorsEmitStdoutObject bans, on the legs it
// didn't cover. A `bd batch --json` consumer that fails mid-transaction (or on a
// close-guard violation) got an unparseable empty stdout. The fix routes both
// legs through HandleErrorRespectJSON (matching the setup legs), so the error is
// a single JSON object on STDOUT with rc=1 and no plaintext double-print.
func TestBatchJSONPerOpErrorsEmitStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	assertJSONErrorOnStdout := func(t *testing.T, stdout, stderr string) {
		t.Helper()
		out := strings.TrimSpace(stdout)
		if out == "" {
			t.Fatalf("stdout is EMPTY on a failing per-op `bd batch --json` — the error must be a JSON object on stdout (beads-vgb94; JSON-on-stderr + empty-stdout breaks parsers)\nstderr:\n%s", stderr)
		}
		var obj map[string]interface{}
		if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
			t.Fatalf("stdout is not a JSON object on a failing per-op `bd batch --json`: %v\nstdout:\n%s", jerr, out)
		}
		msg, ok := obj["error"].(string)
		if !ok {
			if data, dok := obj["data"].(map[string]interface{}); dok {
				msg, ok = data["error"].(string)
			}
		}
		if !ok || msg == "" {
			t.Errorf("expected a non-empty \"error\" field in failing per-op `bd batch --json` stdout, got: %s", out)
		}
	}

	t.Run("transact_error_close_nonexistent", func(t *testing.T) {
		// `close <nonexistent>` passes guardBatchCloses (which skips ids it can't
		// read) and fails authoritatively inside the write transaction at
		// tx.CloseIssue, hitting the transact error leg (batch.go:196).
		cmd := exec.Command(bd, "batch", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		cmd.Stdin = strings.NewReader("close beads-nope999\n")
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err == nil {
			t.Fatalf("`bd batch --json` closing a nonexistent id unexpectedly succeeded\nstdout:\n%s", stdout.String())
		}
		assertJSONErrorOnStdout(t, stdout.String(), stderr.String())
	})

	t.Run("guard_error_close_epic_with_open_child", func(t *testing.T) {
		// An epic with an open child cannot be closed without --force; the
		// close-time integrity pre-pass (guardBatchCloses) aborts the batch,
		// hitting the guard error leg (batch.go:176).
		epic := bdCreate(t, bd, dir, "vgb94 epic", "--type", "epic")
		child := bdCreate(t, bd, dir, "vgb94 open child", "--type", "task")
		// child depends-on epic (parent-child edge): epic has an open child.
		// `bd dep add <child> <epic> --type parent-child` maps child->IssueID,
		// epic->DependsOnID (dep.go: the type is a flag, not a positional).
		depCmd := exec.Command(bd, "dep", "add", child.ID, epic.ID, "--type", "parent-child")
		depCmd.Dir = dir
		depCmd.Env = bdEnv(dir)
		if out, derr := depCmd.CombinedOutput(); derr != nil {
			t.Fatalf("dep add (parent-child) failed: %v\n%s", derr, out)
		}

		cmd := exec.Command(bd, "batch", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		cmd.Stdin = strings.NewReader("close " + epic.ID + "\n")
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err == nil {
			t.Fatalf("`bd batch --json` closing an epic with an open child unexpectedly succeeded\nstdout:\n%s", stdout.String())
		}
		assertJSONErrorOnStdout(t, stdout.String(), stderr.String())
	})
}
