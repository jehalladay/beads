package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestPrintWispCreateDryRunPreview_SanitizesTitle_w8zm4 is the sanitize teeth
// for beads-w8zm4 (7n9y sink-class twin). `bd wisp create --dry-run` prints a
// per-issue preview whose title is variable-substituted (substituteVariables,
// a plain regex ReplaceAllStringFunc that preserves control chars) and was
// then printed RAW via fmt.Printf at wisp.go:286, bypassing displayTitle. A
// proto issue.Title from an untrusted import (bd import does no control-char
// validation) can carry OSC/CSI escapes (OSC 0 window-title / OSC 52
// clipboard), so the preview injected control sequences into the terminal.
// The fix routes the substituted title through displayTitle
// (ui.SanitizeForTerminal).
//
// The preview loop is extracted into the pure printWispCreateDryRunPreview so
// it is testable directly with a bytes.Buffer (no store/cgo needed). Mutation
// proof: revert the displayTitle() call to the raw substituted title and the
// ESC/BEL assertions go RED.
func TestPrintWispCreateDryRunPreview_SanitizesTitle_w8zm4(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"
	// The escape lives in the substituted VALUE, exercising the post-substitution
	// sink (a var value is just as untrusted as the literal title).
	issues := []*types.Issue{
		{ID: "proto-1", Title: "Danger" + csi + "{{name}}" + osc + "Tail"},
	}
	vars := map[string]string{"name": "Injected" + csi}

	var buf bytes.Buffer
	printWispCreateDryRunPreview(&buf, issues, vars)
	out := buf.String()

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("wisp create --dry-run preview leaked a raw ESC (\\x1b) — title not sanitized (beads-w8zm4):\n%q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("wisp create --dry-run preview leaked a raw BEL (\\x07) — title not sanitized (beads-w8zm4):\n%q", out)
	}
	// Visible text (with the var substituted in) must survive sanitize.
	for _, want := range []string{"Danger", "Injected", "Tail", "proto-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("wisp create --dry-run preview dropped visible text %q (beads-w8zm4):\n%q", want, out)
		}
	}
}
