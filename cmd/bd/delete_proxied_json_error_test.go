//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedServerDeleteNoIDsJSONError is the teeth for beads-0igug: the
// PROXIED delete path must honor the fg6/65cgh JSON-error contract — a parseable
// JSON error object on STDOUT — on the no-IDs input error under --json. Before
// the fix runDeleteProxiedServer used plain FatalError on that leg, which under
// --json wrote nothing to stdout and unparseable text to stderr, breaking a
// `bd delete --json` consumer. Every other error leg already used
// FatalErrorRespectJSON, and the DIRECT path (delete.go → HandleErrorRespectJSON)
// emits the JSON object — this test locks the proxied leg to the same contract.
func TestProxiedServerDeleteNoIDsJSONError(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	p := bdProxiedInit(t, bd, "dje1")
	// `bd delete --json` with NO issue IDs hits the no-IDs leg.
	stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "delete", "--json")
	if err == nil {
		t.Fatalf("expected a non-nil error for delete with no IDs, got nil (stdout=%q stderr=%q)", stdout, stderr)
	}
	if strings.Contains(stdout+stderr, "storage is nil") {
		t.Fatalf("hit the nil-store path: %s / %s", stdout, stderr)
	}
	out := strings.TrimSpace(stdout)
	if out == "" {
		t.Fatalf("STDOUT empty on a --json error — proxied delete must emit a JSON error object on stdout (beads-0igug); stderr=%q", stderr)
	}
	var obj map[string]any
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on --json error: %v\nstdout:\n%s", jerr, out)
	}
	if _, ok := obj["error"]; !ok {
		t.Errorf("expected an \"error\" field in the --json stdout object, got: %s", out)
	}
}
