package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
)

// beads-9teyf: `bd dolt set data-dir <abspath> --json` had a compound
// stderr-leak + envelope-accuracy defect.
//
// Config.Save clears an absolute DoltDataDir before writing metadata.json
// (configfile.go:115), so the value is NOT persisted there. The old code
// nonetheless (a) printed a 3-line corrective "Note:" to stderr
// UNCONDITIONALLY — polluting a --json consumer's captured stderr with
// non-JSON noise (the mfmcf/lster/80evy stderr-warning-leaks-under-json
// class) — and (b) fell through to the generic tail whose envelope claimed
// location:"metadata.json", which is FALSE for an absolute data-dir.
//
// The fix owns the data-dir emit (like the shared-server case): under --json
// it reports the accurate session-only location + persisted:false + a
// persist_hint (the guidance the human Note carries), and guards the human
// Note behind !jsonOutput. Suppressing the Note under --json is safe ONLY
// because the envelope now carries the persist guidance (the lster lesson:
// never suppress a stderr side-effect under --json unless it is
// envelope-backed).

// captureDoltSetStreams runs setDoltConfig and returns stdout and stderr
// SEPARATELY (unlike captureDoltSetOutput, which merges them) — the leak under
// test is precisely a stderr side-effect while stdout stays clean JSON.
func captureDoltSetStreams(t *testing.T, key, value string, updateConfig bool) (stdout, stderr string) {
	t.Helper()
	oldStdout, oldStderr := os.Stdout, os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout, os.Stderr = wOut, wErr

	defer func() {
		os.Stdout, os.Stderr = oldStdout, oldStderr
		if rec := recover(); rec != nil {
			// Ignore panics from os.Exit shims.
		}
	}()

	setDoltConfig(key, value, updateConfig)

	wOut.Close()
	wErr.Close()
	var bo, be bytes.Buffer
	_, _ = io.Copy(&bo, rOut)
	_, _ = io.Copy(&be, rErr)
	return bo.String(), be.String()
}

// newDataDirWorkspace creates a non-server-mode Dolt workspace (backend=dolt,
// embedded mode) so the data-dir case is reachable — the GH#2438 guard blocks
// data-dir only in SERVER mode.
func newDataDirWorkspace(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	cfg := configfile.DefaultConfig()
	cfg.Backend = configfile.BackendDolt
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatalf("save config: %v", err)
	}
	t.Setenv("BEADS_DIR", beadsDir)
	oldCwd, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })
}

func TestDoltSetDataDirJSON_NoStderrLeak_AccurateEnvelope_9teyf(t *testing.T) {
	newDataDirWorkspace(t)

	prev := jsonOutput
	t.Cleanup(func() { jsonOutput = prev })
	jsonOutput = true

	abs := filepath.Join(t.TempDir(), "faster-disk", "dolt")
	stdout, stderr := captureDoltSetStreams(t, "data-dir", abs, false)

	// (1) stderr must be clean under --json — no leaked human advisory.
	if strings.Contains(stderr, "Note:") || strings.Contains(stderr, "export BEADS_DOLT_DATA_DIR") {
		t.Errorf("data-dir advisory leaked to stderr under --json (beads-9teyf): %q — must be suppressed like the sibling paths and carried in the envelope instead", stderr)
	}

	// (2) stdout must be a single clean JSON object with an ACCURATE location.
	out := strings.TrimSpace(stdout)
	if out == "" {
		t.Fatalf("stdout empty under --json (setDoltConfig data-dir); stderr=%q", stderr)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("stdout not pure JSON: %v\nstdout:\n%s", err, out)
	}
	if result["key"] != "data-dir" {
		t.Errorf("key = %v, want data-dir", result["key"])
	}
	if result["value"] != abs {
		t.Errorf("value = %v, want %q", result["value"], abs)
	}
	// location must NOT falsely claim metadata.json — Config.Save clears an
	// absolute data-dir, so it is session-only.
	loc, _ := result["location"].(string)
	if strings.Contains(loc, "metadata.json") && !strings.Contains(loc, "not saved") {
		t.Errorf("location = %q falsely claims metadata.json (beads-9teyf): an absolute data-dir is cleared by Config.Save and never persisted there", loc)
	}
	if persisted, ok := result["persisted"].(bool); !ok || persisted {
		t.Errorf("persisted = %v (ok=%v), want false — an absolute data-dir is not written to metadata.json", result["persisted"], ok)
	}
	hint, _ := result["persist_hint"].(string)
	if !strings.Contains(hint, "BEADS_DOLT_DATA_DIR") {
		t.Errorf("persist_hint = %q, want the env-var guidance (envelope must carry what the suppressed Note said)", hint)
	}
}

func TestDoltSetDataDirHuman_StillEmitsNote_9teyf(t *testing.T) {
	newDataDirWorkspace(t)

	prev := jsonOutput
	t.Cleanup(func() { jsonOutput = prev })
	jsonOutput = false

	abs := filepath.Join(t.TempDir(), "faster-disk", "dolt")
	stdout, stderr := captureDoltSetStreams(t, "data-dir", abs, false)

	// A human run MUST still see the persistence advisory — the fix must not
	// silence the operator path, only the --json one.
	if !strings.Contains(stderr, "Note:") || !strings.Contains(stderr, "BEADS_DOLT_DATA_DIR") {
		t.Errorf("human-mode data-dir advisory missing from stderr (beads-9teyf): stderr=%q", stderr)
	}
	if !strings.Contains(stdout, abs) {
		t.Errorf("human-mode stdout should confirm the set value %q, got: %q", abs, stdout)
	}
}
