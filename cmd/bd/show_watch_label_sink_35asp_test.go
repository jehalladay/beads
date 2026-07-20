//go:build cgo

package main

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// beads-35asp (watch-mode sibling of TestShowLabelSink_sanitize_35asp): the
// `bd show --watch <id>` display path (displayShowIssue/displayShowIssueReturn
// in show_display.go, reused for auto-refresh) printed its LABELS line RAW via
// strings.Join(labels, ", ") at show_display.go:130, bypassing
// ui.SanitizeForTerminal — the same sink as the plain `bd show` detail view but
// in a different file, so it fell outside the show.go / show_proxied_server.go
// fix. A label carrying ESC/OSC/CSI bytes from an untrusted import therefore
// injected terminal control sequences on the watch initial render (which fires
// before any poll). Fix routes the join through displayLabels(); display-only.
//
// watchIssue blocks (infinite poll loop), so this drives it under a short
// context timeout and asserts the INITIAL render — emitted immediately by
// displayShowIssueReturn before the ticker — is escape-free while the visible
// text survives. Mutation proof: revert displayLabels() at show_display.go:130
// to the raw join and this goes RED.
func TestShowWatchLabelSink_sanitize_35asp(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"
	poison := "WatchLbl" + csi + osc + "END"

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	id := bdCreateSilent(t, bd, dir, "A bead", "--type", "task")

	if out, err := bdRunWithFlockRetry(t, bd, dir, "label", "add", id, "--label", poison); err != nil {
		t.Fatalf("label add with ESC-bearing value failed: %v\n%s", err, out)
	}

	// --watch blocks forever; give it enough time to render initially, then the
	// context cancellation kills it. The initial render happens synchronously
	// before the poll ticker, so a short window captures the LABELS line.
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bd, "show", "--watch", id)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run() // expected to be killed by the timeout; we only inspect stdout

	s := stdout.String()
	if !strings.Contains(s, "WatchLbl") {
		t.Fatalf("bd show --watch produced no LABELS render to inspect (got %q, stderr %q)", s, stderr.String())
	}
	if strings.ContainsRune(s, '\x1b') {
		t.Errorf("bd show --watch leaked a raw ESC (\\x1b) — LABELS line not sanitized (beads-35asp): %q", s)
	}
	if strings.ContainsRune(s, '\x07') {
		t.Errorf("bd show --watch leaked a raw BEL (\\x07) — LABELS line not sanitized (beads-35asp): %q", s)
	}
	if !strings.Contains(s, "WatchLblEND") {
		t.Errorf("bd show --watch dropped/garbled visible label text (beads-35asp): %q", s)
	}
}
