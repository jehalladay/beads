//go:build cgo

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestGateCreateTitleSanitize_mvb6a is the sanitize teeth for beads-mvb6a (7n9y
// sink-class HOLDOUT). `bd gate create --blocks <id>` prints a confirmation line
// "  Blocks: <id> (<title>)" that rendered the target issue's Title RAW via bare
// fmt.Printf (gate.go:357), bypassing ui.SanitizeForTerminal — a holdout in an
// already-partially-sanitized file (the sibling `bd gate list` at gate.go:415
// already sanitizes). targetIssue is store-read (store.GetIssue on the --blocks
// target), so a title from an untrusted import (JSONL/markdown/SCM) can carry
// OSC/CSI terminal-control escapes (OSC 0 window-title / OSC 52 clipboard). The
// fix routes it through displayTitle; display-only — the --json path
// (outputJSON(gate)) stays raw.
//
// The confirmation is a rc0 (return nil) path, so it runs bd as a subprocess
// with a target title carrying escapes and asserts stdout has no raw ESC/BEL
// while the visible title text and the structural output survive.
func TestGateCreateTitleSanitize_mvb6a(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"
	rawTitle := "Danger" + csi + osc + "Title"

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	// Seed the target issue whose title carries escapes. exec.Command passes the
	// arg as raw bytes (no shell), so the escapes reach the DB intact.
	createCmd := exec.Command(bd, "create", rawTitle, "-p", "2", "--json")
	createCmd.Dir = dir
	createCmd.Env = bdEnv(dir)
	cOut, cErr, cRunErr := runCommandBuffers(t, createCmd)
	if cRunErr != nil {
		t.Fatalf("seed create failed: %v\nstdout:\n%s\nstderr:\n%s", cRunErr, cOut.String(), cErr.String())
	}
	id := extractIssueID(t, cOut.String())

	// `bd gate create --blocks <id>` → creates a gate + prints the "Blocks:
	// <id> (<title>)" confirmation (rc0).
	gateCmd := exec.Command(bd, "gate", "create", "--blocks", id)
	gateCmd.Dir = dir
	gateCmd.Env = bdEnv(dir)
	gOut, gErr, gRunErr := runCommandBuffers(t, gateCmd)
	if gRunErr != nil {
		t.Fatalf("`bd gate create --blocks %s` failed: %v\nstdout:\n%s\nstderr:\n%s", id, gRunErr, gOut.String(), gErr.String())
	}

	out := gOut.String()
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("gate create leaked a raw ESC (\\x1b) — target title not sanitized (beads-mvb6a):\n%q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("gate create leaked a raw BEL (\\x07) — target title not sanitized (beads-mvb6a):\n%q", out)
	}
	// Visible title text must survive sanitize (escapes stripped, text kept).
	if !strings.Contains(out, "DangerTitle") {
		t.Errorf("gate create dropped visible title text (beads-mvb6a):\n%q", out)
	}
	// Structural output must still render.
	if !strings.Contains(out, "Blocks:") || !strings.Contains(out, id) {
		t.Errorf("gate create dropped structural confirmation output:\n%q", out)
	}
}
