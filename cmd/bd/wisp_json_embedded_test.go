//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestEmbeddedWispCreateJSONError proves that a `bd wisp create ... --json`
// input-validation failure emits a parseable JSON error object on stdout
// (HandleErrorRespectJSON), matching the update/close/create/list/search
// convention — not plain text to stderr with empty stdout (beads-ecde). An
// invalid --var value ("badformat", missing '=') is a deterministic error that
// fires before any proto resolution.
func TestEmbeddedWispCreateJSONError(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "wj")

	cmd := exec.Command(bd, "mol", "wisp", "create", "some-proto", "--var", "badformat", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err == nil {
		t.Errorf("expected non-zero exit on invalid --var, got success\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("stdout is empty on a --json wisp create input error — must emit a JSON error object (beads-ecde)\nstderr:\n%s", stderr.String())
	}
	var obj map[string]any
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on --json wisp create input error: %v\nstdout:\n%s", jerr, out)
	}
	if _, ok := obj["error"]; !ok {
		t.Errorf("expected an \"error\" field in the --json wisp-error stdout object, got: %s", out)
	}
}
