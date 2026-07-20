//go:build cgo

package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// beads-51m50: `bd notion init --json` and `bd notion connect --json` returned
// their store/auth-setup errors via a bare HandleError, so cobra printed a
// plaintext "Error: ..." line to stderr and exited 1 with EMPTY stdout —
// unparseable by a --json consumer — while the sibling `bd notion sync --json`
// (beads-nqv0) and every `bd ado ... --json` handler (beads-uc71) correctly
// emit a JSON error object on stdout. The fix routes the reachable-under-json
// error paths in runNotionInit/runNotionConnect/runNotionStatus through
// HandleErrorRespectJSON.
//
// These tests exercise the earliest reachable guard on each handler
// (ensureStoreActive) which fires BEFORE any network use: chdir'd into an empty
// dir with store == nil, dbPath == "", and no BEADS_DIR, FindBeadsDir returns
// "" so ensureStoreActive returns "no beads database found". No live Dolt
// server is required — the assertions are fully hermetic.

func setNoStoreNotionState(t *testing.T) {
	t.Helper()
	prevStore, prevCtx, prevJSON, prevDBPath := store, rootCtx, jsonOutput, dbPath
	store = nil
	rootCtx = context.Background()
	jsonOutput = true
	dbPath = ""
	t.Setenv("BEADS_DIR", "")
	t.Setenv("NOTION_TOKEN", "")
	// A fresh empty working directory so FindBeadsDir walks up to nothing.
	t.Chdir(t.TempDir())
	t.Cleanup(func() { store, rootCtx, jsonOutput, dbPath = prevStore, prevCtx, prevJSON, prevDBPath })
}

func assertNotionJSONError(t *testing.T, stdout string) {
	t.Helper()
	s := strings.TrimSpace(stdout)
	if s == "" {
		t.Fatalf("stdout empty on a --json notion error — must emit a JSON error object (beads-51m50)")
	}
	var obj map[string]any
	if jerr := json.Unmarshal([]byte(s), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on --json error: %v\nstdout:\n%s", jerr, s)
	}
	if _, ok := obj["error"]; !ok {
		t.Errorf("expected an \"error\" field in the --json stdout object, got: %s", s)
	}
}

func TestNotionInitJSONErrorContract_51m50(t *testing.T) {
	setNoStoreNotionState(t)

	out, err := captureStdoutExpectErr(t, func() error {
		return runNotionInit(notionInitCmd, nil)
	})
	if err == nil {
		t.Fatalf("expected a non-nil error from notion init with no store, got nil (stdout=%q)", out)
	}
	assertNotionJSONError(t, out)
}

func TestNotionConnectJSONErrorContract_51m50(t *testing.T) {
	setNoStoreNotionState(t)

	out, err := captureStdoutExpectErr(t, func() error {
		return runNotionConnect(notionConnectCmd, nil)
	})
	if err == nil {
		t.Fatalf("expected a non-nil error from notion connect with no store, got nil (stdout=%q)", out)
	}
	assertNotionJSONError(t, out)
}