//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// beads-3xp23: `bd human respond` / `bd human dismiss` printed only a plaintext
// "✔ Bead X ..." line via fmt.Printf with NO --json handling — so `bd human
// respond X --json` / `bd human dismiss X --json` emitted unparseable text with
// rc=0 and a --json consumer got non-JSON on stdout. The sibling human verbs
// (list=erw5, stats=vath) already honor --json. Additionally the "Warning:
// Issue X does not have 'human' label" stderr write was UNCONDITIONAL, leaking a
// raw non-JSON line into a --json consumer's captured stderr (the
// stderr-warn-under-json class: cxq3c/lster/mfmcf/9teyf).
//
// The fix: on success emit a stable {id,status,action,reason} envelope via
// outputJSON under --json, and guard the label warning behind !jsonOutput —
// across all four seams (direct + proxied × respond + dismiss). These are the
// DIRECT legs (the proxied legs share the same guard, exercised under
// BEADS_TEST_PROXIED_SERVER=1 by the integration harness).
//
// MUTATION-VERIFIED: reverting either the outputJSON block (back to bare
// fmt.Printf) turns the "stdout must be JSON" assertion RED; dropping the
// !jsonOutput guard on the label warning turns the "no raw stderr Warning under
// --json" assertion RED.
func TestHumanRespond_HonorsJSON_3xp23(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "hrj")

	// Seed a human-gate bead, then respond --json.
	hb := bdCreate(t, bd, dir, "needs a human", "--type", "task", "--labels", "human")
	rc := exec.Command(bd, "human", "respond", hb.ID, "-r", "here is the answer", "--json")
	rc.Dir = dir
	rc.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, rc)
	if err != nil {
		t.Fatalf("`bd human respond --json` failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	// stdout must parse as a JSON object carrying the closed state.
	assertHumanVerbJSON(t, stdout.String(), hb.ID, "closed", "responded")
}

func TestHumanDismiss_HonorsJSON_3xp23(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "hdj")

	hb := bdCreate(t, bd, dir, "needs a human", "--type", "task", "--labels", "human")
	dc := exec.Command(bd, "human", "dismiss", hb.ID, "--reason", "no longer applicable", "--json")
	dc.Dir = dir
	dc.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, dc)
	if err != nil {
		t.Fatalf("`bd human dismiss --json` failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	assertHumanVerbJSON(t, stdout.String(), hb.ID, "closed", "dismissed")
}

// TestHumanRespond_NoLabelWarningUnderJSON_3xp23 exercises the stderr-leak half:
// a bead WITHOUT the "human" label triggers the "does not have 'human' label"
// warning. Under --json that raw stderr line must be suppressed (it carries no
// data a script needs and would corrupt a --json consumer's captured stderr).
func TestHumanRespond_NoLabelWarningUnderJSON_3xp23(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "hrw")

	// A plain task (NO human label) → the label-warning branch fires.
	nb := bdCreate(t, bd, dir, "not a human bead", "--type", "task")
	rc := exec.Command(bd, "human", "respond", nb.ID, "-r", "answer", "--json")
	rc.Dir = dir
	rc.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, rc)
	if err != nil {
		t.Fatalf("`bd human respond --json` (no-label) failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "does not have 'human' label") {
		t.Errorf("beads-3xp23: `bd human respond --json` leaked the raw 'does not have human label' warning to stderr; it must be suppressed under --json\nstderr:\n%s", stderr.String())
	}
	// stdout still a valid JSON success envelope.
	assertHumanVerbJSON(t, stdout.String(), nb.ID, "closed", "responded")
}

func assertHumanVerbJSON(t *testing.T, stdout, wantID, wantStatus, wantAction string) {
	t.Helper()
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		t.Fatalf("beads-3xp23: expected a JSON object on stdout under --json, got empty")
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
		t.Fatalf("beads-3xp23: stdout under --json must be a parseable JSON object, got non-JSON (%v):\n%s", err, stdout)
	}
	if got, _ := m["id"].(string); got != wantID {
		t.Errorf("beads-3xp23: json.id = %q, want %q", got, wantID)
	}
	if got, _ := m["status"].(string); got != wantStatus {
		t.Errorf("beads-3xp23: json.status = %q, want %q", got, wantStatus)
	}
	if got, _ := m["action"].(string); got != wantAction {
		t.Errorf("beads-3xp23: json.action = %q, want %q", got, wantAction)
	}
}
