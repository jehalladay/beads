//go:build cgo

package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// beads-99zdz: the DIRECT `bd note` path previously appended to Notes via a
// client-side read-modify-write — it read issue.Notes at resolve time, combined
// in Go, then wrote the whole blob back through a SEPARATE store.UpdateIssue
// (the same lost-update anti-pattern beads-jscve removed for `bd update
// --append-notes`). Two concurrent `bd note` on the same issue both base on the
// same snapshot -> last-writer-wins -> one note silently lost (worst on a shared
// hub sql-server where proxied calls genuinely interleave). The fix routes both
// note paths through the atomic server-side seam jscve landed
// (issueStore.AppendNotes / UpdateSpec.AppendNotes -> a single CONCAT_WS), so no
// client-side snapshot exists to go stale.
//
// These teeth drive the REAL noteCmd.RunE in-process (the a8d14 save/restore-
// globals pattern) against a store that FAULTS the OLD seam (UpdateIssue) while
// letting the NEW seam (AppendNotes) delegate to the real store. On the fixed
// code the note is written via AppendNotes (RunE succeeds, note present); on the
// pre-fix code that calls UpdateIssue the injected fault fires (RunE errors, note
// absent) — so a revert to the client-side RMW FAILS these tests. A happy-path
// test also proves a `bd note` append preserves the pre-existing notes and is
// newline-separated through the cmd path (the atomic seam's contract).
//
// MUTATION-VERIFY: revert note.go's block to the pre-fix shape (combined :=
// issue.Notes; combined += noteText; issueStore.UpdateIssue(...,
// {"notes": combined})) and TestNoteAppendUsesAtomicSeam_99zdz FAILS — the
// injected UpdateIssue fault fires, RunE returns an error, and the note is never
// written.

// faultNoteUpdateStore embeds the real DoltStorage and makes UpdateIssue (the OLD
// note seam) fail, while AppendNotes (the NEW seam) delegates to the real store.
// This discriminates the fix from the pre-fix RMW: the fixed note path calls
// AppendNotes and succeeds; a reverted note path calls UpdateIssue and hits the
// injected failure.
type faultNoteUpdateStore struct {
	storage.DoltStorage
	failUpdate bool
}

var errInjectedNoteUpdateFailure = errors.New("injected UpdateIssue failure (99zdz test)")

func (f *faultNoteUpdateStore) UpdateIssue(ctx context.Context, id string, updates map[string]interface{}, actor string) error {
	if f.failUpdate {
		return errInjectedNoteUpdateFailure
	}
	return f.DoltStorage.UpdateIssue(ctx, id, updates, actor)
}

// runNoteWithStore sets up globals to point at the given store, runs
// noteCmd.RunE for the id + text, and returns the RunE error. Globals are
// restored after the run so state does not bleed.
func runNoteWithStore(t *testing.T, s storage.DoltStorage, id, text string) error {
	t.Helper()

	origStore := store
	origRootCtx := rootCtx
	origJSONOutput := jsonOutput
	origReadonly := readonlyMode
	origActor := actor
	t.Cleanup(func() {
		store = origStore
		rootCtx = origRootCtx
		jsonOutput = origJSONOutput
		readonlyMode = origReadonly
		actor = origActor
	})

	store = s
	rootCtx = context.Background()
	jsonOutput = false
	readonlyMode = false
	actor = "test-actor"

	var runErr error
	_ = captureStdout(t, func() error {
		runErr = noteCmd.RunE(noteCmd, []string{id, text})
		return nil
	})
	return runErr
}

// TestNoteAppendUsesAtomicSeam_99zdz proves the fixed `bd note` writes through
// AppendNotes, not UpdateIssue: with UpdateIssue faulted, the fixed path still
// succeeds and the note lands; a reverted RMW path errors and drops the note.
func TestNoteAppendUsesAtomicSeam_99zdz(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	if err := real.CreateIssue(ctx, &types.Issue{
		ID: "test-note", Title: "note target", Status: types.StatusOpen,
		Priority: 2, IssueType: types.TypeTask, Notes: "seed",
	}, "test"); err != nil {
		t.Fatalf("create target: %v", err)
	}

	fault := &faultNoteUpdateStore{DoltStorage: real, failUpdate: true}
	if err := runNoteWithStore(t, fault, "test-note", "APPEND-MARKER"); err != nil {
		t.Fatalf("bd note through the atomic AppendNotes seam should succeed even when the "+
			"old UpdateIssue seam is faulted; got err=%v (pre-fix RMW path hits the fault)", err)
	}

	got, err := real.GetIssue(ctx, "test-note")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if !strings.Contains(got.Notes, "APPEND-MARKER") {
		t.Fatalf("REGRESSION (99zdz): note not written through the atomic AppendNotes seam; "+
			"a pre-fix client-side UpdateIssue RMW would have hit the injected fault and dropped it; notes=%q", got.Notes)
	}
	if !strings.Contains(got.Notes, "seed") {
		t.Errorf("pre-existing notes clobbered; notes=%q", got.Notes)
	}
}

// TestNoteAppendPreservesAndSeparates_99zdz is the happy-path contract: a `bd
// note` append onto existing notes preserves the old text and newline-separates
// the new note (the CONCAT_WS + NULLIF behavior, driven through the cmd path).
func TestNoteAppendPreservesAndSeparates_99zdz(t *testing.T) {
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	if err := real.CreateIssue(ctx, &types.Issue{
		ID: "test-note2", Title: "note target 2", Status: types.StatusOpen,
		Priority: 2, IssueType: types.TypeTask, Notes: "first line",
	}, "test"); err != nil {
		t.Fatalf("create target: %v", err)
	}

	if err := runNoteWithStore(t, real, "test-note2", "second line"); err != nil {
		t.Fatalf("bd note: %v", err)
	}
	got, err := real.GetIssue(ctx, "test-note2")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.Notes != "first line\nsecond line" {
		t.Fatalf("expected newline-separated append preserving existing notes; got %q", got.Notes)
	}
}
