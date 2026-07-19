//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestDeletePreviewTitleSanitize_jra0 is the sanitize teeth for beads-jra0
// (7n9y sink-class slice). `bd delete <id>` without --force prints a DELETE
// PREVIEW that rendered the issue.Title (and connected-issue titles) RAW via
// bare fmt.Printf (delete.go:140/160/370), bypassing ui.SanitizeForTerminal. A
// title from an untrusted import (JSONL/markdown/SCM) can carry OSC/CSI
// terminal-control escapes (OSC 0 window-title / OSC 52 clipboard), so the
// preview injected control sequences into the terminal. The fix routes each
// Title sink through displayTitle (the same helper the completions/doctor
// slices use). Sink-class tail of j8li/ihaw / beads-7n9y.
//
// The preview is a rc0 (return nil) path, so it runs bd as a subprocess with a
// title carrying escapes and asserts stdout has no raw ESC/BEL while the
// visible title text and the preview structure survive.
func TestDeletePreviewTitleSanitize_jra0(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"
	rawTitle := "Danger" + csi + osc + "Title"

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	// Create an issue whose title carries terminal-control escapes. exec.Command
	// passes the arg as raw bytes (no shell), so the escapes reach the DB intact.
	createCmd := exec.Command(bd, "create", rawTitle, "-p", "3", "--json")
	createCmd.Dir = dir
	createCmd.Env = bdEnv(dir)
	cOut, cErr, cRunErr := runCommandBuffers(t, createCmd)
	if cRunErr != nil {
		t.Fatalf("seed create failed: %v\nstdout:\n%s\nstderr:\n%s", cRunErr, cOut.String(), cErr.String())
	}
	id := extractIssueID(t, cOut.String())

	// `bd delete <id>` with no --force → DELETE PREVIEW (rc0), does not delete.
	delCmd := exec.Command(bd, "delete", id)
	delCmd.Dir = dir
	delCmd.Env = bdEnv(dir)
	dOut, dErr, dRunErr := runCommandBuffers(t, delCmd)
	if dRunErr != nil {
		t.Fatalf("`bd delete %s` (preview) failed: %v\nstdout:\n%s\nstderr:\n%s", id, dRunErr, dOut.String(), dErr.String())
	}

	out := dOut.String()
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("delete preview leaked a raw ESC (\\x1b) — title not sanitized (beads-jra0):\n%q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("delete preview leaked a raw BEL (\\x07) — title not sanitized (beads-jra0):\n%q", out)
	}
	// Visible title text must survive sanitize (escapes stripped, text kept).
	if !strings.Contains(out, "DangerTitle") {
		t.Errorf("delete preview dropped visible title text (beads-jra0):\n%q", out)
	}
	// Structural output must still render.
	for _, want := range []string{"DELETE PREVIEW", id} {
		if !strings.Contains(out, want) {
			t.Errorf("delete preview dropped structural output %q:\n%q", want, out)
		}
	}
}

// extractIssueID pulls the created issue ID from a `bd create --json` stdout.
// The payload is pretty-printed (and may be envelope-wrapped under "data"), so
// it is decoded rather than string-scanned.
func extractIssueID(t *testing.T, jsonStdout string) string {
	t.Helper()
	out := strings.TrimSpace(jsonStdout)
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("create --json output is not a JSON object: %v\n%s", err, out)
	}
	// Unwrap a possible {"data": {...}} envelope.
	if data, ok := obj["data"].(map[string]interface{}); ok {
		if _, hasID := obj["id"]; !hasID {
			obj = data
		}
	}
	id, ok := obj["id"].(string)
	if !ok || id == "" {
		t.Fatalf("could not find non-empty \"id\" in create --json output:\n%s", out)
	}
	return id
}
