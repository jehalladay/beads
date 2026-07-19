//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestCreateFormJSONErrorEmitsStdoutObject is the beads-csgk error-contract
// teeth (8lqh / 0wp9 / jial / 2yhq class). `bd create-form` honors the
// persistent --json on its success path (outputJSON at create_form.go:423) but
// two error paths used a bare HandleError:
//   - :412 form.Run() failure ("form error: %v")
//   - :420 CreateIssueFromFormValues failure ("%v")
//
// Under --json (SilenceErrors) those printed plain text to stderr with an EMPTY
// stdout, unparseable by a consumer — while the sibling `bd create` routes every
// error through HandleErrorRespectJSON (create.go:60-169). They now route through
// HandleErrorRespectJSON too.
//
// The :412 path has a DETERMINISTIC, fast trigger: `bd create-form --json` with
// a non-TTY stdin makes huh/bubbletea fail to open /dev/tty, so form.Run()
// errors immediately (not ErrUserAborted). No interactive input is needed.
func TestCreateFormJSONErrorEmitsStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	// create-form --json in a non-TTY: form.Run() fails to open the TTY, hitting
	// the create_form.go:412 error path. Stdin is empty (/dev/null equivalent).
	cmd := exec.Command(bd, "create-form", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	cmd.Stdin = strings.NewReader("")
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err == nil {
		t.Fatalf("`bd create-form --json` in a non-TTY unexpectedly succeeded\nstdout:\n%s", stdout.String())
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("beads-csgk: stdout is EMPTY on a failing `bd create-form --json` "+
			"(bare HandleError instead of HandleErrorRespectJSON) — the error must be a "+
			"JSON object on stdout; plain-text stderr breaks parsers.\nstderr:\n%s", stderr.String())
	}
	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("beads-csgk: stdout is not a single parseable JSON doc: %v\nstdout:\n%s", jerr, out)
	}
	// Accept both non-envelope ({"error": ...}) and envelope ({"data": {"error": ...}}) shapes.
	msg, ok := obj["error"].(string)
	if !ok {
		if data, dok := obj["data"].(map[string]interface{}); dok {
			msg, ok = data["error"].(string)
		}
	}
	if !ok || msg == "" {
		t.Errorf("beads-csgk: expected a non-empty \"error\" field in the --json error doc, got: %s", out)
	}
}
