package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// beads-tberq (7n9y slice): runGateCreateProxied printed the target issue's
// Title RAW in the "Blocks: ID (title)" gate-create confirmation, bypassing
// ui.SanitizeForTerminal — the proxied twin of the direct gate.go gate-create
// sink. A title can originate from an untrusted import (JSONL/markdown/SCM)
// carrying OSC/CSI terminal-control escapes (OSC 0 window-title / OSC 52
// clipboard), so the confirmation injected control sequences onto that line.
// The fix routes the title through displayTitle via printGateCreateSummary.
func TestPrintGateCreateSummary_sanitize(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	var buf bytes.Buffer
	printGateCreateSummary(&buf, "gate-1", "manual", "bd-target", "Alpha"+csi+osc+"Target", "because", 5*time.Minute)
	out := buf.String()

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("gate-create summary leaked a raw ESC (\\x1b): %q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("gate-create summary leaked a raw BEL (\\x07): %q", out)
	}
	// Visible title text and structural output must survive.
	for _, want := range []string{"AlphaTarget", "bd-target", "gate-1", "because"} {
		if !strings.Contains(out, want) {
			t.Errorf("gate-create summary dropped %q: %q", want, out)
		}
	}
}
