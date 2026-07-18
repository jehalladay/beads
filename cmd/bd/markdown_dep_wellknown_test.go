//go:build cgo

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestCreateIssuesFromMarkdown_UnknownDepTypeRejected is the teeth for
// beads-ed7s: the markdown-import dependency path validated an imported dep's
// type with only IsValid() (non-empty/<=32), so a typo like "blockd:<id>"
// imported a non-gating custom edge with no error — the same false-success +
// silent-gate-drift as beads-qfka (dep add) and beads-9v0d (bd link), which
// both missed this import path. The import must fail loud on an unknown
// dep-type (naming it), while a well-known type still imports clean.
func TestCreateIssuesFromMarkdown_UnknownDepTypeRejected(t *testing.T) {
	setup := func(t *testing.T, md string) string {
		t.Helper()
		tmpDir := t.TempDir()
		beadsDir := filepath.Join(tmpDir, ".beads")
		if err := os.Mkdir(beadsDir, 0755); err != nil {
			t.Fatal(err)
		}
		dbPath := filepath.Join(beadsDir, "dolt")

		oldStore, oldActor, oldCtx := store, actor, rootCtx
		store = newTestStoreIsolatedDB(t, dbPath, "ed7s")
		actor = "ed7s"
		rootCtx = context.Background()
		t.Cleanup(func() { store, actor, rootCtx = oldStore, oldActor, oldCtx })

		mdPath := filepath.Join(tmpDir, "issues.md")
		if err := os.WriteFile(mdPath, []byte(md), 0644); err != nil {
			t.Fatal(err)
		}
		return mdPath
	}

	t.Run("unknown dep-type fails loud and creates nothing", func(t *testing.T) {
		md := "## Issue With Bad Dep Type\n\nBody.\n\n### Dependencies\n\nblockd:ed7s-10\n"
		mdPath := setup(t, md)

		var err error
		out := captureStderr(t, func() {
			err = createIssuesFromMarkdown(nil, mdPath)
		})
		if err == nil {
			t.Fatal("createIssuesFromMarkdown with an unknown dep-type = nil error, want a fail-loud rejection")
		}
		if !strings.Contains(out, "blockd") {
			t.Errorf("error output %q should name the offending dep-type 'blockd'", out)
		}
		// Fail-loud is pre-persist: no issue should have been created.
		n, err := store.CountIssues(rootCtx, "", types.IssueFilter{})
		if err != nil {
			t.Fatalf("CountIssues failed: %v", err)
		}
		if n != 0 {
			t.Errorf("expected no issue created on unknown dep-type, got %d", n)
		}
	})

	t.Run("well-known dep-type imports clean", func(t *testing.T) {
		// Target must exist for the edge to persist; create it first via a
		// self-contained two-issue markdown file (the second depends on the first
		// by a well-known type). "discovered-from" is the non-default well-known
		// type most likely to regress if the gate is wrong.
		md := "## Target Issue\n\nBody.\n\n" +
			"## Dependent Issue\n\nBody.\n\n### Dependencies\n\nblocks:ed7s-target\n"
		mdPath := setup(t, md)

		var err error
		_ = captureStderr(t, func() {
			err = createIssuesFromMarkdown(nil, mdPath)
		})
		if err != nil {
			t.Fatalf("createIssuesFromMarkdown with a well-known dep-type returned error (want clean import): %v", err)
		}
	})
}
