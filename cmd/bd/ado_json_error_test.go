//go:build cgo

package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// beads-uc71: `bd ado projects --json` and `bd ado sync --json` returned their
// config/setup errors via a bare `fmt.Errorf`, so cobra printed a plaintext
// "Error: ..." line to stderr and exited 1 with EMPTY stdout — unparseable by
// a --json consumer — while the sibling `bd ado status --json` correctly emits
// a JSON error object on stdout. The fix routes the reachable-under-json error
// paths through HandleErrorRespectJSON.
//
// These tests exercise the earliest reachable guard on each handler (missing
// ado.pat / invalid config). That guard fires BEFORE any store or network use:
// with store == nil, dbPath == "", and the ADO env vars cleared,
// getADOConfigValue returns "" via the pure env fallback, so cfg.PAT == "" and
// the guard trips. No live Dolt server is required — the assertions are fully
// hermetic.
func TestADOProjectsJSONErrorContract_uc71(t *testing.T) {
	setEmptyADOEnv(t)
	restoreADOGlobals(t)

	out, err := captureStdoutExpectErr(t, func() error {
		return runADOProjects(adoProjectsCmd, nil)
	})
	if err == nil {
		t.Fatalf("expected a non-nil error from ado projects with no config, got nil (stdout=%q)", out)
	}
	assertADOJSONError(t, out)
}

func TestADOSyncJSONErrorContract_uc71(t *testing.T) {
	setEmptyADOEnv(t)
	restoreADOGlobals(t)

	// validateADOConfig fails on the missing PAT before any sync work runs.
	out, err := captureStdoutExpectErr(t, func() error {
		return runADOSync(adoSyncCmd, nil)
	})
	if err == nil {
		t.Fatalf("expected a non-nil error from ado sync with no config, got nil (stdout=%q)", out)
	}
	assertADOJSONError(t, out)
}

// setEmptyADOEnv clears every ADO env var so getADOConfigValue's env fallback
// returns "" and the missing-config guards trip deterministically.
func setEmptyADOEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"AZURE_DEVOPS_PAT", "AZURE_DEVOPS_ORG", "AZURE_DEVOPS_URL",
		"AZURE_DEVOPS_PROJECT", "AZURE_DEVOPS_PROJECTS",
	} {
		t.Setenv(k, "")
	}
}

// restoreADOGlobals points the package globals the ado handlers read at a
// no-store, no-dbPath state under --json, restoring them after the test.
func restoreADOGlobals(t *testing.T) {
	t.Helper()
	prevStore, prevCtx, prevJSON, prevDBPath := store, rootCtx, jsonOutput, dbPath
	store = nil
	rootCtx = context.Background()
	jsonOutput = true
	dbPath = ""
	t.Cleanup(func() { store, rootCtx, jsonOutput, dbPath = prevStore, prevCtx, prevJSON, prevDBPath })
}

// assertADOJSONError asserts stdout is a single JSON object carrying an "error"
// field — the shape HandleErrorRespectJSON emits under --json.
func assertADOJSONError(t *testing.T, stdout string) {
	t.Helper()
	s := strings.TrimSpace(stdout)
	if s == "" {
		t.Fatalf("stdout empty on a --json ado error — must emit a JSON error object (beads-uc71)")
	}
	var obj map[string]any
	if jerr := json.Unmarshal([]byte(s), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on --json error: %v\nstdout:\n%s", jerr, s)
	}
	if _, ok := obj["error"]; !ok {
		t.Errorf("expected an \"error\" field in the --json stdout object, got: %s", s)
	}
}
