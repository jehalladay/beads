//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestShowCloseReasonSpecIDSanitize_1xsbc is the sanitize teeth for the
// CloseReason + SpecID display sinks in `bd show` (beads-1xsbc, i8dsb-sibling
// untrusted-import terminal-sink class). Both fields are JSONL-importable and
// only weakly validated (ValidateCloseReason checks empty/length>=20, SpecID
// only length<=1024) — neither rejects control characters — so a hostile import
// injects OSC/CSI escapes rendered raw at show_format.go:132 (Close reason) and
// :142 (Spec).
//
// End-to-end through the real print path: import a CLOSED issue whose
// close_reason AND spec_id carry escapes, run `bd show`, assert stdout has NO
// raw ESC/BEL while the visible text + framing survive.
func TestShowCloseReasonSpecIDSanitize_1xsbc(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd)

	const csi = "\x1b[31m"
	const osc52 = "\x1b]52;c;cHduZWQ=\x07"
	// close_reason must be >=20 chars to pass ValidateCloseReason on any path;
	// the escapes are embedded mid-string.
	evilReason := "done the work fully" + csi + osc52 + " and verified"
	evilSpec := "SPEC-42" + osc52 + "-tail"

	issue := map[string]any{
		"id":           "probe-1",
		"title":        "clean title",
		"status":       "closed",
		"close_reason": evilReason,
		"spec_id":      evilSpec,
		"priority":     2,
		"issue_type":   "task",
		"created_at":   "2026-07-20T00:00:00Z",
		"updated_at":   "2026-07-20T00:00:00Z",
	}
	line, err := json.Marshal(issue)
	if err != nil {
		t.Fatalf("marshal seed issue: %v", err)
	}
	jsonlPath := filepath.Join(beadsDir, "inj.jsonl")
	if err := os.WriteFile(jsonlPath, append(line, '\n'), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}
	if out, err := bdRunWithFlockRetry(t, bd, dir, "import", jsonlPath); err != nil {
		t.Fatalf("bd import failed: %v\n%s", err, out)
	}

	cmd := exec.Command(bd, "show", "probe-1")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bd show failed: %v\n%s", err, out)
	}
	got := string(out)

	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("bd show leaked a raw ESC (0x1b) — CloseReason/SpecID not sanitized:\n%q", got)
	}
	if strings.ContainsRune(got, '\x07') {
		t.Errorf("bd show leaked a raw BEL (0x07) — CloseReason/SpecID not sanitized:\n%q", got)
	}
	for _, want := range []string{"Close reason:", "done the work fully", "Spec:", "SPEC-42", "-tail"} {
		if !strings.Contains(got, want) {
			t.Errorf("bd show dropped expected visible text %q; output:\n%s", want, got)
		}
	}
}
