//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestRootPreRunGlobalJSONErrorContract is the teeth for beads-ci4m8: errors in
// rootCmd's PersistentPreRunE store-setup path leaked plaintext to stderr (empty
// stdout) under --json, for EVERY bd command — because those sites used a bare
// HandleError even though jsonOutput is resolved earlier in the same closure.
// The `--global requires shared-server mode` guard is the deterministic, no-DB
// repro (it fires before any store access when --global is passed outside
// shared-server mode). After the fix it must emit a JSON error object on stdout.
func TestRootPreRunGlobalJSONErrorContract(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "rp")

	// Strip any shared-server env so --global deterministically hits the
	// "requires shared-server mode" guard (embedded/local default).
	env := make([]string, 0)
	for _, e := range bdEnv(dir) {
		if strings.HasPrefix(e, "BEADS_DOLT_SHARED_SERVER") {
			continue
		}
		env = append(env, e)
	}

	cmd := exec.Command(bd, "--global", "list", "--json")
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	if err == nil {
		t.Fatalf("expected --global list --json to fail without shared-server, got rc=0\nstdout:%s\nstderr:%s", stdout.String(), stderr.String())
	}

	// The error must be a JSON object on STDOUT, not plaintext on stderr.
	s := strings.TrimSpace(stdout.String())
	start := strings.Index(s, "{")
	if start < 0 {
		t.Fatalf("root pre-run --global error emitted no JSON object on stdout (beads-ci4m8: plaintext-stderr leak)\nstdout:%q\nstderr:%q", stdout.String(), stderr.String())
	}
	var obj map[string]interface{}
	if e := json.Unmarshal([]byte(s[start:]), &obj); e != nil {
		t.Fatalf("root pre-run --global error stdout is not valid JSON: %v\n%s", e, s)
	}
	ev, ok := obj["error"]
	if !ok {
		t.Errorf("expected an \"error\" key in the JSON error object, got: %v", obj)
	}
	if es, _ := ev.(string); !strings.Contains(es, "shared-server") {
		t.Errorf("expected the error to mention 'shared-server', got: %v", ev)
	}
}
