//go:build cgo

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// beads-pbt8m (7n9y sink class): `bd close <gh:pr gate>` (without --force) runs
// checkGateSatisfaction → checkGHPR, which builds the reason
// "PR '<title>' is still open" where <title> is UNTRUSTED external SCM data from
// `gh pr view --json state,title`. That reason is embedded in the returned error
// and was displayed RAW at close.go:170 (reportCloseItemError) and
// close_proxied_server.go:232 — so a PR title carrying OSC/CSI escapes injected
// terminal-control sequences (OSC 0 window-title, OSC 52 clipboard) into the
// maintainer's terminal on `bd close`. ce741 fixed the `bd gate check` display
// but MISSED this close-time auto-gate-check path.
//
// End-to-end through the ACTUAL print site (NOT a re-invocation of the
// sanitizer, which would false-green — see the ce741 lesson): seed a gh:pr gate,
// shadow `gh` on PATH with a fake returning an escape-laden OPEN PR title, run
// `bd close <gate>` and assert the emitted error carries NO raw ESC/BEL while the
// visible PR title text survives. MUTATION-VERIFIED: reverting the
// ui.SanitizeForTerminal wrap at close.go:170 leaks \x1b/\x07 exactly.
func TestEmbeddedCloseGateReasonSanitize_pbt8m(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	if runtime.GOOS == "windows" {
		t.Skip("fake gh helper uses POSIX sh")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "pb")

	// A fake `gh` on PATH: `gh pr view <id> --json state,title` returns an OPEN
	// PR whose title carries an OSC window-title escape + a CSI color escape.
	// CRITICAL: the escapes must be JSON \uXXXX sequences (NOT raw control
	// bytes) — raw \x1b/\x07 inside a JSON string literal is INVALID JSON and
	// makes checkGHPR's json.Unmarshal fail (error path, no reason printed). The
	// \uXXXX form is valid JSON that Unmarshal decodes back into REAL control
	// bytes in status.Title, reproducing the attacker vector. `printf '%s'` with
	// a single-quoted literal keeps the backslashes verbatim in the emitted JSON.
	binDir := t.TempDir()
	fakeGH := filepath.Join(binDir, "gh")
	script := "#!/bin/sh\n" +
		`printf '%s' '{"state":"OPEN","title":"Fix\u001b]0;pwned\u0007\u001b[31mbug"}'` + "\n"
	if err := os.WriteFile(fakeGH, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}

	target := bdCreate(t, bd, dir, "Gate target for pbt8m", "--type", "task")

	// Create a gh:pr gate blocking the target (await PR #42). `gate create
	// --json` emits the gate issue object (gate.go:79) → reuse parseIssueJSON.
	mkGate := exec.Command(bd, "gate", "create", "--json", "--type", "gh:pr", "--blocks", target.ID, "--await-id", "42")
	mkGate.Dir = dir
	mkGate.Env = bdEnv(dir)
	gateOut, err := mkGate.Output()
	if err != nil {
		t.Fatalf("gate create failed: %v\n%s", err, gateOut)
	}
	gateID := parseIssueJSON(t, gateOut).ID
	if gateID == "" {
		t.Fatalf("could not resolve gate id from: %s", gateOut)
	}

	// `bd close <gate>` without --force → checkGateSatisfaction → checkGHPR
	// (fake gh) → gate not resolved (OPEN) → error printed. Prepend the fake gh
	// dir to PATH so checkGHPR invokes it.
	closeCmd := exec.Command(bd, "close", gateID)
	closeCmd.Dir = dir
	env := bdEnv(dir)
	for i, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			env[i] = "PATH=" + binDir + string(os.PathListSeparator) + strings.TrimPrefix(e, "PATH=")
		}
	}
	closeCmd.Env = env
	stdout, stderr, _ := runCommandBuffers(t, closeCmd)
	combined := stdout.String() + stderr.String()

	// The close is BLOCKED (OPEN gate), so the "cannot close ... gate condition
	// not satisfied: PR '...' is still open" message is emitted. It must be
	// sanitized: no raw ESC/BEL, but the visible title text survives.
	if strings.ContainsRune(combined, '\x1b') || strings.ContainsRune(combined, '\x07') {
		t.Errorf("bd close leaked a raw terminal escape from the gh:pr gate reason:\n%q", combined)
	}
	if !strings.Contains(combined, "still open") {
		t.Errorf("expected the gate-not-satisfied reason in output, got:\n%q", combined)
	}
	if !strings.Contains(combined, "Fix") || !strings.Contains(combined, "bug") {
		t.Errorf("sanitize dropped visible PR-title text (want Fix...bug):\n%q", combined)
	}
}
