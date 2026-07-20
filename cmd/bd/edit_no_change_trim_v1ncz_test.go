//go:build cgo

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEditNoChangeTrim_v1ncz covers beads-v1ncz: `bd edit` compared the TRIMMED
// edited content against the RAW (untrimmed) stored field, so a no-op editor
// save on a field that carries leading/trailing whitespace (e.g. a description
// set from a file ending in "\n") looked like a change → a spurious trim-only
// write + false audit event, and "No changes made" was never printed.
//
// Repro: seed a description WITH a trailing newline (via `bd update
// --description-file`, which does not trim), then run `bd edit` with a fake
// editor that saves the file UNCHANGED (writes back exactly what edit seeded).
// The fix (compare TrimSpace(currentValue)) must report no change.
func TestEditNoChangeTrim_v1ncz(t *testing.T) {
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "et")

	iss := bdCreate(t, bd, dir, "edit trim target", "--type", "task", "-d", "seed")

	// Seed a description that carries a trailing newline (untrimmed) via a file.
	descFile := filepath.Join(dir, "seed-desc.txt")
	if err := os.WriteFile(descFile, []byte("body with trailing ws\n\n"), 0o644); err != nil {
		t.Fatalf("write seed desc file: %v", err)
	}
	upd := exec.Command(bd, "update", iss.ID, "--description-file", descFile)
	upd.Dir = dir
	upd.Env = bdEnv(dir)
	if out, err := upd.CombinedOutput(); err != nil {
		t.Fatalf("seed update --description-file failed: %v\n%s", err, out)
	}

	// Capture updated_at before the no-op edit; a spurious write would bump it.
	beforeUpdatedAt := jsonFieldOfShow(t, bd, dir, iss.ID, "updated_at")

	// A "save with no edit" editor: leave the seeded temp file exactly as
	// bd wrote it (bd seeds the temp file with the RAW currentValue). `true`
	// touches nothing → the file keeps its raw (whitespace-carrying) content.
	editor := filepath.Join(dir, "noop-editor.sh")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write noop editor: %v", err)
	}

	cmd := exec.Command(bd, "edit", iss.ID)
	cmd.Dir = dir
	cmd.Env = append(bdEnv(dir), "EDITOR="+editor, "VISUAL=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bd edit (no-op) failed: %v\n%s", err, out)
	}

	// The fix: a no-op save on a whitespace-carrying field reports no change.
	if !strings.Contains(string(out), "No changes made") {
		t.Errorf("no-op edit on a whitespace-carrying description did NOT report 'No changes made' (beads-v1ncz) — it took the changed branch:\n%s", out)
	}

	// And it must NOT have written (updated_at unchanged). A spurious trim-write
	// bumps updated_at even though the user changed nothing.
	afterUpdatedAt := jsonFieldOfShow(t, bd, dir, iss.ID, "updated_at")
	if afterUpdatedAt != beforeUpdatedAt {
		t.Errorf("no-op edit bumped updated_at (spurious write, beads-v1ncz): before=%q after=%q", beforeUpdatedAt, afterUpdatedAt)
	}
}
