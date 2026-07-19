//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// beads-rafd: `bd create --json --id <existing>` must honor the --json error
// contract — the "issue already exists" guard (create.go) previously used
// HandleErrorWithHint, which writes the JSON error object to STDERR under
// --json, so a consumer json.load-ing stdout got nothing. The fix routes it
// through HandleErrorWithHintRespectJSON (stdout). Sibling of the hbn3
// (purge)/z2b4 (worktree) HandleErrorWithHint-to-stderr class.
//
// This test captures stdout and stderr SEPARATELY (runCommandBuffers) so it is
// load-bearing for the stdout-vs-stderr distinction — a combined-output capture
// would pass either way and not catch a regression.
func TestEmbeddedCreateAlreadyExistsJSONError(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "aex")

	// Seed an issue with an explicit id.
	if out, err := bdRunWithFlockRetry(t, bd, dir, "create", "First", "--id", "aex-dup1"); err != nil {
		t.Fatalf("initial create failed: %v\n%s", err, out)
	}

	// Re-create the SAME id under --json — the "already exists" guard must emit
	// a JSON error object on STDOUT (not stderr).
	cmd := exec.Command(bd, "create", "--json", "Second", "--id", "aex-dup1")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err == nil {
		t.Fatalf("expected create of an existing id to fail; stdout=%s stderr=%s", stdout.String(), stderr.String())
	}

	so := strings.TrimSpace(stdout.String())
	if so == "" {
		t.Fatalf("STDOUT empty on a --json 'already exists' error — JSON went to stderr (beads-rafd regression); stderr=%s", stderr.String())
	}
	var obj map[string]any
	if jerr := json.Unmarshal([]byte(so), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on --json error: %v\nstdout:\n%s", jerr, so)
	}
	if _, ok := obj["error"]; !ok {
		t.Errorf("expected an \"error\" field in the --json stdout object, got: %s", so)
	}
	if !strings.Contains(so, "already exists") {
		t.Errorf("expected 'already exists' in the JSON error, got: %s", so)
	}
}
