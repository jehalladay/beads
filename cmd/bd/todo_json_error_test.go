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

// beads-j9ir: bd todo is a documented --json command — `bd todo add` marshals
// the created issue (todo.go) and `bd todo`/`bd todo list` marshals the list.
// But two store-error returns used a plain HandleError before the jsonOutput
// block: todo.go:89 (CreateIssue in `todo add`) and todo.go:143 (SearchIssues in
// the list core). Under `--json` that left stdout empty + stderr text,
// unparseable by a --json consumer. The fix routes both through
// HandleErrorRespectJSON (0wp9/21xi --json-error-contract class).
//
// The store errors aren't forceable from a subprocess, so the teeth inject a
// store double failing CreateIssue/SearchIssues into the package `store` global
// and invoke the RunE directly (mirrors beads-21xi lint_json_error_test).
type failTodoStore struct {
	storage.DoltStorage
}

func (f failTodoStore) CreateIssue(context.Context, *types.Issue, string) error {
	return errors.New("injected create failure")
}

func (f failTodoStore) SearchIssues(context.Context, string, types.IssueFilter) ([]*types.Issue, error) {
	return nil, errors.New("injected search failure")
}

func assertJSONErrorObject(t *testing.T, out string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a non-nil error, got nil (stdout=%q)", out)
	}
	s := strings.TrimSpace(out)
	if s == "" {
		t.Fatalf("stdout empty on `bd todo --json` store error — must emit a JSON error object (beads-j9ir), err=%v", err)
	}
	var obj map[string]any
	if jerr := json.Unmarshal([]byte(s), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on --json error: %v\nstdout:\n%s", jerr, s)
	}
	if _, ok := obj["error"]; !ok {
		t.Errorf("expected an \"error\" field in the --json stdout object, got: %s", s)
	}
}

func TestTodoJSONErrorContract_j9ir(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	prevStore, prevCtx, prevJSON := store, rootCtx, jsonOutput
	rootCtx = ctx
	jsonOutput = true
	store = failTodoStore{DoltStorage: real}
	t.Cleanup(func() { store, rootCtx, jsonOutput = prevStore, prevCtx, prevJSON })

	t.Run("todo_add_create_error", func(t *testing.T) {
		out, err := captureStdoutExpectErr(t, func() error {
			return addTodoCmd.RunE(addTodoCmd, []string{"a todo"})
		})
		assertJSONErrorObject(t, out, err)
	})

	t.Run("todo_list_search_error", func(t *testing.T) {
		out, err := captureStdoutExpectErr(t, func() error {
			return runTodoListCore(listTodosCmd, nil)
		})
		assertJSONErrorObject(t, out, err)
	})
}
