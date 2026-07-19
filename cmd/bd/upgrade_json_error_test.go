//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// beads-m71ko (8lqh error-half): `bd upgrade ack` sets SilenceErrors and emits
// outputJSON on success, but its metadata load/save error paths returned a bare
// HandleError. Because SilenceErrors is set, main() prints the returned error as
// plain text "Error: <msg>" to stderr with EMPTY stdout even under --json —
// breaking a `--json` consumer doing json.load on stdout. The fix routes them
// through HandleErrorRespectJSON so --json emits a parseable JSON error object
// on stdout. A malformed .beads/metadata.json makes configfile.Load return
// "parsing config: ..." on the load path — a hermetic, reachable trigger.
func TestUpgradeAckJSONErrorContract_m71ko(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".beads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	// Malformed metadata.json → configfile.Load returns a parse error, which
	// upgrade ack's load-path guard surfaces.
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte("{ this is not json"), 0o644); err != nil {
		t.Fatalf("write metadata.json: %v", err)
	}
	t.Setenv("BEADS_DIR", dir)

	prevJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = prevJSON })

	out, err := captureStdoutExpectErr(t, func() error {
		return upgradeAckCmd.RunE(upgradeAckCmd, nil)
	})
	if err == nil {
		t.Fatalf("upgrade ack (bad metadata): expected a non-nil error, got nil (stdout=%q)", out)
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		t.Fatalf("upgrade ack (bad metadata): stdout empty on a --json error — must emit a JSON error object (beads-m71ko), err=%v", err)
	}
	var obj map[string]any
	if jerr := json.Unmarshal([]byte(trimmed), &obj); jerr != nil {
		t.Fatalf("upgrade ack (bad metadata): stdout is not a JSON object on --json error: %v\nstdout:\n%s", jerr, trimmed)
	}
	if _, ok := obj["error"]; !ok {
		t.Errorf("upgrade ack (bad metadata): expected an \"error\" field in the --json stdout object, got: %s", trimmed)
	}
}
