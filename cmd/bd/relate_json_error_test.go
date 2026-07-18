//go:build cgo

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// beads-jy6w: bd relate/unrelate error paths must honor the --json error
// contract — a parseable JSON error object on stdout (HandleErrorRespectJSON),
// matching list(8lqh)/create/delete — not a raw fmt.Errorf that cobra prints as
// plain text to stderr (which breaks `bd relate X Y --json` consumers doing
// json.load on stdout).

// captureStdoutExpectErr runs fn (which is expected to return a non-nil error),
// capturing anything written to os.Stdout, and returns (stdout, err). Unlike
// the shared captureStdout helper it does NOT t.Errorf on a returned error —
// the error IS the thing under test.
func captureStdoutExpectErr(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	stdioMutex.Lock()
	defer stdioMutex.Unlock()

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	err := fn()
	w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	os.Stdout = oldStdout
	return buf.String(), err
}

func TestRelateJSONErrorContract_jy6w(t *testing.T) {
	tmpDir := t.TempDir()
	testDB := filepath.Join(tmpDir, ".beads", "beads.db")
	s := newTestStore(t, testDB)
	ctx := context.Background()

	issue := &types.Issue{
		ID:        "test-jy6w-1",
		Title:     "Issue 1",
		Status:    types.StatusOpen,
		Priority:  1,
		IssueType: types.TypeTask,
		CreatedAt: time.Now(),
	}
	if err := s.CreateIssue(ctx, issue, "test"); err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}

	// Point the package globals runRelate/runUnrelate read at the test store.
	prevStore, prevCtx, prevJSON := store, rootCtx, jsonOutput
	store = s
	rootCtx = ctx
	jsonOutput = true
	t.Cleanup(func() { store, rootCtx, jsonOutput = prevStore, prevCtx, prevJSON })

	assertJSONError := func(t *testing.T, label, stdout string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: expected a non-nil error, got nil (stdout=%q)", label, stdout)
		}
		out := strings.TrimSpace(stdout)
		if out == "" {
			t.Fatalf("%s: stdout empty on a --json error — must emit a JSON error object (beads-jy6w), err=%v", label, err)
		}
		var obj map[string]any
		if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
			t.Fatalf("%s: stdout is not a JSON object on --json error: %v\nstdout:\n%s", label, jerr, out)
		}
		if _, ok := obj["error"]; !ok {
			t.Errorf("%s: expected an \"error\" field in the --json stdout object, got: %s", label, out)
		}
	}

	// relate: self-relate is a deterministic user error reached after ID
	// resolution — it must emit a JSON error object under --json.
	t.Run("relate_self_json_error", func(t *testing.T) {
		out, err := captureStdoutExpectErr(t, func() error {
			return runRelate(relateCmd, []string{issue.ID, issue.ID})
		})
		assertJSONError(t, "relate self", out, err)
	})

	// relate: unresolvable ID → JSON error object, not plain-text stderr.
	t.Run("relate_unresolvable_json_error", func(t *testing.T) {
		out, err := captureStdoutExpectErr(t, func() error {
			return runRelate(relateCmd, []string{"no-such-issue-xyz", issue.ID})
		})
		assertJSONError(t, "relate unresolvable", out, err)
	})

	// unrelate: unresolvable ID → JSON error object.
	t.Run("unrelate_unresolvable_json_error", func(t *testing.T) {
		out, err := captureStdoutExpectErr(t, func() error {
			return runUnrelate(unrelateCmd, []string{"no-such-issue-xyz", issue.ID})
		})
		assertJSONError(t, "unrelate unresolvable", out, err)
	})
}
