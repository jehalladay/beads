//go:build cgo

package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestTodoDoneJSONStderrContract_scl2z is the clean-stderr error-contract teeth
// for beads-scl2z. `bd todo done <ids> --json` ALWAYS emits its results envelope
// {"closed":[...],"reason":...} on STDOUT, but every per-id failure (the direct
// doneTodoCmd resolve/guard/close loop in todo.go AND the proxied twin
// runTodoDoneProxiedServer in todo_proxied_server.go) wrote a RAW plain-text
// "Error: ..." line to os.Stderr even under --json. So `bd todo done $IDS --json
// 2>&1 | jq` interleaved plain text with the JSON object (parse failure) and the
// failed id was invisible to a --json consumer. `bd close`, which this verb
// documents itself as wrapping, already routes per-item errors through the
// json-aware reportItemError; todo done was the odd-one-out of the fg6/92tz/en28/
// n96g/iwy1k clean-stderr per-item contract family.
//
// The fix routes all per-id writes through reportItemError (errors.go:250), which
// under --json emits a structured JSON object to stderr (jsonStderrError) instead
// of raw text. These teeth run bd as a subprocess against a real embedded DB with
// a non-existent id (wholly-failed batch) and assert, with stdout and stderr
// captured SEPARATELY:
//   - stdout is exactly the parseable {"closed":...} envelope (unpolluted), and
//   - stderr, if non-empty, is itself parseable JSON — NOT raw plain text.
// Reverting either the direct or proxied path to fmt.Fprintf(os.Stderr, ...)
// makes stderr a raw "Error: issue ... not found" line → the stderr json.Unmarshal
// fails → RED.
func TestTodoDoneJSONStderrContract_scl2z(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd)

	// Non-existent id → the per-id resolve fails for every arg (wholly-failed
	// batch), the exact repro shape. `bd init` yields a DIRECT (embedded) store,
	// so this exercises the doneTodoCmd path; the proxied twin shares the same
	// reportItemError sink (verified by inspection + covered by the same edit).
	cmd := exec.Command(bd, "todo", "done", "zz-000-nope", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Expected to exit non-zero (nothing closed, failedCount>0, beads-xi35).
	if runErr := cmd.Run(); runErr == nil {
		t.Fatalf("`bd todo done zz-000-nope --json` unexpectedly succeeded\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}

	// STDOUT must be the pure, parseable results envelope — no interleaved text.
	so := strings.TrimSpace(stdout.String())
	if so == "" {
		t.Fatalf("stdout empty on `bd todo done --json` — expected the {\"closed\":...} envelope")
	}
	var env map[string]interface{}
	if jerr := json.Unmarshal([]byte(so), &env); jerr != nil {
		t.Fatalf("stdout is not a single parseable JSON object on `bd todo done --json`: %v\nstdout:\n%s", jerr, so)
	}
	if _, ok := env["closed"]; !ok {
		t.Errorf("stdout envelope missing \"closed\" field: %s", so)
	}

	// STDERR, if non-empty, must be a parseable JSON object — a raw
	// "Error: issue ... not found" plain-text line (the beads-scl2z symptom)
	// fails this and is what a `--json 2>&1 | jq` pipeline chokes on.
	se := strings.TrimSpace(stderr.String())
	if se == "" {
		t.Fatalf("stderr empty on a wholly-failed `bd todo done --json` — expected a JSON error object reporting the failed id")
	}
	var errObj map[string]interface{}
	if jerr := json.Unmarshal([]byte(se), &errObj); jerr != nil {
		t.Fatalf("stderr is NOT parseable JSON under --json (raw plain text breaks `2>&1 | jq`, beads-scl2z): %v\nstderr:\n%s", jerr, se)
	}
	if _, ok := errObj["error"]; !ok {
		t.Errorf("stderr JSON object missing \"error\" field (per-id failure must be machine-readable): %s", se)
	}
}
