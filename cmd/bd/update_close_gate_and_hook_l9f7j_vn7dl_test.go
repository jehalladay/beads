//go:build cgo

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-l9f7j + beads-vn7dl (CLOSE-PARITY matrix, single-update close leg).
//
// `bd update --status closed` reaches the SAME terminal closed state as
// `bd close` (the 6qo8t comment says so verbatim) and the codebase has
// repeatedly driven the two to parity (close_reason default, n4sn audit,
// zgku/2hkd/b0tw/a8a1b close-integrity guards, molecule-autoclose). Two more
// close side-effects were still divergent on the DIRECT update path:
//
//   l9f7j — gate-satisfaction: `bd close` (close.go:178) and `bd batch`
//           (beads-zpq1f) REJECT closing an issue whose machine-checkable gate
//           (timer / gh:pr* / gh:run*) is unsatisfied; `bd update --status
//           closed` silently bypassed it (both direct + proxied).
//   vn7dl — on_close hook: `bd close` (both modes) and the PROXIED update twin
//           fire on_close on a real open->closed transition; the DIRECT update
//           path fired only on_update (HookFiringStore.UpdateIssue), so on_close
//           automation silently did not run in embedded mode.
//
// Both driven END-TO-END through the ACTUAL `bd update` subprocess — a tx-helper
// would false-green by skipping the CLI-layer guard/hook plumbing entirely (the
// batch-parity family lesson). MUTATION-VERIFIED: remove the checkGateSatisfaction
// call (l9f7j) or the getHookRunner().RunSync(EventClose,...) call (vn7dl) added
// to update.go and the corresponding sub-test goes RED.

func gateShowsClosed(t *testing.T, bd, dir, id string) bool {
	t.Helper()
	show := exec.Command(bd, "show", id, "--json")
	show.Dir = dir
	show.Env = bdEnv(dir)
	out, err := show.Output()
	if err != nil {
		t.Fatalf("bd show %s failed: %v\n%s", id, err, out)
	}
	return strings.Contains(string(out), `"status": "closed"`) ||
		strings.Contains(string(out), `"status":"closed"`)
}

// beads-l9f7j: `bd update --status closed` on an unexpired timer gate must be
// REFUSED, at parity with `bd close`.
func TestUpdateStatusClosedGateSatisfaction_l9f7j(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	makeUnexpiredGate := func(t *testing.T, dir string) string {
		t.Helper()
		target := bdCreate(t, bd, dir, "Gate target for l9f7j", "--type", "task")
		mkGate := exec.Command(bd, "gate", "create", "--json", "--type", "timer", "--blocks", target.ID, "--timeout", "24h")
		mkGate.Dir = dir
		mkGate.Env = bdEnv(dir)
		out, err := mkGate.Output()
		if err != nil {
			t.Fatalf("gate create failed: %v\n%s", err, out)
		}
		gateID := parseIssueJSON(t, out).ID
		if gateID == "" {
			t.Fatalf("could not resolve gate id from: %s", out)
		}
		return gateID
	}

	// CONTROL: `bd close` refuses the unexpired gate (authoritative behavior).
	t.Run("close_refuses_unexpired_gate", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "lc")
		gateID := makeUnexpiredGate(t, dir)

		c := exec.Command(bd, "close", gateID)
		c.Dir = dir
		c.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, c)
		combined := stdout.String() + stderr.String()
		if err == nil {
			t.Fatalf("expected `bd close` to FAIL for unexpired gate, got rc=0:\n%s", combined)
		}
		if !strings.Contains(combined, "gate condition not satisfied") {
			t.Errorf("expected 'gate condition not satisfied' from bd close, got:\n%s", combined)
		}
		if gateShowsClosed(t, bd, dir, gateID) {
			t.Errorf("bd close of an unexpired gate should leave it OPEN, but it is closed")
		}
	})

	// FIX: `bd update --status closed` of the same unexpired gate must ALSO be
	// refused (l9f7j). RED before the fix: update closes it (rc=0, gate closed).
	t.Run("update_status_closed_refuses_unexpired_gate", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "lu")
		gateID := makeUnexpiredGate(t, dir)

		u := exec.Command(bd, "update", gateID, "--status", "closed")
		u.Dir = dir
		u.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, u)
		combined := stdout.String() + stderr.String()
		if err == nil {
			t.Fatalf("REGRESSION (beads-l9f7j): expected `bd update --status closed` to FAIL for an unexpired gate (parity with bd close), got rc=0:\n%s", combined)
		}
		if !strings.Contains(combined, "gate condition not satisfied") {
			t.Errorf("expected 'gate condition not satisfied' from bd update --status closed, got:\n%s", combined)
		}
		if gateShowsClosed(t, bd, dir, gateID) {
			t.Errorf("REGRESSION (beads-l9f7j): bd update --status closed of an unexpired gate should leave it OPEN, but it is closed")
		}
	})

	// --force override: `bd update --status closed --force` skips the gate guard,
	// matching `bd close --force`.
	t.Run("update_force_overrides_gate", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "lf")
		gateID := makeUnexpiredGate(t, dir)

		u := exec.Command(bd, "update", gateID, "--status", "closed", "--force")
		u.Dir = dir
		u.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, u)
		combined := stdout.String() + stderr.String()
		if err != nil {
			t.Fatalf("expected `bd update --status closed --force` to CLOSE the gate, got error: %v\n%s", err, combined)
		}
		if !gateShowsClosed(t, bd, dir, gateID) {
			t.Errorf("bd update --status closed --force should close the unexpired gate, but it is still open:\n%s", combined)
		}
	})
}

// beads-vn7dl: `bd update --status closed` (DIRECT) must fire the on_close hook
// on a real open->closed transition, at parity with `bd close` and the proxied
// update twin.
func TestUpdateStatusClosedFiresOnCloseHook_vn7dl(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// installHooks writes on_close + on_update marker scripts into the workspace
	// hooks dir; returns the two marker paths.
	installHooks := func(t *testing.T, beadsDir string) (onClose, onUpdate string) {
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

	fired := func(marker string) bool {
		b, err := os.ReadFile(marker)
		return err == nil && len(strings.TrimSpace(string(b))) > 0
	}

	// CONTROL: `bd close` fires on_close (authoritative behavior the update path
	// must mirror).
	t.Run("close_fires_on_close", func(t *testing.T) {
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "vc")
		onClose, _ := installHooks(t, beadsDir)
		iss := bdCreate(t, bd, dir, "close-hook control", "--type", "task")

		c := exec.Command(bd, "close", iss.ID)
		c.Dir = dir
		c.Env = bdEnv(dir)
		if _, _, err := runCommandBuffers(t, c); err != nil {
			t.Fatalf("bd close failed: %v", err)
		}
		if !fired(onClose) {
			t.Errorf("bd close did not fire on_close (control) — harness broken")
		}
	})

	// FIX: `bd update --status closed` on an open issue fires on_close (vn7dl).
	// RED before the fix: only on_update fired.
	t.Run("update_status_closed_fires_on_close", func(t *testing.T) {
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "vu")
		onClose, onUpdate := installHooks(t, beadsDir)
		iss := bdCreate(t, bd, dir, "close-via-update", "--type", "task")

		u := exec.Command(bd, "update", iss.ID, "--status", "closed")
		u.Dir = dir
		u.Env = bdEnv(dir)
		if _, _, err := runCommandBuffers(t, u); err != nil {
			t.Fatalf("bd update --status closed failed: %v", err)
		}
		if !fired(onClose) {
			t.Errorf("REGRESSION (beads-vn7dl): `bd update --status closed` on an open issue did NOT fire the on_close hook (fires it via bd close and via the proxied update twin) — on_close automation silently skipped in embedded mode")
		}
		if !fired(onUpdate) {
			t.Errorf("`bd update --status closed` should still fire on_update (unchanged decorator behavior)")
		}
	})

	// NEGATIVE: re-closing an already-closed issue is a no-op transition and must
	// NOT fire on_close (guards the transition condition, not blanket firing).
	t.Run("update_already_closed_does_not_fire_on_close", func(t *testing.T) {
		dir, beadsDir, _ := bdInit(t, bd, "--prefix", "vn")
		iss := bdCreate(t, bd, dir, "already-closed", "--type", "task")

		// Close first (before installing hooks, so the marker is clean).
		pre := exec.Command(bd, "close", iss.ID)
		pre.Dir = dir
		pre.Env = bdEnv(dir)
		if _, _, err := runCommandBuffers(t, pre); err != nil {
			t.Fatalf("pre-close failed: %v", err)
		}
		onClose, _ := installHooks(t, beadsDir)

		// Now `update --status closed` on an ALREADY-closed issue: no transition.
		u := exec.Command(bd, "update", iss.ID, "--status", "closed")
		u.Dir = dir
		u.Env = bdEnv(dir)
		_, _, _ = runCommandBuffers(t, u)
		if fired(onClose) {
			t.Errorf("REGRESSION (beads-vn7dl): re-closing an already-closed issue via update fired on_close — it must fire only on a genuine open->closed transition")
		}
	})
}

// beads-l9f7j PROXIED leg: the proxied `bd update --status closed` path
// (checkProxiedUpdateCloseGuards) must ALSO refuse closing an unexpired timer
// gate, at parity with the proxied close path (close_proxied_server.go:276) and
// the direct update guard above. Runs end-to-end through the real proxied-server
// subprocess. MUTATION-VERIFIED: removing the checkGateSatisfaction call added
// to checkProxiedUpdateCloseGuards lets the proxied update close the gate.
func TestProxiedUpdateStatusClosedGateSatisfaction_l9f7j(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "plg")

	// Unexpired timer gate blocking a fresh target.
	target := bdProxiedCreate(t, bd, p.dir, "gate target l9f7j proxied", "--type", "task")
	out, err := bdProxiedRun(t, bd, p.dir, "gate", "create", "--json", "--type", "timer", "--blocks", target.ID, "--timeout", "24h")
	if err != nil {
		t.Fatalf("proxied gate create failed: %v\n%s", err, out)
	}
	gateID := parseIssueJSON(t, out).ID
	if gateID == "" {
		t.Fatalf("could not resolve proxied gate id from: %s", out)
	}

	// FIX: proxied `bd update --status closed` of the unexpired gate must FAIL.
	combined := bdProxiedUpdateFail(t, bd, p.dir, gateID, "--status", "closed")
	if !strings.Contains(combined, "gate condition not satisfied") {
		t.Errorf("expected 'gate condition not satisfied' from proxied update --status closed, got:\n%s", combined)
	}
	if bdProxiedShow(t, bd, p.dir, gateID).Status == types.StatusClosed {
		t.Errorf("REGRESSION (beads-l9f7j proxied): proxied `bd update --status closed` of an unexpired gate should leave it OPEN, but it is closed")
	}

	// --force override closes it (parity with proxied bd close --force).
	if out, err := bdProxiedRun(t, bd, p.dir, "update", gateID, "--status", "closed", "--force"); err != nil {
		t.Fatalf("proxied update --status closed --force should close the gate, got: %v\n%s", err, out)
	}
	if bdProxiedShow(t, bd, p.dir, gateID).Status != types.StatusClosed {
		t.Errorf("proxied update --status closed --force should close the unexpired gate, but it is still open")
	}
}
