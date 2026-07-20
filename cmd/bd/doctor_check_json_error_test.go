//go:build cgo

package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// beads-5vist: `bd doctor --check=pollution --json` returned its store-setup
// error via a bare HandleError, so cobra printed a plaintext "Error: ..." line
// to stderr and exited 1 with EMPTY stdout — unparseable by a --json consumer —
// while the success path (no pollution / pollution list) correctly emits a JSON
// object. Sibling of beads-51m50 (notion setup handlers) and beads-uc71 (ado).
// The fix routes the reachable-under-json error paths in runPollutionCheck (and
// runArtifactsCheck) through HandleErrorRespectJSON.
//
// This test exercises the earliest reachable guard (ensureDirectMode →
// ensureStoreActive): chdir'd into an empty dir with store == nil and no
// BEADS_DIR, FindBeadsDir returns "" so the guard trips "no beads database
// found" BEFORE any store/network use. Fully hermetic.

func TestDoctorPollutionJSONErrorContract_5vist(t *testing.T) {
	prevStore, prevCtx, prevJSON, prevDBPath := store, rootCtx, jsonOutput, dbPath
	store = nil
	rootCtx = context.Background()
	jsonOutput = true
	dbPath = ""
	t.Setenv("BEADS_DIR", "")
	t.Chdir(t.TempDir())
	t.Cleanup(func() { store, rootCtx, jsonOutput, dbPath = prevStore, prevCtx, prevJSON, prevDBPath })

	out, err := captureStdoutExpectErr(t, func() error {
		return runPollutionCheck(".", false, true)
	})
	if err == nil {
		t.Fatalf("expected a non-nil error from doctor pollution with no store, got nil (stdout=%q)", out)
	}
	s := strings.TrimSpace(out)
	if s == "" {
		t.Fatalf("stdout empty on a --json doctor pollution error — must emit a JSON error object (beads-5vist)")
	}
	var obj map[string]any
	if jerr := json.Unmarshal([]byte(s), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on --json error: %v\nstdout:\n%s", jerr, s)
	}
	if _, ok := obj["error"]; !ok {
		t.Errorf("expected an \"error\" field in the --json stdout object, got: %s", s)
	}
}