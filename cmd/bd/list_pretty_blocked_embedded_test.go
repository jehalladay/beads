//go:build cgo

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestListPrettyBlockedSignal_54lww is the end-to-end teeth for beads-54lww:
// the DEFAULT bd list pretty/tree view must show the ● blocked icon +
// "(blocked by: X)" annotation for an open issue with an active blocker, at
// parity with `bd list --flat`. Before the fix the pretty view rendered the
// issue as "○ ... open" with no annotation (it under-signalled blocked state),
// while --flat, bd ready, and bd blocked all agreed it was blocked.
//
// Repro (from the bead): create A and B, `dep add B A --type blocks`; B is now
// blocked by the open A.
func TestListPrettyBlockedSignal_54lww(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "lw")

	a := bdCreate(t, bd, dir, "Blocker A", "-p", "1")
	b := bdCreate(t, bd, dir, "Blocked B", "-p", "1")
	bdDep(t, bd, dir, "add", b.ID, a.ID, "--type", "blocks")

	runList := func(args ...string) string {
		full := append([]string{"list"}, args...)
		cmd := exec.Command(bd, full...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd list %v failed: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout.String(), stderr.String())
		}
		return stdout.String()
	}

	// blockedLine returns the output line that renders issue id in its ID field
	// (begins "<icon> <id> "), i.e. B's own row — not a row that merely mentions
	// it inside another issue's "(blocked by: ...)" annotation.
	blockedLine := func(out, id string) string {
		for _, ln := range strings.Split(out, "\n") {
			if strings.Contains(ln, " "+id+" ") {
				return ln
			}
		}
		return ""
	}

	// --- DEFAULT pretty/tree view (the regression seam) ---
	pretty := runList("--pretty")
	bLine := blockedLine(pretty, b.ID)
	if bLine == "" {
		t.Fatalf("could not find B's line in --pretty output:\n%s", pretty)
	}
	if !strings.Contains(bLine, "●") {
		t.Errorf("--pretty: blocked issue B should render ● blocked, got line: %q\nfull:\n%s", bLine, pretty)
	}
	if strings.Contains(bLine, "○") {
		t.Errorf("--pretty: blocked issue B must not render ○ open, got line: %q", bLine)
	}
	if !strings.Contains(bLine, "(blocked by: "+a.ID+")") {
		t.Errorf("--pretty: expected '(blocked by: %s)' on B, got line: %q", a.ID, bLine)
	}
	// The unblocked blocker A stays ○ open with no blocked annotation.
	aLine := blockedLine(pretty, a.ID)
	if aLine != "" && strings.Contains(aLine, "blocked by") {
		t.Errorf("--pretty: unblocked blocker A must not be annotated as blocked, got line: %q", aLine)
	}

	// --- parity: --flat already showed it; confirm both agree ---
	flat := runList("--flat")
	fLine := blockedLine(flat, b.ID)
	if !strings.Contains(fLine, "●") || !strings.Contains(fLine, "(blocked by: "+a.ID+")") {
		t.Errorf("--flat parity baseline: B should show ● + '(blocked by: %s)', got line: %q", a.ID, fLine)
	}
}
