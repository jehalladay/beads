//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestUnknownSubcommandJSON_EmitsStdoutErrorObject is the end-to-end tooth for
// beads-dthi. The shared attachUnknownSubcommandGuards RunE previously returned
// a bare fmt.Errorf on an unknown subcommand of a pure parent group (e.g. `bd
// label bogus`): under --json that produced rc=1 with an EMPTY stdout and a
// PLAINTEXT "Error: unknown ... subcommand" on stderr, breaking any parser that
// reads the stdout JSON contract. The fix routes it through
// HandleErrorRespectJSON so under --json the error is a structured
// {error,schema_version} object on stdout.
//
// This cannot be a pure test: the defect lives in cobra flag plumbing — the
// root PersistentPreRunE must set the jsonOutput global from the --json flag
// BEFORE the guard RunE fires — so the teeth run real bd as a subprocess and
// assert stdout is a parseable JSON error object.
func TestUnknownSubcommandJSON_EmitsStdoutErrorObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	// `label` is a pure parent group (no RunE of its own), so `label bogustypo`
	// hits the shared unknown-subcommand guard.
	cmd := exec.Command(bd, "label", "bogustypo", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)

	// The command must FAIL (non-zero exit) — the guard's whole purpose is to
	// stop silently exiting 0 on a typo'd subcommand.
	if err == nil {
		t.Fatalf("`label bogustypo --json` unexpectedly succeeded (exit 0)\nstdout:\n%s", stdout.String())
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("stdout is EMPTY on `label bogustypo --json` — the error must be a JSON object on stdout, not plaintext on stderr (beads-dthi)\nstderr:\n%s", stderr.String())
	}

	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on `label bogustypo --json`: %v\nstdout:\n%s\n(if this is human-readable text, the bare fmt.Errorf plaintext leak has regressed)", jerr, out)
	}

	// The error message lives at the top level or under a "data" envelope,
	// matching the canonical honored-json commands.
	msg, ok := obj["error"].(string)
	if !ok {
		if data, dok := obj["data"].(map[string]interface{}); dok {
			msg, ok = data["error"].(string)
		}
	}
	if !ok || msg == "" {
		t.Fatalf("expected a non-empty \"error\" field in `label bogustypo --json` stdout, got: %s", out)
	}
	if !strings.Contains(msg, "unknown") || !strings.Contains(msg, "bogustypo") {
		t.Errorf("error message should name the unknown subcommand; got: %q", msg)
	}
}
