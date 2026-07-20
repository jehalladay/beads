//go:build cgo

package main

import (
	"context"
	"strings"
	"testing"
)

// beads-0stio (7n9y slice): the proxied-server ready/blocked/explain/molecule
// TEXT views printed stored issue titles RAW via fmt.Printf, bypassing
// ui.SanitizeForTerminal (displayTitle) — the proxied twins of already-sanitized
// direct ready.go sinks. A title can originate from an untrusted import
// (JSONL/markdown/SCM) carrying OSC/CSI terminal-control escapes (OSC 0
// window-title / OSC 52 clipboard), so these views injected control sequences
// onto their lines. This is the sibling of beads-l71h3 (plain-verbose list :192)
// and beads-3nkwv (molecule header / gated list): the remaining uncovered sinks
// were runBlockedProxiedServer (:113), runReadyProxiedExplain (:321/:340/:343),
// and runReadyProxiedMolecule (:432). Fix routes each through displayTitle.
// Display-only — the STORED title and JSON paths are unchanged.
//
// The title is planted directly into issues.title via SQL to model an untrusted
// import (bd create sanitizes/validates its own CLI arg differently; the leak is
// about REDISPLAYING already-stored bytes), then each real proxied command is
// run and asserted escape-free with the visible text preserved.
func TestProxiedReadyTitleSinks_sanitize_0stio(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"
	// A visible-text remnant to assert the title still renders (sanitize strips
	// the escape, not the surrounding characters).
	poison := func(prefix string) string { return prefix + osc + csi + "END" }

	// setTitle plants a raw-escape title straight into the issues table,
	// bypassing any create-time input handling — modelling an imported title.
	setTitle := func(t *testing.T, p proxiedProject, id, title string) {
		t.Helper()
		db := openProxiedDB(t, p)
		if _, err := db.ExecContext(context.Background(),
			"UPDATE issues SET title = ? WHERE id = ?", title, id); err != nil {
			t.Fatalf("plant escape title on %s: %v", id, err)
		}
	}

	t.Run("blocked_list", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "s0blk")
		blocker := bdProxiedCreate(t, bd, p.dir, "Blocker seed")
		dep := bdProxiedCreate(t, bd, p.dir, "Blocked dependent", "--deps", "depends-on:"+blocker.ID)
		setTitle(t, p, dep.ID, poison("BlockedTitle"))

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "blocked")
		if err != nil {
			t.Fatalf("bd blocked failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		assertNoRawEscapes(t, stdout, "proxied blocked list")
		if !strings.Contains(stdout, "BlockedTitleEND") {
			t.Errorf("blocked list dropped visible title text: %q", stdout)
		}
	})

	t.Run("explain_ready_and_blocked", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "s0exp")
		ready := bdProxiedCreate(t, bd, p.dir, "Ready seed")
		blocker := bdProxiedCreate(t, bd, p.dir, "Blocker seed")
		dep := bdProxiedCreate(t, bd, p.dir, "Blocked dependent", "--deps", "depends-on:"+blocker.ID)
		setTitle(t, p, ready.ID, poison("ReadyTitle"))
		setTitle(t, p, dep.ID, poison("BlkTitle"))
		setTitle(t, p, blocker.ID, poison("BlockerTitle"))

		stdout, _ := bdProxiedReadyCapture(t, bd, p, "--explain")
		assertNoRawEscapes(t, stdout, "proxied ready --explain")
		for _, want := range []string{"ReadyTitleEND", "BlkTitleEND", "BlockerTitleEND"} {
			if !strings.Contains(stdout, want) {
				t.Errorf("explain output dropped visible title %q: %q", want, stdout)
			}
		}
	})

	t.Run("molecule_step", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "s0mol")
		mol := bdProxiedCreate(t, bd, p.dir, "Mol parent", "--type", "molecule")
		step := bdProxiedCreate(t, bd, p.dir, "Step one", "--parent", mol.ID)
		setTitle(t, p, step.ID, poison("StepTitle"))

		stdout, _ := bdProxiedReadyCapture(t, bd, p, "--mol", mol.ID)
		assertNoRawEscapes(t, stdout, "proxied ready --mol step list")
		if !strings.Contains(stdout, "StepTitleEND") {
			t.Errorf("molecule step list dropped visible title text: %q", stdout)
		}
	})
}
