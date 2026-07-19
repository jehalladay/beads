package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-3nkwv (7n9y slice): the proxied-server ready views printed molecule,
// closed-gate, and ready-step titles RAW via fmt.Printf, bypassing
// ui.SanitizeForTerminal — the proxied twins of the direct ready.go sinks. A
// title can originate from an untrusted import (JSONL/markdown/SCM) carrying
// OSC/CSI terminal-control escapes (OSC 0 window-title / OSC 52 clipboard), so
// these views injected control sequences onto their lines. The fix routes each
// through displayTitle. Display-only — stored titles and the JSON path are
// unchanged.

func TestPrintProxiedMoleculeReadyHeader_sanitize(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"

	out := captureStdout(t, func() error {
		printProxiedMoleculeReadyHeader("Alpha"+osc+"Mol", "mol-1", 3, 1)
		return nil
	})

	assertNoRawEscapes(t, out, "proxied molecule-ready header")
	for _, want := range []string{"AlphaMol", "mol-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("proxied molecule-ready header dropped %q: %q", want, out)
		}
	}
}

func TestPrintProxiedGatedMolecules_sanitize(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	molecules := []*GatedMolecule{
		{
			MoleculeID:    "mol-1",
			MoleculeTitle: "Alpha" + osc + "Mol",
			ClosedGate:    &types.Issue{ID: "gate-1", Title: "Beta" + csi + "Gate"},
			ReadyStep:     &types.Issue{ID: "step-1", Title: "Gamma" + osc + "Step"},
		},
	}

	out := captureStdout(t, func() error {
		printProxiedGatedMolecules(molecules)
		return nil
	})

	assertNoRawEscapes(t, out, "proxied gated molecules")
	for _, want := range []string{"AlphaMol", "BetaGate", "GammaStep", "mol-1", "gate-1", "step-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("proxied gated molecules dropped %q: %q", want, out)
		}
	}
}

// assertNoRawEscapes is defined in dep_proxied_server_sanitize_test.go (same
// package) — the shared 7n9y sanitize assertion helper.
