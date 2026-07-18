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
)

// beads-927v: bd recompute-blocked is a documented --json command (success path
// honors jsonOutput), but both error returns ran before the success block with a
// plain HandleError — empty stdout + stderr text under --json, unparseable by a
// consumer. The fix routes both through HandleErrorRespectJSON (0wp9/y2yo/xwjg/
// 8lqh --json-error-contract class).
//
// The recompute error isn't forceable from a subprocess, so the teeth inject a
// store double that implements BlockedRecomputer with a failing RecomputeAllBlocked
// into the package `store` global and invoke the RunE directly. The double embeds
// storage.DoltStorage but does NOT expose Inner(), so storage.UnwrapStore returns
// it unchanged and the type-assert to BlockedRecomputer reaches this override.
type failRecomputeStore struct {
	storage.DoltStorage
}

func (f failRecomputeStore) RecomputeAllBlocked(context.Context) (int, error) {
	return 0, errors.New("injected recompute failure")
}

func TestRecomputeBlockedJSONErrorContract_927v(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	prevStore, prevCtx, prevJSON := store, rootCtx, jsonOutput
	rootCtx = ctx
	jsonOutput = true
	store = failRecomputeStore{DoltStorage: real}
	t.Cleanup(func() { store, rootCtx, jsonOutput = prevStore, prevCtx, prevJSON })

	out, err := captureStdoutExpectErr(t, func() error {
		return recomputeBlockedCmd.RunE(recomputeBlockedCmd, nil)
	})
	if err == nil {
		t.Fatalf("expected a non-nil error, got nil (stdout=%q)", out)
	}
	s := strings.TrimSpace(out)
	if s == "" {
		t.Fatalf("stdout empty on `bd recompute-blocked --json` error — must emit a JSON error object (beads-927v), err=%v", err)
	}
	var obj map[string]any
	if jerr := json.Unmarshal([]byte(s), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on --json error: %v\nstdout:\n%s", jerr, s)
	}
	if _, ok := obj["error"]; !ok {
		t.Errorf("expected an \"error\" field in the --json stdout object, got: %s", s)
	}
}
