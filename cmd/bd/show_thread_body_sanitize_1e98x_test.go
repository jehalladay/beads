//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestShowMessageThread_SanitizesBody_1e98x is the sanitize teeth for
// beads-1e98x (i8dsb/7n9y sink-class slice) on the DIRECT path. showMessageThread
// ('bd show <id> --thread') printed each message's BODY (msg.Description) RAW —
// strings.Split on "\n" then bare fmt.Printf per line — bypassing
// ui.SanitizeForTerminal. A message body is settable by other actors (e.g. via
// 'gt mail send --stdin') / imported from JSONL verbatim, so an OSC/CSI escape
// (OSC 52 clipboard / OSC 0 window-title) reached the terminal. Distinct from
// beads-s3qhv (Subject) and beads-jxi3d (From/To). The fix sanitizes per line;
// display-only — the --json path (outputJSON(threadMessages)) stays raw.
//
// showMessageThread reads the package-global store, so per the jxi3d lesson this
// uses the buildEmbeddedBD + SUBPROCESS harness (the in-process newTestStore
// approach skips under BEADS_TEST_EMBEDDED_DOLT=1 — that path is Dolt-container-
// gated). We import a body-bearing message + reply so --thread walks the chain.
func TestShowMessageThread_SanitizesBody_1e98x(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	const csi = "\x1b[31m"
	const osc52 = "\x1b]52;c;cHduZWQ=\x07"
	// Multi-line body: the escape spans the "\n" split so per-line sanitize is required.
	evilBody := "line-one" + csi + osc52 + "line-two\nline-three" + osc52 + "END"

	run := func(t *testing.T, args ...string) string {
		t.Helper()
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bd %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	// Extract a created issue ID from `bd create` stdout ("Created issue: <id> — ...").
	createID := func(t *testing.T, out string) string {
		t.Helper()
		for _, line := range strings.Split(out, "\n") {
			if i := strings.Index(line, "Created issue: "); i >= 0 {
				rest := line[i+len("Created issue: "):]
				return strings.Fields(rest)[0]
			}
		}
		t.Fatalf("no created ID in output:\n%s", out)
		return ""
	}

	root := createID(t, run(t, "create", "Root subject", "--type", "task", "-d", evilBody))
	run(t, "create", "Reply subject", "--type", "task", "--deps", "replies-to:"+root)

	out := run(t, "show", root, "--thread")

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("show --thread leaked a raw ESC (0x1b) — message body not sanitized (beads-1e98x):\n%q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("show --thread leaked a raw BEL (0x07) — message body not sanitized (beads-1e98x):\n%q", out)
	}
	// Visible body text must survive sanitize (escapes stripped, text kept).
	if !strings.Contains(out, "line-oneline-two") {
		t.Errorf("show --thread dropped/garbled body text (beads-1e98x):\n%q", out)
	}
	if !strings.Contains(out, "line-threeEND") {
		t.Errorf("show --thread dropped/garbled second body line (beads-1e98x):\n%q", out)
	}
}
