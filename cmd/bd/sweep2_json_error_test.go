//go:build cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// beads-wl0s: SWEEP2 of the --json error-contract class. These commands honor
// the persistent --json on their success paths (they have outputJSON blocks)
// but their error paths used plain HandleError/HandleErrorWithHint — plain text
// on stderr, EMPTY stdout — so under --json a failure produced empty stdout,
// breaking JSON parsers. Same contract half as beads-jjuv/06km/lv51/9fww/rg0c,
// WITHOUT a flag-shadow (none of these register a command-local --json flag).
// The fix routes the error paths through HandleErrorRespectJSON, matching the
// canonical honored-json commands (list/show/update/close).
//
// The defect lives in cobra's RunE error return + PersistentPostRun JSON
// emission, so the teeth run bd as a subprocess and assert stdout is a
// parseable JSON object with a non-empty "error" field. A pure-function test
// cannot catch it. Each command below uses a deterministic, server-free error
// path so the teeth are hermetic.

// assertJSONErrorOnStdout is the shared assertion: the command must have failed,
// and its stdout must be a JSON object with a non-empty "error" field (top level
// or under a "data" envelope). Empty/plain stdout is the bug.
func assertJSONErrorOnStdout(t *testing.T, label string, stdout, stderr strings.Builder, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("`%s` unexpectedly succeeded\nstdout:\n%s", label, stdout.String())
	}
	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("stdout is EMPTY on a failing `%s` — the error must be emitted as a JSON object on stdout (plain-text HandleError breaks parsers)\nstderr:\n%s", label, stderr.String())
	}
	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on a failing `%s`: %v\nstdout:\n%s", label, jerr, out)
	}
	msg, ok := obj["error"].(string)
	if !ok {
		if data, dok := obj["data"].(map[string]interface{}); dok {
			msg, ok = data["error"].(string)
		}
	}
	if !ok || msg == "" {
		t.Errorf("expected a non-empty \"error\" field in failing `%s` stdout, got: %s", label, out)
	}
}

func runBDErr(t *testing.T, bd, dir string, args ...string) (stdoutStr, stderrStr strings.Builder, err error) {
	t.Helper()
	cmd := exec.Command(bd, args...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	sout, serr, e := runCommandBuffers(t, cmd)
	stdoutStr.WriteString(sout.String())
	stderrStr.WriteString(serr.String())
	return stdoutStr, stderrStr, e
}

// `bd admin cleanup --json` in embedded mode hits requireServerMode("cleanup")
// (cleanup.go:62) before any store access — a deterministic server-free error.
func TestAdminCleanupJSON_ErrorPathEmitsStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)
	stdout, stderr, err := runBDErr(t, bd, dir, "admin", "cleanup", "--json")
	assertJSONErrorOnStdout(t, "admin cleanup --json", stdout, stderr, err)
}

// `bd cook <nonexistent.formula.json> --json` fails in loadAndResolveFormula
// (cook.go) before any DB write — a deterministic server-free error.
func TestCookJSON_ErrorPathEmitsStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)
	stdout, stderr, err := runBDErr(t, bd, dir, "cook", "no_such_formula_xyz.formula.json", "--json")
	assertJSONErrorOnStdout(t, "cook <missing> --json", stdout, stderr, err)
}

// `bd mol pour <bogus-id> --json` fails resolving the proto/formula ID
// (pour.go:122) before any spawn — a deterministic server-free error.
func TestMolPourJSON_ErrorPathEmitsStdoutObject(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)
	stdout, stderr, err := runBDErr(t, bd, dir, "mol", "pour", "no_such_proto_xyz", "--json")
	assertJSONErrorOnStdout(t, "mol pour <bogus> --json", stdout, stderr, err)
}
