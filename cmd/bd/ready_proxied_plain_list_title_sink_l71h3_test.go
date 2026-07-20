//go:build cgo

package main

import (
	"context"
	"strings"
	"testing"
)

// beads-l71h3 (7n9y proxied twin): the proxied-server plain-verbose ready LIST
// (runReadyProxiedList usePlain branch, ready_proxied_server.go:192) printed the
// stored issue.Title RAW via fmt.Printf, bypassing ui.SanitizeForTerminal
// (displayTitle). Its DIRECT twin ready.go usePlain (:445) already routes Title
// through displayTitle and Assignee through ui.SanitizeForTerminal. A title can
// originate from an untrusted import (JSONL/markdown/SCM) carrying OSC/CSI
// terminal-control escapes (OSC 0 window-title / OSC 52 clipboard), so this view
// injected control sequences onto its lines.
//
// This is the sibling still-uncovered by beads-0stio (which sanitized the
// blocked/explain/molecule proxied text views) and beads-3nkwv (molecule header
// / gated list). The Assignee line (:197) was already sanitized in the base, so
// only the Title needed the fix. Display-only — the STORED title and JSON paths
// are unchanged.
//
// The title is planted directly into issues.title via SQL to model an untrusted
// import (bd create validates its own CLI arg differently; the leak is about
// REDISPLAYING already-stored bytes), then `bd ready --plain` (which forces the
// usePlain verbose list loop) is run and asserted escape-free with the visible
// text preserved. Mutation proof: revert displayTitle() at :192 to the raw
// issue.Title and the assertNoRawEscapes assertion goes RED.
func TestProxiedReadyPlainListTitleSink_sanitize_l71h3(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"
	// A visible-text remnant to assert the title still renders (sanitize strips
	// the escape, not the surrounding characters).
	poison := "PlainListTitle" + osc + csi + "END"

	p := bdProxiedInit(t, bd, "l71plain")
	ready := bdProxiedCreate(t, bd, p.dir, "Ready seed")

	// Plant a raw-escape title straight into the issues table, bypassing any
	// create-time input handling — modelling an imported title.
	db := openProxiedDB(t, p)
	if _, err := db.ExecContext(context.Background(),
		"UPDATE issues SET title = ? WHERE id = ?", poison, ready.ID); err != nil {
		t.Fatalf("plant escape title on %s: %v", ready.ID, err)
	}

	// --plain forces the usePlain verbose list loop (ready_proxied_server.go:186).
	stdout, _ := bdProxiedReadyCapture(t, bd, p, "--plain")
	assertNoRawEscapes(t, stdout, "proxied plain-verbose ready list title")
	if !strings.Contains(stdout, "PlainListTitleEND") {
		t.Errorf("proxied plain-verbose ready list dropped visible title text: %q", stdout)
	}
}
