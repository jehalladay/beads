//go:build cgo

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestShowMessageThreadJSONErrorContract is the error-contract teeth for the
// `bd show <id> --thread --json` path (showMessageThread). Its GetIssue-failure
// leg (show_thread.go:23) used bare HandleError, emitting plain-text
// "Error: ..." to STDERR even under --json — so a
// `bd show ... --thread --json 2>&1 | json.load` consumer trips on the
// interleaved plain-text line. Per the beads-fg6/8lqh contract the error must
// be a JSON object on STDOUT (jsonStdoutError), matching the success path which
// encodes the thread to stdout. This leg is reachable when the resolved id is
// absent from the global `store` (e.g. a cross-repo routed thread id whose
// re-fetch on the local store misses).
func TestShowMessageThreadJSONErrorContract(t *testing.T) {
	tmpDir := t.TempDir()
	testStore := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	// Install the empty test store as the package global showMessageThread reads,
	// and flip --json on. Restore all three on cleanup.
	oldStore, oldDBPath, oldJSON := store, dbPath, jsonOutput
	store, jsonOutput = testStore, true
	t.Cleanup(func() { store, dbPath, jsonOutput = oldStore, oldDBPath, oldJSON })

	stdout, stderr := captureThreadStdoutStderr(t, func() {
		// The store is empty → GetIssue("no-such-msg-id") returns ErrNotFound →
		// drives the L23 error leg.
		_ = showMessageThread(ctx, "no-such-msg-id", true)
	})

	if strings.Contains(stderr, "Error:") {
		t.Fatalf("--json error leaked plain-text to STDERR (violates beads-fg6 contract):\nstderr:\n%s", stderr)
	}

	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		t.Fatalf("expected a JSON error object on STDOUT under --json, got empty stdout (stderr:\n%s)", stderr)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		t.Fatalf("STDOUT under --json is not a parseable JSON object (violates contract): %v\nstdout:\n%s", err, stdout)
	}
	if _, ok := obj["error"]; !ok {
		t.Fatalf("JSON error object on STDOUT is missing an \"error\" key: %v", obj)
	}
}

// captureThreadStdoutStderr redirects os.Stdout and os.Stderr around fn and
// returns what each captured.
func captureThreadStdoutStderr(t *testing.T, fn func()) (string, string) {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout, os.Stderr = wOut, wErr

	outDone := make(chan string)
	errDone := make(chan string)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, e := rOut.Read(buf)
			if n > 0 {
				b.Write(buf[:n])
			}
			if e != nil {
				break
			}
		}
		outDone <- b.String()
	}()
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, e := rErr.Read(buf)
			if n > 0 {
				b.Write(buf[:n])
			}
			if e != nil {
				break
			}
		}
		errDone <- b.String()
	}()

	fn()

	_ = wOut.Close()
	_ = wErr.Close()
	outStr := <-outDone
	errStr := <-errDone
	os.Stdout, os.Stderr = origOut, origErr
	return outStr, errStr
}
