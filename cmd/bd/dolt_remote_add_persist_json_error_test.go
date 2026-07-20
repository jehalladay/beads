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

// TestDoltRemoteAddPersistJSONErrorEmitsStdoutObject is the beads-h64j9 teeth
// (an uncovered member of the beads-0bxgs --json error-contract sweep). `bd dolt
// remote add origin <url> --json` honors --json on its success path (outputJSON
// -> stdout) and on its primary "adding remote" error leg (FatalErrorRespectJSON),
// but the SECONDARY origin-only leg that persists sync.remote to config.yaml
// (dolt.go: `config.SetYamlConfig("sync.remote", url)`) used plain FatalError,
// which under --json writes its JSON error to STDERR (jsonStderrError) instead of
// STDOUT — the exact banned failure mode a --json consumer reading stdout cannot
// parse. It now routes through FatalErrorRespectJSON like its siblings.
//
// Trigger: add `origin` (so the persist branch runs) with config.yaml made
// read-only after init, so the dolt-layer AddRemote succeeds but the config.yaml
// write fails. Assert the error is a JSON object on STDOUT, exit != 0.
func TestDoltRemoteAddPersistJSONErrorEmitsStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd)

	// Force the sync.remote persist to fail without touching the dolt layer:
	// make .beads/config.yaml read-only (non-root test process). The remote add
	// succeeds at the SQL server, then SetYamlConfig's os.WriteFile fails.
	configPath := filepath.Join(beadsDir, "config.yaml")
	if err := os.Chmod(configPath, 0o400); err != nil {
		t.Fatalf("chmod config.yaml read-only: %v", err)
	}
	// Best-effort restore so t.TempDir cleanup can remove the file.
	t.Cleanup(func() { _ = os.Chmod(configPath, 0o600) })

	cmd := exec.Command(bd, "dolt", "remote", "add", "origin", "https://doltremoteapi.dolthub.com/org/repo", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err == nil {
		t.Fatalf("`bd dolt remote add origin <url> --json` with a read-only config.yaml unexpectedly succeeded\nstdout:\n%s", stdout.String())
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("stdout is EMPTY on a failing `bd dolt remote add origin --json` persist leg — the error must be a JSON object on stdout (beads-h64j9; JSON-on-stderr breaks parsers)\nstderr:\n%s", stderr.String())
	}
	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on the failing persist leg: %v\nstdout:\n%s", jerr, out)
	}
	msg, ok := obj["error"].(string)
	if !ok {
		if data, dok := obj["data"].(map[string]interface{}); dok {
			msg, ok = data["error"].(string)
		}
	}
	if !ok || msg == "" {
		t.Errorf("expected a non-empty \"error\" field in the failing persist-leg stdout, got: %s", out)
	}
	if !strings.Contains(msg, "sync.remote") {
		t.Errorf("expected the persist-failure error to mention sync.remote, got: %q", msg)
	}
}
