//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDoltShowJSONErrorEmitsStdoutObject is the beads-zczxw error-contract
// teeth (8lqh / 0bxgs FatalErrorWithHint twin). `bd dolt show/set/test/status`
// honor the persistent --json on their SUCCESS paths (outputJSON -> stdout),
// but their entry-point error legs (workspace-not-found guard + config-load
// failure) hand-rolled `FatalErrorWithHint(...)` / `Fprintf(os.Stderr) + os.Exit(1)`.
// FatalErrorWithHint (errors.go:286) routes the JSON error to os.STDERR under
// --json (via jsonStderrError), so stdout was EMPTY — the exact banned failure
// mode a --json consumer reading stdout cannot parse. The legs now route through
// FatalErrorWithHintRespectJSON / FatalErrorRespectJSON: a single
// {error, schema_version} JSON object on STDOUT, exit 1.
//
// Reachable in embedded mode: a workspace whose .beads/metadata.json is invalid
// JSON makes configfile.Load fail inside showDoltConfig, hitting the config-load
// error leg without needing a live sql-server.
func TestDoltShowJSONErrorEmitsStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd)

	// Corrupt metadata.json so configfile.Load returns a parse error, hitting
	// showDoltConfig's config-load leg.
	metaPath := filepath.Join(beadsDir, "metadata.json")
	if err := os.WriteFile(metaPath, []byte("{ this is not valid json "), 0o644); err != nil {
		t.Fatalf("corrupt metadata.json: %v", err)
	}

	cmd := exec.Command(bd, "dolt", "show", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err == nil {
		t.Fatalf("`bd dolt show --json` with corrupt metadata.json unexpectedly succeeded\nstdout:\n%s", stdout.String())
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("stdout is EMPTY on a failing `bd dolt show --json` — the error must be a JSON object on stdout (beads-zczxw; plain-text/JSON-on-stderr breaks parsers)\nstderr:\n%s", stderr.String())
	}
	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on a failing `bd dolt show --json`: %v\nstdout:\n%s", jerr, out)
	}
	msg, ok := obj["error"].(string)
	if !ok {
		if data, dok := obj["data"].(map[string]interface{}); dok {
			msg, ok = data["error"].(string)
		}
	}
	if !ok || msg == "" {
		t.Errorf("expected a non-empty \"error\" field in failing `bd dolt show --json` stdout, got: %s", out)
	}
}
