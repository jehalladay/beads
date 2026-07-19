//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// beads-b8o1: the inline --metadata handling in createCmd.RunE (create.go, the
// live single-issue path — distinct from gatherCreateInput/create_input.go which
// mrns covers) must honor the --json error contract. `bd create --json
// --metadata '<non-object>'` previously emitted plain text via HandleError; a
// --json consumer json.load-ing stdout got nothing. Sibling checks in the same
// block (invalid-JSON, @file-read) already used HandleErrorRespectJSON — this
// closes the one plain-text lapse.
func TestEmbeddedCreateMetadataJSONError(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// assertJSONErrorOnStdout requires the command to fail AND emit a parseable
	// JSON object carrying an "error" field on stdout (not plain text on stderr).
	assertJSONErrorOnStdout := func(t *testing.T, label string, out []byte, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: expected a non-nil error, got nil (out=%q)", label, out)
		}
		s := strings.TrimSpace(string(out))
		start := strings.Index(s, "{")
		if start < 0 {
			t.Fatalf("%s: no JSON object on stdout for a --json error (beads-b8o1 regression):\n%s", label, s)
		}
		var obj map[string]any
		if jerr := json.Unmarshal([]byte(s[start:]), &obj); jerr != nil {
			t.Fatalf("%s: stdout is not a JSON object on --json error: %v\nstdout:\n%s", label, jerr, s)
		}
		if _, ok := obj["error"]; !ok {
			t.Errorf("%s: expected an \"error\" field in the --json stdout object, got: %s", label, s)
		}
	}

	t.Run("metadata_array_not_object", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "mdj1")
		// Valid JSON, but an array — must be rejected as "not a JSON object",
		// and under --json that error must be a JSON object on stdout.
		out, err := bdRunWithFlockRetry(t, bd, dir, "create", "--json", "Meta array", "--metadata", "[1,2,3]")
		assertJSONErrorOnStdout(t, "metadata array", out, err)
		if strings.Contains(string(out), "storage is nil") {
			t.Fatalf("unexpected nil-store path: %s", out)
		}
	})

	t.Run("metadata_scalar_not_object", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "mdj2")
		out, err := bdRunWithFlockRetry(t, bd, dir, "create", "--json", "Meta scalar", "--metadata", "42")
		assertJSONErrorOnStdout(t, "metadata scalar", out, err)
	})
}
