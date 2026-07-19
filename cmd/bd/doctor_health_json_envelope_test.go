package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestRunServerHealthJSONEnvelope is the teeth for beads-scsf (s2oy
// MARSHAL-variant sibling): `bd doctor --server --json` built its output with a
// raw json.Marshal + fmt.Println, bypassing outputJSON → wrapWithSchemaVersion,
// so it omitted schema_version and ignored BD_JSON_ENVELOPE. Under
// BD_JSON_ENVELOPE=1 the output must be the {schema_version,data} envelope that
// only outputJSON produces.
//
// runServerHealth is exercised directly (like the doctor_health smoke test):
// it runs RunServerHealthChecks against an unreachable server (port 1) and
// still marshals a result to stdout on the --json path, which is all this test
// needs — no live Dolt server required.
func TestRunServerHealthJSONEnvelope(t *testing.T) {
	// Force an unreachable server so the checks fail fast without a real Dolt.
	t.Setenv("BEADS_DOLT_SERVER_PORT", "1")
	t.Setenv("BD_JSON_ENVELOPE", "1")

	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := `{
  "database": "beads.db",
  "dolt_mode": "server",
  "dolt_server_host": "127.0.0.1",
  "dolt_server_user": "root",
  "dolt_database": "beads"
}`
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}

	oldJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = oldJSON })

	out := captureHealthStdout(t, func() {
		// runServerHealth returns SilentExit() when checks fail (unreachable
		// server), which is expected here — we only assert the JSON shape it
		// printed to stdout before returning.
		_ = runServerHealth(tmpDir)
	})

	var obj map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(out), &obj); err != nil {
		t.Fatalf("bd doctor --server --json did not emit a parseable JSON object: %v\nraw:\n%s", err, out)
	}
	if _, ok := obj["schema_version"]; !ok {
		t.Errorf("bd doctor --server --json (BD_JSON_ENVELOPE=1) is missing schema_version — raw-marshal bypass of outputJSON (beads-scsf):\n%s", out)
	}
	if _, ok := obj["data"]; !ok {
		t.Errorf("bd doctor --server --json (BD_JSON_ENVELOPE=1) is missing the \"data\" envelope key (outputJSON wraps under \"data\" when enabled):\n%s", out)
	}
}

// captureHealthStdout runs fn with os.Stdout redirected to a pipe and returns
// what was written. Self-contained (no shared stdio mutex) — this test does not
// run in parallel with other stdout-capturing tests in the package because it
// does not call t.Parallel().
func captureHealthStdout(t *testing.T, fn func()) []byte {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	fn()
	_ = w.Close()
	os.Stdout = old
	<-done
	_ = r.Close()
	return buf.Bytes()
}
