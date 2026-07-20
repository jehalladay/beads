package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-smrvu (xsmon label-sink axis sibling): the issue-LISTING views printed
// issue.Labels RAW via fmt %v, bypassing ui.SanitizeForTerminal — while the
// Title (displayTitle) and Assignee (ui.SanitizeForTerminal) rendered in the
// SAME loop were sanitized. Label values arrive from untrusted markdown/JSONL/
// SCM import and validateLabelValue permits ESC/OSC/CSI bytes (it rejects only
// comma/newline/>255), so a poisoned label injected terminal control sequences
// (OSC 52 clipboard-write, OSC 0/2 window-title) when a listing rendered them.
// xsmon fixed `bd label list`/`list-all` (label.go) but not these issue-listing
// sinks (query.go long+compact, search.go long+compact, list_format.go
// formatIssueLong+formatIssueCompact).
//
// These formatters are pure helpers, so the teeth drive them in-process (mirrors
// the jxi3d pure-Go approach). A helper re-call would false-green a print-site
// regression, so we exercise the ACTUAL formatting functions and assert the
// rendered buffer/string carries no raw ESC/BEL while the visible label text and
// the "Labels" framing survive. The STORED slice must never be mutated.
func TestIssueListingLabels_Sanitize_smrvu(t *testing.T) {
	const csi = "\x1b[31m"
	const osc52 = "\x1b]52;c;cHduZWQ=\x07"
	// Poison label: control escapes wrapped around visible text. No comma or
	// newline, so validateLabelValue would accept it and store it verbatim —
	// exactly what an untrusted import lands.
	poison := "dang" + csi + osc52 + "erlbl"
	const visible = "dangerlbl"

	newIssue := func() *types.Issue {
		return &types.Issue{
			ID:        "bd-smrvu",
			Title:     "a clean title",
			Priority:  2,
			IssueType: types.IssueType("task"),
			Status:    types.StatusOpen,
			Assignee:  "someone",
			Labels:    []string{poison},
		}
	}

	assertClean := func(t *testing.T, label, got string) {
		t.Helper()
		if strings.ContainsRune(got, '\x1b') {
			t.Errorf("%s leaked a raw ESC (0x1b) — labels not sanitized (beads-smrvu):\n%q", label, got)
		}
		if strings.ContainsRune(got, '\x07') {
			t.Errorf("%s leaked a raw BEL (0x07) — labels not sanitized (beads-smrvu):\n%q", label, got)
		}
		if !strings.Contains(got, visible) {
			t.Errorf("%s dropped the visible label text %q:\n%q", label, visible, got)
		}
	}

	t.Run("formatIssueLong", func(t *testing.T) {
		var buf strings.Builder
		iss := newIssue()
		formatIssueLong(&buf, iss, iss.Labels, false)
		got := buf.String()
		assertClean(t, "formatIssueLong", got)
		// The long format uses an explicit "Labels:" line — verify it survives.
		if !strings.Contains(got, "Labels:") {
			t.Errorf("formatIssueLong dropped the 'Labels:' framing:\n%q", got)
		}
	})

	t.Run("formatIssueCompact", func(t *testing.T) {
		var buf strings.Builder
		iss := newIssue()
		formatIssueCompact(&buf, iss, iss.Labels, nil, nil, "")
		assertClean(t, "formatIssueCompact", buf.String())
	})

	t.Run("formatQueryIssue", func(t *testing.T) {
		var buf strings.Builder
		iss := newIssue()
		formatQueryIssue(&buf, iss)
		assertClean(t, "formatQueryIssue", buf.String())
	})

	// The stored slice must be left untouched (round-trip fidelity): the raw
	// escape bytes must still be present on the issue after formatting.
	t.Run("stored_slice_not_mutated", func(t *testing.T) {
		var buf strings.Builder
		iss := newIssue()
		formatIssueLong(&buf, iss, iss.Labels, false)
		if iss.Labels[0] != poison {
			t.Errorf("stored label was mutated by display; got %q want %q", iss.Labels[0], poison)
		}
	})
}
