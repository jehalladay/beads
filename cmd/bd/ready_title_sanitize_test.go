//go:build cgo

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestReadyTitleSanitize_aoz1q is the sanitize teeth for beads-aoz1q (7n9y
// sink-class slice). `bd ready` printed issue/item/step titles RAW via bare
// fmt.Printf in its verbose/blocked/molecule/explain outputs
// (ready.go:357/434/647/668/671/742/780), bypassing ui.SanitizeForTerminal —
// unlike the feedback line (ready.go:284) which routes through
// formatFeedbackID→applyTitleConfig (already sanitizes, beads-j8li). A title
// from an untrusted import (JSONL/markdown/SCM stores it raw) can carry
// OSC/CSI terminal-control escapes, so `bd ready` injected control sequences
// into the terminal. The fix routes each direct Title sink through displayTitle
// (display-only; the stored title + the --json round-trip path stay raw).
//
// Exercises the primary sink (357, the plain ready list) end-to-end: seed an
// issue whose title carries escapes, run `bd ready --plain`, and assert stdout
// has no raw ESC/BEL while the visible title text survives.
func TestReadyTitleSanitize_aoz1q(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"
	rawTitle := "Danger" + csi + osc + "Title"

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	// exec.Command passes the arg as raw bytes (no shell), so the escapes reach
	// the DB intact.
	createCmd := exec.Command(bd, "create", rawTitle, "-p", "1", "--json")
	createCmd.Dir = dir
	createCmd.Env = bdEnv(dir)
	cOut, cErr, cRunErr := runCommandBuffers(t, createCmd)
	if cRunErr != nil {
		t.Fatalf("seed create failed: %v\nstdout:\n%s\nstderr:\n%s", cRunErr, cOut.String(), cErr.String())
	}
	_ = extractIssueID(t, cOut.String())

	readyCmd := exec.Command(bd, "ready", "--plain")
	readyCmd.Dir = dir
	readyCmd.Env = bdEnv(dir)
	rOut, rErr, rRunErr := runCommandBuffers(t, readyCmd)
	if rRunErr != nil {
		t.Fatalf("`bd ready --plain` failed: %v\nstdout:\n%s\nstderr:\n%s", rRunErr, rOut.String(), rErr.String())
	}

	out := rOut.String()
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("bd ready leaked a raw ESC (\\x1b) — title not sanitized (beads-aoz1q):\n%q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("bd ready leaked a raw BEL (\\x07) — title not sanitized (beads-aoz1q):\n%q", out)
	}
	// Visible title text must survive sanitize (escapes stripped, text kept).
	if !strings.Contains(out, "Danger") || !strings.Contains(out, "Title") {
		t.Errorf("bd ready dropped the visible title text (over-sanitized): %q", out)
	}
}
