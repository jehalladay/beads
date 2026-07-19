//go:build cgo

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestBootstrapJSONExecuteFailureRespectsContract is the teeth for beads-pq1m2.
//
// `bd bootstrap` supports --json (it emits a BootstrapPlan via outputJSON). But
// its terminal error return for a failed executeBootstrapPlan used a bare
// HandleError, which prints a multi-line plain-text "Error: Bootstrap failed:
// ..." to stderr with rc=1 — breaking the --json error contract (8lqh class): a
// --json consumer got a JSON plan on stdout but a non-JSON error on stderr.
//
// This drives a deterministic execute failure: a pure-Go (CGO_ENABLED=0) bd
// with an embedded-mode workspace resolves action=init, emits the plan JSON,
// then fails to create the embedded database ("embedded Dolt requires a CGO
// build"). The fix routes that failure through jsonStderrError under --json, so
// stderr must contain a parseable JSON error object (matching defer.go leg-B),
// while stdout stays a single parseable plan document.
func TestBootstrapJSONExecuteFailureRespectsContract(t *testing.T) {
	// Build bd with CGO_ENABLED=0 so the embedded-init execute step fails
	// deterministically ("embedded Dolt requires a CGO build") — exactly the
	// terminal-error path beads-pq1m2 fixes. buildEmbeddedBD is unusable here:
	// it inherits the ambient CGO setting, so under a cgo test run it produces a
	// bd WITH embedded support and init would succeed (skipping the negative
	// path). No embedded server or external dolt is needed for this assertion.
	bd := buildPureGoBDNoCGO(t)

	dir := t.TempDir()
	initGitRepoAt(t, dir)
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Minimal metadata → default embedded mode → detectBootstrapAction = "init".
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bd, "bootstrap", "--json", "--yes")
	cmd.Dir = dir
	// Scrub inherited BD_/BEADS_ env so the workspace resolves to our tmp dir.
	env := []string{}
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "BD_") || strings.HasPrefix(e, "BEADS_") {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	// The execute step must have failed (rc=1). If a future CGO/embedded build
	// makes init succeed here, the negative path is untested — surface that.
	if runErr == nil {
		t.Skipf("bootstrap unexpectedly succeeded (embedded init worked); negative path not exercised.\nstdout:\n%s", stdout.String())
	}

	// stdout must still be a single parseable JSON document (the plan). A second
	// JSON doc appended to stdout would itself break the contract.
	so := bytes.TrimSpace(stdout.Bytes())
	var plan map[string]interface{}
	dec := json.NewDecoder(bytes.NewReader(so))
	if err := dec.Decode(&plan); err != nil {
		t.Fatalf("stdout is not a parseable JSON plan document: %v\nstdout:\n%s", err, so)
	}
	if dec.More() {
		t.Fatalf("stdout has more than one JSON document under --json (contract break)\nstdout:\n%s", so)
	}

	// The failure must appear on stderr as a parseable JSON error object — not a
	// bare plain-text "Error: Bootstrap failed: ..." line. There may be
	// unrelated plain-text warnings on stderr (e.g. a permissions hint), so scan
	// for a JSON object carrying the "Bootstrap failed" message.
	if strings.Contains(stderr.String(), "Error: Bootstrap failed") {
		t.Fatalf("bootstrap --json emitted a bare plain-text 'Error: Bootstrap failed' on stderr (beads-pq1m2 regression):\n%s", stderr.String())
	}
	found := false
	sc := bufio.NewScanner(bytes.NewReader(stderr.Bytes()))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	// The JSON error is pretty-printed (multi-line), so accumulate the tail and
	// try to decode a trailing JSON object.
	var tail bytes.Buffer
	for sc.Scan() {
		line := sc.Text()
		tail.WriteString(line)
		tail.WriteByte('\n')
		trimmed := strings.TrimSpace(tail.String())
		if !strings.HasPrefix(trimmed, "{") {
			// Reset accumulation until a JSON object begins.
			tail.Reset()
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
			msg := errMessageFromJSONObj(obj)
			if strings.Contains(strings.ToLower(msg), "bootstrap failed") {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatalf("bootstrap --json execute failure did not emit a parseable JSON {error} object with 'Bootstrap failed' on stderr (beads-pq1m2)\nstderr:\n%s", stderr.String())
	}
}

// buildPureGoBDNoCGO builds a bd binary with CGO_ENABLED=0 (and the pure-Go
// build tag) so embedded-Dolt operations fail loudly at runtime — the
// deterministic trigger for bootstrap's execute-failure error path. Distinct
// from buildEmbeddedBD, which inherits the ambient CGO setting.
func buildPureGoBDNoCGO(t *testing.T) string {
	t.Helper()
	name := "bd"
	if runtime.GOOS == "windows" {
		name = "bd.exe"
	}
	exe := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-tags", "gms_pure_go", "-o", exe, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build pure-Go (CGO_ENABLED=0) bd: %v\n%s", err, out)
	}
	return exe
}

// errMessageFromJSONObj extracts the error message from either a flat
// {"error":...} shape or the enveloped {"data":{"error":...}} shape.
func errMessageFromJSONObj(obj map[string]interface{}) string {
	if m, ok := obj["error"].(string); ok && m != "" {
		return m
	}
	if data, ok := obj["data"].(map[string]interface{}); ok {
		if m, ok := data["error"].(string); ok {
			return m
		}
	}
	return ""
}
