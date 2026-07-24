//go:build cgo

package main

import (
	"strings"
	"testing"
)

// beads-35asp (xsmon/smrvu label-sink twin): the single-issue `bd show <id>`
// detail view printed its LABELS line RAW via strings.Join(labels, ", ") with
// no ui.SanitizeForTerminal, in BOTH the direct sink (show.go:292) and the
// proxied sink (show_proxied_server.go:651). Label values arrive from untrusted
// markdown/JSONL/SCM import and validateLabelValue permits ESC/OSC/CSI bytes
// (it rejects only comma/newline/>255), so a poisoned label injected terminal
// control sequences (OSC 52 clipboard-write, OSC 0/2 window-title) whenever an
// operator ran `bd show`.
//
// beads-xsmon fixed the `bd label list/list-all` subcommands; beads-smrvu added
// the displayLabels() helper and covered the 6 issue-LISTING sinks
// (query/search/list). The single-issue show DETAIL line fell between both
// slices — the same seam beads-l71h3 (proxied plain-list Title) fell through.
// Fix routes both show LABELS joins through displayLabels(); display-only, the
// stored labels slice and JSON path are unchanged.
//
// This drives the real proxied bd binary: add an ESC-bearing label through the
// CLI (validate accepts it, proving the sink is reachable via normal storage),
// then `bd show` and assert the raw escape never reaches stdout while the
// visible label text survives. Mutation proof: drop displayLabels() at
// show_proxied_server.go:651 back to the raw join and assertNoRawEscapes goes
// RED.
func TestProxiedShowLabelSink_sanitize_35asp(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"
	// A visible-text remnant to assert the label still renders (sanitize strips
	// the escape, not the surrounding characters). No comma/newline so
	// validateLabelValue accepts it and it is stored verbatim.
	poison := "ShowLbl" + csi + osc + "END"

	p := bdProxiedInit(t, bd, "l35asp")
	issue := bdProxiedCreate(t, bd, p.dir, "Show label seed", "--type", "task")

	// Add the poisoned label through the real proxied CLI — this must succeed
	// (validate does not block ESC), landing the raw bytes in storage.
	if out, err := bdProxiedRun(t, bd, p.dir, "label", "add", issue.ID, "--label", poison); err != nil {
		t.Fatalf("proxied label add with ESC-bearing value failed: %v\n%s", err, out)
	}

	stdout := bdProxiedShowRaw(t, bd, p.dir, issue.ID)
	assertNoRawEscapes(t, stdout, "proxied bd show detail-view LABELS line")
	// Labels are case-folded at write (beads-9jjj8) → visible text lowercased.
	if !strings.Contains(strings.ToLower(stdout), "showlblend") {
		t.Errorf("proxied bd show dropped/garbled visible label text: %q", stdout)
	}
}
