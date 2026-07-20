//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedServerCreateJSONError is the teeth for beads-v5yu: the PROXIED
// create path must honor the fg6/mrns JSON-error contract — a parseable JSON
// error object on STDOUT — on an input/validation error under --json. Before
// the fix runCreateProxiedSingle used FatalError, which under --json writes the
// JSON error to STDERR (jsonStderrError) and leaves STDOUT empty, breaking a
// `bd create ... --json` consumer that json.loads stdout. FatalErrorRespectJSON
// puts it on stdout, matching the direct path (create.go HandleErrorRespectJSON,
// beads-mrns) and the vuyx show/update proxied fixes.
func TestProxiedServerCreateJSONError(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	assertJSONErrorOnStdout := func(t *testing.T, label, stdout, stderr string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: expected a non-nil error, got nil (stdout=%q stderr=%q)", label, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("%s: hit the nil-store path: %s / %s", label, stdout, stderr)
		}
		out := strings.TrimSpace(stdout)
		if out == "" {
			t.Fatalf("%s: STDOUT empty on a --json error — must emit a JSON error object on stdout (beads-v5yu); stderr=%q", label, stderr)
		}
		var obj map[string]any
		if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
			t.Fatalf("%s: stdout is not a JSON object on --json error: %v\nstdout:\n%s", label, jerr, out)
		}
		if _, ok := obj["error"]; !ok {
			t.Errorf("%s: expected an \"error\" field in the --json stdout object, got: %s", label, out)
		}
	}

	t.Run("invalid_type", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cje1")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "create", "Bad type", "--type", "definitely-not-a-type", "--json")
		assertJSONErrorOnStdout(t, "invalid_type", stdout, stderr, err)
	})

	t.Run("parent_not_found_dry_run", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cje2")
		// A dry-run create with a nonexistent parent hits the proxied
		// parent-lookup FatalError path.
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "create", "Orphan", "--parent", "ghost-9999", "--dry-run", "--json")
		assertJSONErrorOnStdout(t, "parent_not_found_dry_run", stdout, stderr, err)
	})

	t.Run("invalid_explicit_id", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cje3")
		// A malformed explicit --id trips ValidateIDFormat before the write.
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "create", "Bad id", "--id", "not a valid id!!", "--json")
		assertJSONErrorOnStdout(t, "invalid_explicit_id", stdout, stderr, err)
	})

	// beads-65cgh: the explicit-id existence-check block (parentID=="") must
	// keep the whole --json contract. Its 'already exists' leg
	// (FatalErrorWithHintRespectJSON) and the UOW-open/lookup-failure legs
	// (converted from plain FatalError to FatalErrorRespectJSON) must all emit
	// a JSON error object on STDOUT under --json. The reachable member of that
	// block is the duplicate-id collision (the fault-injection legs are
	// defensive), so lock its contract here — a regression that reverts the
	// block to plain FatalError would leave stdout empty and this would fail.
	t.Run("explicit_id_collision", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cje4")
		if _, _, err := bdProxiedRunBuffers(t, bd, p.dir, "create", "Original", "--id", "cje4-dup1", "--json"); err != nil {
			t.Fatalf("setup create failed: %v", err)
		}
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "create", "Clobber attempt", "--id", "cje4-dup1", "--json")
		assertJSONErrorOnStdout(t, "explicit_id_collision", stdout, stderr, err)
	})
}
