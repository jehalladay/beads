//go:build cgo

package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// beads-yw6g: bd duplicates honors the persistent --json on its success path,
// but its fetch-error return used a plain HandleError (empty stdout + stderr
// text), so under `bd duplicates --json` a store failure was unparseable by a
// --json consumer. The fix routes it through HandleErrorRespectJSON (0wp9/y2yo/
// xwjg/8lqh --json-error-contract class). The store-error path is not forceable
// from a subprocess, so the teeth inject a failing-store double into the package
// `store` global and invoke the command RunE directly, mirroring the
// relate_json_error_test seam.
//
// NOTE (beads-13or dead-branch lesson): the sibling candidate count.go:270 was
// investigated and found NON-load-bearing — bd count's RunE loads the same
// filter config UNCONDITIONALLY at the top (count.go:119, already
// HandleErrorRespectJSON), so a config-load failure is caught there and
// count.go:270's --include-infra reload is never reached on that error. It was
// left as plain HandleError rather than ship a fix whose RED-verify passes.

// failSearchStore fails SearchIssues; everything else delegates to the embedded
// real store (used to reach duplicates.go's fetch-error return).
type failSearchStore struct {
	storage.DoltStorage
}

func (f failSearchStore) SearchIssues(context.Context, string, types.IssueFilter) ([]*types.Issue, error) {
	return nil, errors.New("injected search failure")
}

func TestDuplicatesJSONErrorContract_yw6g(t *testing.T) {
	tmpDir := t.TempDir()
	testDB := filepath.Join(tmpDir, ".beads", "beads.db")
	real := newTestStore(t, testDB)
	ctx := context.Background()

	if err := real.CreateIssue(ctx, &types.Issue{
		ID: "yw6g-1", Title: "seed", Status: types.StatusOpen,
		Priority: 1, IssueType: types.TypeTask, CreatedAt: time.Now(),
	}, "test"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	prevStore, prevCtx, prevJSON := store, rootCtx, jsonOutput
	rootCtx = ctx
	jsonOutput = true
	store = failSearchStore{DoltStorage: real}
	t.Cleanup(func() { store, rootCtx, jsonOutput = prevStore, prevCtx, prevJSON })

	out, err := captureStdoutExpectErr(t, func() error {
		return duplicatesCmd.RunE(duplicatesCmd, nil)
	})
	if err == nil {
		t.Fatalf("expected a non-nil error, got nil (stdout=%q)", out)
	}
	s := strings.TrimSpace(out)
	if s == "" {
		t.Fatalf("stdout empty on `bd duplicates --json` store error — must emit a JSON error object (beads-yw6g), err=%v", err)
	}
	var obj map[string]any
	if jerr := json.Unmarshal([]byte(s), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on --json error: %v\nstdout:\n%s", jerr, s)
	}
	if _, ok := obj["error"]; !ok {
		t.Errorf("expected an \"error\" field in the --json stdout object, got: %s", s)
	}
}
