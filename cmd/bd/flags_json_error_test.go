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

// TestCreateJSON_BodyFlagErrorEmitsStdoutObject is the error-contract teeth for
// beads-7zh1h. The shared body/description parser getDescriptionFlag (flags.go)
// returned its flag-validation errors via plain HandleError — plain text on
// stderr, EMPTY stdout — so under the persistent --json a body/description flag
// error (e.g. the mutually-exclusive --body-file + --description guard) produced
// empty stdout, breaking JSON parsers. getDescriptionFlag is the SHARED parser
// used by the write verbs `bd create` and `bd update` (create.go/update.go),
// both of which honor --json on their success path, so the asymmetry is a real
// contract violation on the create/update error path. Same class as beads-rg0c
// (audit.go): the fix routes those sites through HandleErrorRespectJSON.
//
// The mutually-exclusive body-file+description guard fires during flag handling
// BEFORE any store mutation, so it is a deterministic, server-free error path.
// The defect lives in cobra's RunE error return + JSON emission, so the teeth
// run bd as a subprocess and assert stdout is a parseable JSON object with an
// "error" field.
func TestCreateJSON_BodyFlagErrorEmitsStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	// Seed a file so --body-file resolves to a real path; the error we want is
	// the mutual-exclusivity of --body-file and --description, not a read error.
	bodyFile := filepath.Join(dir, "body.txt")
	if err := os.WriteFile(bodyFile, []byte("some body"), 0o644); err != nil {
		t.Fatalf("write body file: %v", err)
	}

	cmd := exec.Command(bd, "create", "A title", "--json", "--body-file", bodyFile, "--description", "conflicting desc")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	// Expected to FAIL (body-file + description are mutually exclusive).
	if err == nil {
		t.Fatalf("`create --json --body-file X --description Y` unexpectedly succeeded\nstdout:\n%s", stdout.String())
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("stdout is EMPTY on a failing `create --json` body-flag error — the error must be emitted as a JSON object on stdout (plain-text HandleError breaks parsers)\nstderr:\n%s", stderr.String())
	}

	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on a failing `create --json` body-flag error: %v\nstdout:\n%s", jerr, out)
	}
	msg, ok := obj["error"].(string)
	if !ok {
		if data, dok := obj["data"].(map[string]interface{}); dok {
			msg, ok = data["error"].(string)
		}
	}
	if !ok || msg == "" {
		t.Errorf("expected a non-empty \"error\" field in failing `create --json` body-flag error stdout, got: %s", out)
	}
}
