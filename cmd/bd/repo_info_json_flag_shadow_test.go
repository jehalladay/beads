//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestRepoListJSON_EmitsStdoutObject and TestInfoJSON_EmitsStdoutObject are the
// end-to-end teeth for the --json flag-shadow fix (beads-06km, same root as
// beads-lv51/beads-9fww). These commands previously registered a command-local
// --json flag bound to the global jsonOutput, which shadowed the root persistent
// --json: cobra set jsonOutput=true from the local flag, but PersistentPreRun
// then saw root.PersistentFlags().Changed("json")==false and clobbered
// jsonOutput back to the config default (false). Net: `--json` was silently
// non-functional — the command printed human-readable text on stdout instead of
// a JSON object. Removing the local flag makes the inherited persistent flag
// take effect.
//
// A pure-function test cannot catch this — the defect lives in cobra flag
// plumbing across PersistentPreRun, so the teeth must run bd as a subprocess and
// assert stdout is a parseable JSON object.

func TestRepoListJSON_EmitsStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	cmd := exec.Command(bd, "repo", "list", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("`repo list --json` failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("stdout is EMPTY on `repo list --json` — must emit a JSON object on stdout (the local --json flag shadow was clobbering jsonOutput to false)\nstderr:\n%s", stderr.String())
	}

	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on `repo list --json`: %v\nstdout:\n%s\n(if this is human-readable text, the --json flag shadow has regressed)", jerr, out)
	}
	if _, ok := obj["primary"]; !ok {
		t.Errorf("expected a \"primary\" field in `repo list --json` stdout, got: %s", out)
	}
}

func TestInfoJSON_EmitsStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	cmd := exec.Command(bd, "info", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("`info --json` failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("stdout is EMPTY on `info --json` — must emit a JSON object on stdout (the local --json flag shadow was clobbering jsonOutput to false)\nstderr:\n%s", stderr.String())
	}

	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on `info --json`: %v\nstdout:\n%s\n(if this is human-readable text, the --json flag shadow has regressed)", jerr, out)
	}
}

// TestRepoJSON_ErrorPathEmitsStdoutObject is the error-contract half of the same
// fix. Removing the shadow flag (above) makes `--json` honored on the SUCCESS
// path, but repo's error paths used plain HandleError (plain text on stderr,
// EMPTY stdout) — so under --json a failure produced empty stdout, breaking
// parsers. The fix routes those errors through HandleErrorRespectJSON, matching
// the canonical honored-json commands (list/show/update/close). This asserts a
// deterministic error (`repo add` of a nonexistent workspace) emits a parseable
// JSON object with an "error" field on stdout, not plain text.
func TestRepoJSON_ErrorPathEmitsStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	cmd := exec.Command(bd, "repo", "add", "/nonexistent/path/xyz", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	// The command is expected to FAIL (nonexistent workspace) — err != nil is fine.
	if err == nil {
		t.Fatalf("`repo add /nonexistent --json` unexpectedly succeeded\nstdout:\n%s", stdout.String())
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("stdout is EMPTY on a failing `repo add --json` — the error must be emitted as a JSON object on stdout (plain-text HandleError breaks parsers)\nstderr:\n%s", stderr.String())
	}

	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on a failing `repo add --json`: %v\nstdout:\n%s", jerr, out)
	}
	// The error message lives at the top level or under a "data" envelope.
	msg, ok := obj["error"].(string)
	if !ok {
		if data, dok := obj["data"].(map[string]interface{}); dok {
			msg, ok = data["error"].(string)
		}
	}
	if !ok || msg == "" {
		t.Errorf("expected a non-empty \"error\" field in failing `repo add --json` stdout, got: %s", out)
	}
}
