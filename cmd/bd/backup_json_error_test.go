//go:build cgo

package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// beads-51fl LEG 2 (8lqh error-half): bd backup sync/restore/remove returned
// raw fmt.Errorf on their reachable guard/failure paths. Because those commands
// set SilenceErrors, main() prints the returned error as plain text
// "Error: <msg>" to stderr even under --json — breaking a `--json` consumer
// doing json.load on stdout. The fix routes them through HandleErrorRespectJSON
// so --json emits a parseable JSON error object on stdout. These guard paths
// are store-free/hermetic. RED before the fix (empty stdout on --json error).

// assertBackupJSONError asserts a --json error emitted a JSON object carrying an
// "error" field on stdout (not plain text on stderr).
func assertBackupJSONError(t *testing.T, label, stdout string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected a non-nil error, got nil (stdout=%q)", label, stdout)
	}
	out := strings.TrimSpace(stdout)
	if out == "" {
		t.Fatalf("%s: stdout empty on a --json error — must emit a JSON error object (beads-51fl), err=%v", label, err)
	}
	var obj map[string]any
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("%s: stdout is not a JSON object on --json error: %v\nstdout:\n%s", label, jerr, out)
	}
	if _, ok := obj["error"]; !ok {
		t.Errorf("%s: expected an \"error\" field in the --json stdout object, got: %s", label, out)
	}
}

func TestBackupJSONErrorContract_51fl(t *testing.T) {
	prevStore, prevJSON := store, jsonOutput
	store = nil
	jsonOutput = true
	t.Cleanup(func() { store, jsonOutput = prevStore, prevJSON })

	// sync: store == nil is a reachable guard error under --json.
	t.Run("sync_no_store", func(t *testing.T) {
		out, err := captureStdoutExpectErr(t, func() error {
			return backupSyncCmd.RunE(backupSyncCmd, nil)
		})
		assertBackupJSONError(t, "backup sync (no store)", out, err)
	})

	// remove: store == nil is a reachable guard error under --json.
	t.Run("remove_no_store", func(t *testing.T) {
		out, err := captureStdoutExpectErr(t, func() error {
			return backupRemoveCmd.RunE(backupRemoveCmd, nil)
		})
		assertBackupJSONError(t, "backup remove (no store)", out, err)
	})

	// restore: a nonexistent backup dir fails validateBackupRestoreDir before
	// any store access — a reachable guard error that must honor --json.
	t.Run("restore_missing_dir", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "does-not-exist")
		out, err := captureStdoutExpectErr(t, func() error {
			return backupRestoreCmd.RunE(backupRestoreCmd, []string{missing})
		})
		assertBackupJSONError(t, "backup restore (missing dir)", out, err)
	})
}
