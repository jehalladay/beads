//go:build cgo

package main

import (
	"strings"
	"testing"
)

// beads-xsmon: `bd label list` and `bd label list-all` printed stored label
// values RAW via fmt.Printf, bypassing ui.SanitizeForTerminal. A label can
// originate from an untrusted external source (SCM/JSONL/markdown import), and
// validateLabelValue (internal/storage/domain/label.go) only rejects
// comma/newline/length — ESC(0x1b)/OSC/CSI bytes pass straight through and are
// stored verbatim. So a poisoned label injected terminal control sequences
// (OSC 0 window-title / OSC 52 clipboard) when listed. list-all is the worst
// vector: it aggregates labels across EVERY issue, so one poisoned import hits
// every operator's terminal.
//
// This is the label twin of the 7n9y title-sink umbrella (which covered
// issue.Title/.Description only). End-to-end teeth: drive the real bd binary —
// add an ESC-bearing label (which validate accepts), then list and assert the
// raw ESC never reaches stdout, while the visible text survives.
func TestLabelList_sanitize_xsmon(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"
	// Poison label: control escapes wrapped around visible text. No comma or
	// newline, so validateLabelValue accepts it and it is stored verbatim —
	// exactly what an untrusted import would land.
	poison := "dang" + csi + osc + "erlbl"
	const visible = "dangerlbl"

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	id := bdCreateSilent(t, bd, dir, "A bead", "--type", "task")

	// Add the poisoned label through the real CLI. This must succeed (validate
	// does not block ESC) — proving the sink is reachable via normal storage.
	if out, err := bdRunWithFlockRetry(t, bd, dir, "label", "add", id, "--label", poison); err != nil {
		t.Fatalf("label add with ESC-bearing value failed: %v\n%s", err, out)
	}

	t.Run("label_list", func(t *testing.T) {
		out, err := bdRunWithFlockRetry(t, bd, dir, "label", "list", id)
		if err != nil {
			t.Fatalf("label list failed: %v\n%s", err, out)
		}
		s := string(out)
		if strings.ContainsRune(s, '\x1b') {
			t.Errorf("label list leaked a raw ESC (\\x1b): %q", s)
		}
		if strings.ContainsRune(s, '\x07') {
			t.Errorf("label list leaked a raw BEL (\\x07): %q", s)
		}
		if !strings.Contains(s, visible) {
			t.Errorf("label list dropped the visible label text %q: %q", visible, s)
		}
	})

	t.Run("label_list_all", func(t *testing.T) {
		out, err := bdRunWithFlockRetry(t, bd, dir, "label", "list-all")
		if err != nil {
			t.Fatalf("label list-all failed: %v\n%s", err, out)
		}
		s := string(out)
		if strings.ContainsRune(s, '\x1b') {
			t.Errorf("label list-all leaked a raw ESC (\\x1b): %q", s)
		}
		if strings.ContainsRune(s, '\x07') {
			t.Errorf("label list-all leaked a raw BEL (\\x07): %q", s)
		}
		if !strings.Contains(s, visible) {
			t.Errorf("label list-all dropped the visible label text %q: %q", visible, s)
		}
	})
}
