//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestGateCheckReasonSanitize_7n9y is the sanitize teeth for the gate-check
// reason sink (7n9y sink-class). `bd gate check` prints per-gate progress lines
// ("resolved - <reason>", "ESCALATE - <reason>", "pending - <reason>") to the
// terminal. For a gh:pr gate the reason embeds the PR TITLE returned by
// `gh pr view --json state,title` — an UNTRUSTED external string that can carry
// OSC/CSI terminal-control escapes (OSC 0 window-title, OSC 52 clipboard). The
// print sites previously rendered r.reason RAW, so a hostile PR title would
// inject control sequences into the operator's terminal.
//
// End-to-end teeth exercising the ACTUAL print site: seed a gh:pr gate, run
// `bd gate check --dry-run` with a fake `gh` on PATH that returns a MERGED PR
// whose title carries escapes, and assert the human progress line ("would
// resolve - ...") reaches stdout with NO raw ESC/BEL while the visible text and
// framing survive. --dry-run is used so no close/commit side-effect runs; the
// print path is identical (both branches route r.reason through displayTitle).
func TestGateCheckReasonSanitize_7n9y(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake gh shell script uses POSIX sh")
	}
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	// A hostile PR title carrying a CSI color escape and an OSC window-title-set
	// (terminated by BEL). json.Marshal emits the control bytes as valid \uXXXX
	// JSON escapes, so the fake gh prints well-formed JSON that checkGHPR's
	// json.Unmarshal decodes back into real control bytes in status.Title.
	const csi = "\x1b[31m"
	const osc = "\x1b]0;pwned\x07"
	rawTitle := "Danger" + csi + osc + "Title"
	ghJSON, err := json.Marshal(map[string]string{"state": "MERGED", "title": rawTitle})
	if err != nil {
		t.Fatalf("marshal fake gh output: %v", err)
	}

	binDir := t.TempDir()
	fakeGH := filepath.Join(binDir, "gh")
	// Single-quote the JSON in the shell so the shell performs no expansion.
	script := "#!/bin/sh\nprintf '%s' '" + string(ghJSON) + "'\n"
	if err := os.WriteFile(fakeGH, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}

	// Seed a task + a gh:pr gate blocking it (await-id 3488 → the fake gh's PR).
	task := bdCreate(t, bd, dir, "Task gated by PR", "--type", "task")
	bdGate(t, bd, dir, "create", "--type", "gh:pr", "--await-id", "3488", "--blocks", task.ID)

	// Run `bd gate check --dry-run` with the fake gh prepended to PATH so the
	// gate resolves against the hostile PR title and prints the progress line.
	env := bdEnv(dir)
	env = prependPATHForGate(env, binDir)
	cmd := exec.Command(bd, "gate", "check", "--dry-run")
	cmd.Dir = dir
	cmd.Env = env
	out, cerr := cmd.CombinedOutput()
	if cerr != nil {
		t.Fatalf("bd gate check --dry-run failed: %v\n%s", cerr, out)
	}
	s := string(out)

	// The hostile escapes must NOT reach the terminal.
	if strings.ContainsRune(s, '\x1b') {
		t.Errorf("gate check leaked a raw ESC (\\x1b) — reason (PR title) not sanitized for display:\n%q", s)
	}
	if strings.ContainsRune(s, '\x07') {
		t.Errorf("gate check leaked a raw BEL (\\x07) — reason (PR title) not sanitized for display:\n%q", s)
	}
	// The gate must have resolved (proves the fake gh + gh:pr path ran) and the
	// visible title text must survive sanitize (escapes stripped, text kept).
	if !strings.Contains(s, "would resolve") {
		t.Errorf("expected the gate to resolve via the fake gh:pr (dry-run); got:\n%q", s)
	}
	if !strings.Contains(s, "DangerTitle") {
		t.Errorf("gate check dropped the visible PR-title text from the reason:\n%q", s)
	}
}

// prependPATHForGate returns env with dir prepended to the PATH entry so a fake
// `gh` binary in dir shadows any real one for the gate-check subprocess.
func prependPATHForGate(env []string, dir string) []string {
	out := make([]string, 0, len(env))
	found := false
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			out = append(out, "PATH="+dir+string(os.PathListSeparator)+strings.TrimPrefix(e, "PATH="))
			found = true
			continue
		}
		out = append(out, e)
	}
	if !found {
		out = append(out, "PATH="+dir)
	}
	return out
}
