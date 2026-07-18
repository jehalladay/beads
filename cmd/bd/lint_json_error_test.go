//go:build cgo

package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// beads-21xi: bd lint is a documented --json command (success honors jsonOutput),
// but two store-error returns in the no-explicit-IDs branch used a plain
// HandleError before the jsonOutput block: lint.go:118 (loadDirectListFilterConfig
// failure) and lint.go:147 (store.SearchIssues failure). Under `bd lint --json`
// that left stdout empty + stderr text, unparseable by a --json consumer (a CI/
// agent lint gate). The fix routes both through HandleErrorRespectJSON (0wp9/
// y2yo/yw6g --json-error-contract class).
//
// The SearchIssues error isn't forceable from a subprocess, so the teeth inject
// a store double that fails SearchIssues into the package `store` global and
// invoke lintCmd.RunE directly (mirrors yw6g/relate_json_error). The double
// embeds storage.DoltStorage without Inner(), so storage.UnwrapStore reaches the
// override.
type failSearchLintStore struct {
	storage.DoltStorage
}

func (f failSearchLintStore) SearchIssues(context.Context, string, types.IssueFilter) ([]*types.Issue, error) {
	return nil, errors.New("injected search failure")
}

func TestLintJSONErrorContract_21xi(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	prevStore, prevCtx, prevJSON := store, rootCtx, jsonOutput
	rootCtx = ctx
	jsonOutput = true
	store = failSearchLintStore{DoltStorage: real}
	t.Cleanup(func() { store, rootCtx, jsonOutput = prevStore, prevCtx, prevJSON })

	// No explicit IDs → the RunE takes the filter branch, loads config (succeeds
	// via the embedded real store), then hits the failing SearchIssues at :147.
	out, err := captureStdoutExpectErr(t, func() error {
		return lintCmd.RunE(lintCmd, nil)
	})
	if err == nil {
		t.Fatalf("expected a non-nil error, got nil (stdout=%q)", out)
	}
	s := strings.TrimSpace(out)
	if s == "" {
		t.Fatalf("stdout empty on `bd lint --json` store error — must emit a JSON error object (beads-21xi), err=%v", err)
	}
	var obj map[string]any
	if jerr := json.Unmarshal([]byte(s), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on --json error: %v\nstdout:\n%s", jerr, s)
	}
	if _, ok := obj["error"]; !ok {
		t.Errorf("expected an \"error\" field in the --json stdout object, got: %s", s)
	}
}
