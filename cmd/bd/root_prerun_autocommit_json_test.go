//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestRootPreRunAutoCommitJSONErrorContract is the teeth for beads-63wku, the
// completeness sibling of beads-ci4m8. ci4m8 converted the store-setup legs of
// rootCmd's PersistentPreRunE (--global / uow-open / db-open / dolt-auto-commit)
// to HandleErrorRespectJSON, but left three same-closure legs on a bare
// HandleError: the two getDoltAutoCommitMode() error returns and the
// refuseMetadatalessRemoteServer refusal. jsonOutput is resolved earlier in the
// same closure, so under --json those leaked plaintext to stderr with EMPTY
// stdout for every command.
//
// The `--dolt-auto-commit=<bogus>` value is the deterministic repro: it fails
// getDoltAutoCommitMode() inside the PersistentPreRunE (in an initialized
// workspace, so it reaches that leg rather than the no-db guard). After the fix
// it must emit a JSON error object on stdout.
func TestRootPreRunAutoCommitJSONErrorContract(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ac")

	cmd := exec.Command(bd, "list", "--dolt-auto-commit=bogus", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	if err == nil {
		t.Fatalf("expected `list --dolt-auto-commit=bogus --json` to fail, got rc=0\nstdout:%s\nstderr:%s", stdout.String(), stderr.String())
	}

	s := strings.TrimSpace(stdout.String())
	start := strings.Index(s, "{")
	if start < 0 {
		t.Fatalf("root pre-run --dolt-auto-commit error emitted no JSON object on stdout (beads-63wku: plaintext-stderr leak)\nstdout:%q\nstderr:%q", stdout.String(), stderr.String())
	}
	var obj map[string]interface{}
	if e := json.Unmarshal([]byte(s[start:]), &obj); e != nil {
		t.Fatalf("root pre-run --dolt-auto-commit error stdout is not valid JSON: %v\n%s", e, s)
	}
	ev, ok := obj["error"]
	if !ok {
		t.Errorf("expected an \"error\" key in the JSON error object, got: %v", obj)
	}
	if es, _ := ev.(string); !strings.Contains(es, "dolt-auto-commit") {
		t.Errorf("expected the error to mention 'dolt-auto-commit', got: %v", ev)
	}
}
