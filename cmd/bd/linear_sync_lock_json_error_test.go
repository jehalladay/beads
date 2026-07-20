package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/linear"
)

// beads-7v628: `bd linear sync --json` honors the --json error contract for the
// state-validation and prefer-conflict guards, but the sync-lock-acquire block
// (linear.go:280/283/285) used a bare HandleError, so when another sync holds
// the lock a --json consumer got a plaintext "Error: ..." line on stderr and an
// EMPTY stdout — unparseable. This is the residual the ru7wr note missed (it
// claimed "linear sync --json honors the contract", true only for the earlier
// guards). The fix routes the 3 lock sites through HandleErrorRespectJSON.
//
// Hermetic: hold the lock in-process via linear.AcquireSyncLock, chdir into that
// dir (AcquireSyncLock MkdirAll's the .beads dir it locks), set --no-wait so the
// second acquire is non-blocking, and call runLinearSync directly. store is nil
// but the lock-held return fires BEFORE any store/auth use.
func TestLinearSyncLockHeldJSONErrorContract_7v628(t *testing.T) {
	root := t.TempDir()
	beadsDir := filepath.Join(root, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	// hasBeadsProjectFiles only needs a metadata.json to exist so FindBeadsDir
	// resolves this dir (no live store required to reach the lock-acquire path).
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write metadata.json: %v", err)
	}

	// Hold the lock so the command's acquire fails as "already running".
	held, err := linear.AcquireSyncLock(beadsDir, false)
	if err != nil {
		t.Fatalf("failed to pre-acquire sync lock: %v", err)
	}
	t.Cleanup(func() { _ = held.Release() })

	prevStore, prevCtx, prevJSON, prevDBPath := store, rootCtx, jsonOutput, dbPath
	store = nil
	rootCtx = context.Background()
	jsonOutput = true
	dbPath = ""
	t.Setenv("BEADS_DIR", beadsDir)
	t.Chdir(root)
	t.Cleanup(func() { store, rootCtx, jsonOutput, dbPath = prevStore, prevCtx, prevJSON, prevDBPath })

	// --no-wait makes the second acquire non-blocking → SyncLockHeldError.
	if err := linearSyncCmd.Flags().Set("no-wait", "true"); err != nil {
		t.Fatalf("set --no-wait: %v", err)
	}
	t.Cleanup(func() { _ = linearSyncCmd.Flags().Set("no-wait", "false") })

	out, runErr := captureStdoutExpectErr(t, func() error {
		return runLinearSync(linearSyncCmd, nil)
	})
	if runErr == nil {
		t.Fatalf("expected linear sync to fail with lock held, got nil (stdout=%q)", out)
	}
	s := strings.TrimSpace(out)
	if s == "" {
		t.Fatalf("stdout empty on a --json linear sync lock-held error — must emit a JSON error object (beads-7v628)")
	}
	var obj map[string]any
	if jerr := json.Unmarshal([]byte(s), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on --json error: %v\nstdout:\n%s", jerr, s)
	}
	if _, ok := obj["error"]; !ok {
		t.Errorf("expected an \"error\" field in the --json stdout object, got: %s", s)
	}
}
