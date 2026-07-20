package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-tt13r (xsmon/smrvu/slq18 label-sink axis, create + markdown dry-run legs):
// renderCreateDryRunPreview (bd create --dry-run) and dryRunMarkdownBatch
// (bd create --from-markdown --dry-run) printed labels RAW via strings.Join,
// bypassing ui.SanitizeForTerminal — while every sibling field in the SAME
// preview function was sanitized (create: Title/Desc ihaw, Assignee i8dsb,
// EventKind k86xm; markdown: Title displayTitle, Assignee SanitizeForTerminal).
// Labels was the last uncovered raw field. Dry-run labels come from --label /
// inherited (create) and a parsed '### Labels' markdown file (markdown), and
// validateLabelValue permits ESC/OSC/CSI bytes, so a poisoned label injected
// terminal control sequences on the "Labels:" preview line.
//
// Both functions only format to stdout (no store read), so this teeth is
// pure-Go (no cgo tag) and drives the ACTUAL preview functions via the pure-Go
// captureStdout helper — a helper re-call would false-green a print-site
// regression.

// csi/osc52 poison: control escapes wrapped around visible text. No comma or
// newline, so validateLabelValue would accept it and store it verbatim.
const tt13rCSI = "\x1b[31m"
const tt13rOSC52 = "\x1b]52;c;cHduZWQ=\x07"

func tt13rPoison() string  { return "dang" + tt13rCSI + tt13rOSC52 + "erlbl" }
const tt13rVisible = "dangerlbl"

func tt13rAssertSanitized(t *testing.T, out, label string) {
	t.Helper()
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("%s leaked a raw ESC (0x1b) — labels not sanitized (beads-tt13r):\n%q", label, out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("%s leaked a raw BEL (0x07) — labels not sanitized (beads-tt13r):\n%q", label, out)
	}
	if !strings.Contains(out, tt13rVisible) {
		t.Errorf("%s dropped the visible label text %q:\n%q", label, tt13rVisible, out)
	}
	if !strings.Contains(out, "Labels:") {
		t.Errorf("%s dropped the 'Labels:' framing:\n%q", label, out)
	}
}

func TestRenderCreateDryRunPreview_LabelSanitize_tt13r(t *testing.T) {
	poison := tt13rPoison()
	labels := []string{poison}

	iss := &types.Issue{
		ID:        "bd-tt13r",
		Title:     "a clean title",
		Priority:  2,
		IssueType: types.TypeBug,
		Status:    types.StatusOpen,
	}

	out := captureStdout(t, func() error {
		renderCreateDryRunPreview(iss, labels, nil)
		return nil
	})

	tt13rAssertSanitized(t, out, "renderCreateDryRunPreview")

	// Round-trip fidelity: the input label slice must not be mutated by display.
	if labels[0] != poison {
		t.Errorf("input label was mutated by display; got %q want %q", labels[0], poison)
	}
}

func TestDryRunMarkdownBatch_LabelSanitize_tt13r(t *testing.T) {
	poison := tt13rPoison()
	tmpl := &IssueTemplate{
		Title:     "a clean title",
		Priority:  2,
		IssueType: types.TypeBug,
		Labels:    []string{poison},
	}

	out := captureStdout(t, func() error {
		return dryRunMarkdownBatch([]*IssueTemplate{tmpl}, "poison.md")
	})

	tt13rAssertSanitized(t, out, "dryRunMarkdownBatch")

	if tmpl.Labels[0] != poison {
		t.Errorf("template label was mutated by display; got %q want %q", tmpl.Labels[0], poison)
	}
}
