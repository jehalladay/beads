//go:build cgo

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// beads-usumn (on_close-hook CLOSE-PARITY family: vn7dl update leg, 7o4av batch
// leg, this = duplicate/supersede leg).
//
// `bd duplicate` and `bd supersede` close the source issue via
// store.LinkAndClose. HookFiringStore.LinkAndClose (hook_decorator.go:180) fires
// ONLY on_update ("behavior-preserving" — the pre-atomic path closed via
// UpdateIssue) — so a close-transition through duplicate/supersede did NOT fire
// on_close, unlike `bd close` (HookFiringStore.CloseIssue:149), `bd update
// --status closed` (vn7dl), and `bd batch update status=closed` (7o4av). The
// PROXIED twin (runLinkAndCloseProxied) closed via uw.IssueUseCase().CloseIssue
// and fired ZERO hooks (neither on_update nor on_close). on_close automation
// (notifications, downstream sync, GC/archival) silently did not run when an
// issue was closed by being marked duplicate/superseded.
//
// FIX: direct legs (duplicate.go) fire getHookRunner().RunSync(EventClose,
// after) on the open->closed transition, after the existing autoClose cascade;
// the proxied twin reuses fireProxiedUpdateHooks (on_update always + on_close on
// transition) with a fresh post-commit after-image — exact direct parity.
//
// Driven END-TO-END through the ACTUAL `bd duplicate`/`bd supersede` subprocess
// (a tx-helper would false-green by skipping the CLI-layer hook plumbing — the
// batch-parity family lesson). MUTATION-VERIFIED: remove the
// getHookRunner().RunSync(EventClose,...) call in duplicate.go and the fix
// sub-tests go RED.

// usumnInstallHooks writes on_close + on_update marker scripts into the
// workspace hooks dir; returns the two marker paths. (Mirrors the vn7dl harness.)
func usumnInstallHooks(t *testing.T, beadsDir string) (onClose, onUpdate string) {
	t.Helper()
	hooksDir := filepath.Join(beadsDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		t.Fatalf("mkdir hooks: %v", err)
	}
	onClose = filepath.Join(beadsDir, "on_close_marker.txt")
	onUpdate = filepath.Join(beadsDir, "on_update_marker.txt")
	writeHook := func(name, marker string) {
		script := "#!/bin/sh\necho fired >> " + marker + "\n"
		if err := os.WriteFile(filepath.Join(hooksDir, name), []byte(script), 0755); err != nil {
			t.Fatalf("write %s hook: %v", name, err)
		}
	}
	writeHook("on_close", onClose)
	writeHook("on_update", onUpdate)
	return onClose, onUpdate
}

func usumnFired(marker string) bool {
	b, err := os.ReadFile(marker)
	return err == nil && len(strings.TrimSpace(string(b))) > 0
}

func TestDuplicateSupersedeFireOnCloseHook_usumn(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// CONTROL: `bd close` fires on_close (authoritative behavior the duplicate/
	// supersede legs must mirror — proves the harness detects a real fire).
	t.Run("close_fires_on_close_control", func(t *testing.T) {
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "uc")
		onClose, _ := usumnInstallHooks(t, beadsDir)
		iss := bdCreate(t, bd, dir, "close control", "--type", "task")

		c := exec.Command(bd, "close", iss.ID)
		c.Dir = dir
		c.Env = bdEnv(dir)
		if _, _, err := runCommandBuffers(t, c); err != nil {
			t.Fatalf("bd close failed: %v", err)
		}
		if !usumnFired(onClose) {
			t.Errorf("bd close did not fire on_close (control) — harness broken")
		}
	})

	// FIX (duplicate): `bd duplicate <src> --of <canonical>` closes the source
	// and must fire on_close on the open->closed transition (usumn). RED before
	// the fix: only on_update fired.
	t.Run("duplicate_fires_on_close", func(t *testing.T) {
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "ud")
		onClose, onUpdate := usumnInstallHooks(t, beadsDir)
		canonical := bdCreate(t, bd, dir, "canonical", "--type", "task")
		src := bdCreate(t, bd, dir, "dup source", "--type", "task")

		d := exec.Command(bd, "duplicate", src.ID, "--of", canonical.ID)
		d.Dir = dir
		d.Env = bdEnv(dir)
		if _, _, err := runCommandBuffers(t, d); err != nil {
			t.Fatalf("bd duplicate failed: %v", err)
		}
		if !usumnFired(onClose) {
			t.Errorf("REGRESSION (beads-usumn): `bd duplicate` closed the source but did NOT fire the on_close hook (bd close/update/batch all do) — on_close automation silently skipped")
		}
		if !usumnFired(onUpdate) {
			t.Errorf("`bd duplicate` should still fire on_update (LinkAndClose decorator behavior)")
		}
	})

	// FIX (supersede): `bd supersede <old> --with <new>` closes the old issue and
	// must fire on_close on the open->closed transition (usumn).
	t.Run("supersede_fires_on_close", func(t *testing.T) {
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "us")
		onClose, onUpdate := usumnInstallHooks(t, beadsDir)
		replacement := bdCreate(t, bd, dir, "replacement", "--type", "task")
		old := bdCreate(t, bd, dir, "old", "--type", "task")

		s := exec.Command(bd, "supersede", old.ID, "--with", replacement.ID)
		s.Dir = dir
		s.Env = bdEnv(dir)
		if _, _, err := runCommandBuffers(t, s); err != nil {
			t.Fatalf("bd supersede failed: %v", err)
		}
		if !usumnFired(onClose) {
			t.Errorf("REGRESSION (beads-usumn): `bd supersede` closed the old issue but did NOT fire the on_close hook — on_close automation silently skipped")
		}
		if !usumnFired(onUpdate) {
			t.Errorf("`bd supersede` should still fire on_update (LinkAndClose decorator behavior)")
		}
	})
}
