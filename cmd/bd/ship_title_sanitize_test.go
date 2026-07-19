package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-pwhu1 (7n9y slice): when more than one issue carries the export label,
// both the direct (ship.go) and proxied (ship_proxied_server.go) ship paths
// printed each issue.Title RAW via fmt.Fprintf in the disambiguation list,
// bypassing ui.SanitizeForTerminal. A title can originate from an untrusted
// import (JSONL/markdown/SCM) carrying OSC/CSI terminal-control escapes (OSC 0
// window-title / OSC 52 clipboard), so the error list injected control
// sequences into the terminal. Both paths now share printShipMultiLabelMatches,
// which routes each title through displayTitle.
func TestPrintShipMultiLabelMatches_sanitize(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	issues := []*types.Issue{
		{ID: "bd-a", Title: "Alpha" + osc + "Ship", Status: types.StatusOpen},
		{ID: "bd-b", Title: "Beta" + csi + osc + "Ship", Status: types.StatusClosed},
	}

	var buf bytes.Buffer
	printShipMultiLabelMatches(&buf, issues, "export:foo")
	out := buf.String()

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("ship multi-label list leaked a raw ESC (\\x1b): %q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("ship multi-label list leaked a raw BEL (\\x07): %q", out)
	}
	for _, want := range []string{"AlphaShip", "BetaShip", "bd-a", "bd-b", "export:foo"} {
		if !strings.Contains(out, want) {
			t.Errorf("ship multi-label list dropped %q: %q", want, out)
		}
	}
}
