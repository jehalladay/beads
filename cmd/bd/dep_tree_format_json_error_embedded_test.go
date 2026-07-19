//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestEmbeddedDepTreeFormatJSONErrorContract is the beads-6i6f regression: the
// `--format json` alias must be promoted to jsonOutput BEFORE the first error
// path (resolveIDWithRouting), so a bad ID under `--format json` produces the
// same JSON error object on stdout that the global `--json` flag produces.
//
// Before the fix, dep tree resolved the ID (dep.go:1155) while jsonOutput was
// still false and only promoted --format json→jsonOutput ten lines later
// (:1166). HandleErrorRespectJSON keys off jsonOutput, so on a resolve failure
// it emitted plaintext to stderr with empty stdout — unparseable by a --json
// consumer that (reasonably) treats `--format json` and `--json` as equivalent.
//
// This asserts on SEPARATED stdout/stderr (not CombinedOutput): the whole bug
// is which fd the error lands on. The `--json` sibling below is the control —
// it already worked because main.go sets jsonOutput pre-RunE.
func TestEmbeddedDepTreeFormatJSONErrorContract(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "dje")

	// A bad ID guarantees resolveIDWithRouting fails — the first error path in
	// depTreeCmd RunE, which is exactly where the late promotion used to bite.
	const badID = "dje-nope-does-not-exist"

	assertJSONErrorOnStdout := func(t *testing.T, formatArgs ...string) {
		t.Helper()
		args := append([]string{"dep", "tree", badID}, formatArgs...)
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err == nil {
			t.Fatalf("expected `bd %s` to fail on a bad ID, but it succeeded:\nstdout:\n%s", strings.Join(args, " "), stdout.String())
		}

		so := strings.TrimSpace(stdout.String())
		if so == "" {
			t.Fatalf("`bd %s`: stdout empty on error — a --json/-format-json consumer got nothing parseable (beads-6i6f). stderr:\n%s",
				strings.Join(args, " "), stderr.String())
		}
		var obj map[string]any
		if jerr := json.Unmarshal([]byte(so), &obj); jerr != nil {
			t.Fatalf("`bd %s`: stdout is not a JSON object on error: %v\nstdout:\n%s\nstderr:\n%s",
				strings.Join(args, " "), jerr, so, stderr.String())
		}
		if _, ok := obj["error"]; !ok {
			t.Fatalf("`bd %s`: JSON error object missing an \"error\" field: %s", strings.Join(args, " "), so)
		}
	}

	// The bug path: --format json.
	t.Run("format_json_bad_id_emits_json_error_on_stdout", func(t *testing.T) {
		assertJSONErrorOnStdout(t, "--format", "json")
	})

	// The control path: the global --json flag (set pre-RunE in main.go) always
	// worked; asserting it here pins the two aliases to identical behavior so a
	// future regression that "fixes" one but not the other is caught.
	t.Run("global_json_bad_id_emits_json_error_on_stdout", func(t *testing.T) {
		assertJSONErrorOnStdout(t, "--json")
	})
}
