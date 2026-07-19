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

// beads-huz3: `bd count` supports --json (it marshals {"count": N}), and loads
// the filter config twice — once unconditionally at the top of RunE
// (count.go:132, already HandleErrorRespectJSON) and again inside the
// --include-infra branch (count.go:300). That second load used a bare
// HandleError before the fix, so under `--include-infra --json` a
// store/config failure left stdout empty + text on stderr, unparseable by a
// --json consumer. The fix routes it through HandleErrorRespectJSON.
//
// The two loads both call GetCustomStatusesDetailed, so to exercise the SECOND
// (--include-infra) load specifically the double succeeds on the first call and
// fails on the second — the first load must succeed to reach line 300 at all.
type countInfraFailStore struct {
	storage.DoltStorage
	calls int
}

func (f *countInfraFailStore) GetCustomStatusesDetailed(ctx context.Context) ([]types.CustomStatus, error) {
	f.calls++
	if f.calls == 1 {
		return f.DoltStorage.GetCustomStatusesDetailed(ctx)
	}
	return nil, errors.New("injected config-load failure")
}

func TestCountJSONErrorContract_huz3(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	prevStore, prevCtx, prevJSON := store, rootCtx, jsonOutput
	rootCtx = ctx
	jsonOutput = true
	store = &countInfraFailStore{DoltStorage: real}
	t.Cleanup(func() { store, rootCtx, jsonOutput = prevStore, prevCtx, prevJSON })

	// Drive the --include-infra branch so the second (failing) config load runs.
	if err := countCmd.Flags().Set("include-infra", "true"); err != nil {
		t.Fatalf("set --include-infra: %v", err)
	}
	t.Cleanup(func() { _ = countCmd.Flags().Set("include-infra", "false") })

	out, err := captureStdoutExpectErr(t, func() error {
		return countCmd.RunE(countCmd, nil)
	})
	if err == nil {
		t.Fatalf("expected a non-nil error, got nil (stdout=%q)", out)
	}
	s := strings.TrimSpace(out)
	if s == "" {
		t.Fatalf("stdout empty on `bd count --include-infra --json` config-load error — must emit a JSON error object (beads-huz3), err=%v", err)
	}
	var obj map[string]any
	if jerr := json.Unmarshal([]byte(s), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on --json error: %v\nstdout:\n%s", jerr, s)
	}
	if _, ok := obj["error"]; !ok {
		t.Errorf("expected an \"error\" field in the --json stdout object, got: %s", s)
	}
}
