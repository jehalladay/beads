//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestCompactJSONError_EmitsStdoutObject is the end-to-end teeth for the
// compact --json error contract (beads-9fww / beads-lv51). The pure-function
// test (TestValidateCompactMode) pins the mode/tier logic, but it cannot catch
// the flag-shadow defect: `bd admin compact` registered a command-local --json
// flag bound to the global jsonOutput, which shadowed the root persistent
// --json. Cobra set jsonOutput=true from the local flag, but PersistentPreRun
// then saw root.PersistentFlags().Changed("json")==false and clobbered
// jsonOutput back to the config default (false) — so --json was silently
// non-functional and the FatalErrorRespectJSON error paths never produced
// stdout JSON. Removing the local flag makes the inherited persistent flag take
// effect.
//
// This runs `bd admin compact --analyze --tier 2 --json` as a subprocess:
// --analyze is read-only (skips requireServerMode), and validateCompactMode
// rejects tier 2 as unimplemented, routing through FatalErrorRespectJSON. Under
// --json the error must appear as a JSON object on stdout, not empty stdout.
func TestCompactJSONError_EmitsStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	cmd := exec.Command(bd, "admin", "compact", "--analyze", "--tier", "2", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err == nil {
		t.Fatalf("expected non-zero exit for `compact --analyze --tier 2 --json`, got success\nstdout:\n%s\nstderr:\n%s",
			stdout.String(), stderr.String())
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("stdout is EMPTY on compact --json error — must emit a JSON error object on stdout (json-error-contract; the local --json flag shadow was clobbering jsonOutput to false)\nstderr:\n%s",
			stderr.String())
	}

	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on compact --json error: %v\nstdout:\n%s", jerr, out)
	}
	if _, ok := obj["error"]; !ok {
		t.Errorf("expected an \"error\" field in compact --json error stdout, got: %s", out)
	}
}

// TestCompactJSONError_RequireServerModeEmitsStdoutObject is the teeth for the
// requireServerMode leg of `bd admin compact --json` (beads-t2ebq). This is the
// ONE straggler compact error path that stayed on a bare stderr FatalError
// after the 9fww sweep converted the other ~30 compact paths and broz fixed the
// sibling `bd admin reset`. In embedded mode a mutating compact
// (--force --id x, not --stats/--analyze/--dry-run) hits requireServerMode
// ("'bd admin compact' is not yet supported in embedded mode"). Before the fix
// that used FatalError → JSON went to stderr with EMPTY stdout under --json;
// after, FatalErrorRespectJSON puts the {error} object on STDOUT. Mirror of
// TestResetJSONError_RequireServerModeEmitsStdoutObject.
func TestCompactJSONError_RequireServerModeEmitsStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	cmd := exec.Command(bd, "admin", "compact", "--force", "--id", "beads-x", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err == nil {
		t.Fatalf("expected non-zero exit for `compact --force --id x --json` in embedded mode, got success\nstdout:\n%s\nstderr:\n%s",
			stdout.String(), stderr.String())
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("stdout is EMPTY on compact --json requireServerMode error — must emit a JSON error object on stdout (json-error-contract beads-t2ebq)\nstderr:\n%s",
			stderr.String())
	}

	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on compact --json requireServerMode error: %v\nstdout:\n%s", jerr, out)
	}
	msg, ok := obj["error"]
	if !ok {
		t.Fatalf("expected an \"error\" field in compact requireServerMode --json stdout, got: %s", out)
	}
	if s, _ := msg.(string); !strings.Contains(s, "embedded mode") {
		t.Errorf("expected the error to name the embedded-mode limitation, got %q", s)
	}
}
