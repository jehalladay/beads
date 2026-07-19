//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// beads-9c0o: `bd update <valid-id> --<field> <invalid-value> --json` where the
// update fails on a field-validation error (e.g. invalid issue type) must
// surface the REAL reason on stdout, not the generic "no issues updated
// matching the provided IDs" — which misleads a consumer into thinking the ID
// did not exist. The all-failed --json path (update.go) now joins the captured
// deferredItemErrors instead of emitting the generic string. Preserves the
// beads-92tz one-object contract (single JSON object on stdout, clean stderr).
func TestEmbeddedUpdateAllFailedJSONSurfacesRealError(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt update tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ur")

	issue := bdCreate(t, bd, dir, "real-error target")

	// Valid ID, but an invalid --type value: the update fails on type
	// validation, not on a missing ID.
	cmd := exec.Command(bd, "update", issue.ID, "--type", "bogustype", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err == nil {
		t.Fatalf("expected non-zero exit for invalid --type; stdout=%s stderr=%s", stdout.String(), stderr.String())
	}

	out := strings.TrimSpace(stdout.String())
	var obj map[string]any
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("stdout is not a single JSON object: %v\nstdout:\n%s", jerr, out)
	}
	errVal, _ := obj["error"].(string)
	if errVal == "" {
		t.Fatalf("expected a non-empty \"error\" field, got: %s", out)
	}
	// The real reason must be surfaced — NOT the generic no-match message
	// (beads-9c0o regression: the invalid-type reason was masked).
	if strings.Contains(errVal, "no issues updated matching the provided IDs") {
		t.Errorf("--json error masks the real reason with the generic no-match message (beads-9c0o): %q", errVal)
	}
	if !strings.Contains(errVal, "invalid issue type") {
		t.Errorf("expected the real 'invalid issue type' reason in the --json error, got: %q", errVal)
	}
	// beads-92tz: stderr must not carry a competing JSON error object.
	errStr := strings.TrimSpace(stderr.String())
	if errStr != "" && json.Valid([]byte(errStr)) {
		t.Errorf("stderr must be clean of a competing JSON object (beads-92tz); got:\n%s", errStr)
	}
}
