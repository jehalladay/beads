//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestDuplicatesGroupHeaderTitleSanitize_13sqi is the sanitize teeth for
// beads-13sqi (7n9y sink-enum delta). `bd duplicates` prints each group's
// header as "Group N: <group[0].Title>" via a bare fmt.Printf
// (duplicates.go:149), rendering the title RAW and bypassing
// ui.SanitizeForTerminal. A title from an untrusted import (JSONL/markdown/SCM)
// can carry OSC/CSI terminal-control escapes, so the group header injected
// control sequences into the terminal. The fix routes group[0].Title through
// displayTitle. Display-only — the --json path (which emits the raw title as a
// machine-readable field) is unaffected.
func TestDuplicatesGroupHeaderTitleSanitize_13sqi(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}

	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"
	rawTitle := "Danger" + csi + osc + "Dup"

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "dts")

	// Two issues with identical (escape-laden) title+description form a
	// duplicate group. exec passes the arg as raw bytes (no shell), so the
	// escapes reach the DB intact.
	for i := 0; i < 2; i++ {
		c := exec.Command(bd, "create", rawTitle, "--type", "task", "--description", "same body", "--json")
		c.Dir = dir
		c.Env = bdEnv(dir)
		out, errBuf, err := runCommandBuffers(t, c)
		if err != nil {
			t.Fatalf("seed create %d failed: %v\nstdout:\n%s\nstderr:\n%s", i, err, out.String(), errBuf.String())
		}
	}

	dupCmd := exec.Command(bd, "duplicates")
	dupCmd.Dir = dir
	dupCmd.Env = bdEnv(dir)
	dOut, dErr, dRunErr := runCommandBuffers(t, dupCmd)
	if dRunErr != nil {
		t.Fatalf("`bd duplicates` failed: %v\nstdout:\n%s\nstderr:\n%s", dRunErr, dOut.String(), dErr.String())
	}

	out := dOut.String()
	// Precondition: a group must have been detected, or the header sink never
	// renders and the test proves nothing.
	if !strings.Contains(out, "Group 1:") {
		t.Fatalf("expected a 'Group 1:' header (duplicate group), got:\n%q", out)
	}
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("duplicates group header leaked a raw ESC (\\x1b) — title not sanitized (beads-13sqi):\n%q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("duplicates group header leaked a raw BEL (\\x07) — title not sanitized (beads-13sqi):\n%q", out)
	}
	// The visible title text must survive sanitize (escapes stripped, text kept).
	if !strings.Contains(out, "Danger") || !strings.Contains(out, "Dup") {
		t.Errorf("visible title text did not survive sanitize in the group header:\n%q", out)
	}
}
