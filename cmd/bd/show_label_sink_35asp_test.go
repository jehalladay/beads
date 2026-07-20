//go:build cgo

package main

import (
	"strings"
	"testing"
)

// beads-35asp (direct twin of TestProxiedShowLabelSink_sanitize_35asp): the
// single-issue `bd show <id>` DIRECT detail view printed its LABELS line RAW
// via strings.Join(labels, ", ") at show.go:292, bypassing
// ui.SanitizeForTerminal. A label can carry ESC/OSC/CSI bytes from an untrusted
// import (validateLabelValue rejects only comma/newline/>255), so a poisoned
// label injected terminal control sequences when an operator ran `bd show`. Fix
// routes the join through displayLabels(); display-only.
//
// End-to-end teeth: add an ESC-bearing label through the real CLI (validate
// accepts it), then `bd show` and assert the raw escape never reaches stdout
// while the visible text survives. Mutation proof: revert displayLabels() at
// show.go:292 to the raw join and this goes RED.
func TestShowLabelSink_sanitize_35asp(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"
	poison := "ShowLbl" + csi + osc + "END"

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	id := bdCreateSilent(t, bd, dir, "A bead", "--type", "task")

	if out, err := bdRunWithFlockRetry(t, bd, dir, "label", "add", id, "--label", poison); err != nil {
		t.Fatalf("label add with ESC-bearing value failed: %v\n%s", err, out)
	}

	out, err := bdRunWithFlockRetry(t, bd, dir, "show", id)
	if err != nil {
		t.Fatalf("bd show failed: %v\n%s", err, out)
	}
	s := string(out)
	if strings.ContainsRune(s, '\x1b') {
		t.Errorf("bd show leaked a raw ESC (\\x1b) — LABELS line not sanitized (beads-35asp): %q", s)
	}
	if strings.ContainsRune(s, '\x07') {
		t.Errorf("bd show leaked a raw BEL (\\x07) — LABELS line not sanitized (beads-35asp): %q", s)
	}
	if !strings.Contains(s, "ShowLblEND") {
		t.Errorf("bd show dropped/garbled visible label text (beads-35asp): %q", s)
	}
}
