package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-slq18 (xsmon/smrvu label-sink axis, restore leg): displayRestoredIssue
// (the `bd restore` preview) printed issue.Labels RAW via strings.Join,
// bypassing ui.SanitizeForTerminal — while Description/Design/AcceptanceCriteria/
// Notes/Assignee in the SAME function were sanitized (ihaw covered Title+Desc+
// Design, i8dsb covered Assignee; Labels was the last uncovered raw field).
// Restored labels come from an untrusted backup JSONL / Dolt snapshot and
// validateLabelValue permits ESC/OSC/CSI bytes, so a poisoned label injected
// terminal control sequences on the "Labels:" line.
//
// displayRestoredIssue only formats a *types.Issue to stdout (no store read),
// so this teeth is pure-Go (no cgo tag) and uses the pure-Go captureStdout
// helper. It drives the ACTUAL display function so a print-site regression is
// caught (a helper re-call would false-green it).
func TestDisplayRestoredIssue_LabelSanitize_slq18(t *testing.T) {
	const csi = "\x1b[31m"
	const osc52 = "\x1b]52;c;cHduZWQ=\x07"
	// Poison label: control escapes wrapped around visible text. No comma or
	// newline, so validateLabelValue would accept it and store it verbatim.
	poison := "dang" + csi + osc52 + "erlbl"
	const visible = "dangerlbl"

	iss := &types.Issue{
		ID:        "bd-slq18",
		Title:     "a clean title",
		Priority:  2,
		IssueType: types.TypeBug,
		Status:    types.StatusOpen,
		Labels:    []string{poison},
	}

	out := captureStdout(t, func() error {
		displayRestoredIssue(iss, "backup.jsonl")
		return nil
	})

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("displayRestoredIssue leaked a raw ESC (0x1b) — labels not sanitized (beads-slq18):\n%q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("displayRestoredIssue leaked a raw BEL (0x07) — labels not sanitized (beads-slq18):\n%q", out)
	}
	if !strings.Contains(out, visible) {
		t.Errorf("displayRestoredIssue dropped the visible label text %q:\n%q", visible, out)
	}
	// The "Labels:" framing must survive.
	if !strings.Contains(out, "Labels:") {
		t.Errorf("displayRestoredIssue dropped the 'Labels:' framing:\n%q", out)
	}

	// Round-trip fidelity: the stored label slice must be left untouched.
	if iss.Labels[0] != poison {
		t.Errorf("stored label was mutated by display; got %q want %q", iss.Labels[0], poison)
	}
}
