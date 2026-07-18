//go:build cgo

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeFakeEditor writes a shell script that non-interactively overwrites the
// file it is passed ($1) with newContent, and returns its path. Used to drive
// `bd edit` (which shells out to $EDITOR) deterministically in tests.
func writeFakeEditor(t *testing.T, dir, newContent string) string {
	t.Helper()
	script := filepath.Join(dir, "fake-editor.sh")
	body := "#!/bin/sh\ncat > \"$1\" <<'BD_EDIT_EOF'\n" + newContent + "\nBD_EDIT_EOF\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake editor: %v", err)
	}
	return script
}

// bdEditProxied runs `bd edit` under proxied-server mode with EDITOR pointed at
// a fake editor that writes newContent. Returns combined output + error.
func bdEditProxied(t *testing.T, bd, dir, newContent string, args ...string) (string, error) {
	t.Helper()
	editor := writeFakeEditor(t, dir, newContent)
	cmd := exec.Command(bd, append([]string{"edit"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(bdProxiedEnv(dir), "EDITOR="+editor, "VISUAL=")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestProxiedServerEdit covers beads-8fm2: `bd edit` must work for
// hub-connected (proxied-server) crew — previously it hit nil-store
// "storage is nil" because edit.go used the direct `store` with no proxied
// routing.
func TestProxiedServerEdit(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("edit_description_persists", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "edt1")
		iss := bdProxiedCreate(t, bd, p.dir, "Edit Desc", "--type", "task")
		out, err := bdEditProxied(t, bd, p.dir, "brand new description", iss.ID)
		if err != nil {
			t.Fatalf("proxied bd edit failed: %v\n%s", err, out)
		}
		if strings.Contains(out, "storage is nil") {
			t.Fatalf("proxied edit hit nil-store path (beads-8fm2 regression): %s", out)
		}
		if !strings.Contains(out, "Updated description") {
			t.Errorf("expected 'Updated description', got: %s", out)
		}
		// Verify persistence via a proxied show.
		show := bdProxiedRunOrFail(t, bd, p.dir, "show", iss.ID)
		if !strings.Contains(show, "brand new description") {
			t.Errorf("edited description did not persist: %s", show)
		}
	})

	t.Run("edit_title_persists", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "edt2")
		iss := bdProxiedCreate(t, bd, p.dir, "Old Title", "--type", "task")
		out, err := bdEditProxied(t, bd, p.dir, "New Shiny Title", iss.ID, "--title")
		if err != nil {
			t.Fatalf("proxied bd edit --title failed: %v\n%s", err, out)
		}
		if !strings.Contains(out, "Updated title") {
			t.Errorf("expected 'Updated title', got: %s", out)
		}
		show := bdProxiedRunOrFail(t, bd, p.dir, "show", iss.ID)
		if !strings.Contains(show, "New Shiny Title") {
			t.Errorf("edited title did not persist: %s", show)
		}
	})

	t.Run("edit_notes_persists", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "edt3")
		iss := bdProxiedCreate(t, bd, p.dir, "Edit Notes", "--type", "task")
		out, err := bdEditProxied(t, bd, p.dir, "some proxied notes", iss.ID, "--notes")
		if err != nil {
			t.Fatalf("proxied bd edit --notes failed: %v\n%s", err, out)
		}
		if !strings.Contains(out, "Updated notes") {
			t.Errorf("expected 'Updated notes', got: %s", out)
		}
		show := bdProxiedRunOrFail(t, bd, p.dir, "show", iss.ID)
		if !strings.Contains(show, "some proxied notes") {
			t.Errorf("edited notes did not persist: %s", show)
		}
	})

	t.Run("edit_title_empty_rejected", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "edt4")
		iss := bdProxiedCreate(t, bd, p.dir, "Keep Me", "--type", "task")
		out, err := bdEditProxied(t, bd, p.dir, "", iss.ID, "--title")
		if err == nil {
			t.Fatalf("expected empty-title edit to fail, got success:\n%s", out)
		}
		if !strings.Contains(out, "title cannot be empty") {
			t.Errorf("expected 'title cannot be empty', got: %s", out)
		}
	})

	t.Run("edit_no_change_reports_no_change", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "edt5")
		iss := bdProxiedCreate(t, bd, p.dir, "Same Title", "--type", "task", "-d", "unchanged body")
		out, err := bdEditProxied(t, bd, p.dir, "unchanged body", iss.ID)
		if err != nil {
			t.Fatalf("proxied bd edit (no-op) failed: %v\n%s", err, out)
		}
		if !strings.Contains(out, "No changes made") {
			t.Errorf("expected 'No changes made', got: %s", out)
		}
	})
}

// bdProxiedRunOrFail runs bd under proxied env and fails the test on error.
func bdProxiedRunOrFail(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	out, err := bdProxiedRun(t, bd, dir, args...)
	if err != nil {
		t.Fatalf("bd %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
