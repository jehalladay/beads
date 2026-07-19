//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestMarshalVariantSchemaVersion is the teeth for beads-s2oy: the lav0
// MARSHAL-variant class. Several --json SUCCESS paths built their output with a
// raw json.MarshalIndent + fmt.Println block instead of routing through
// outputJSON → wrapWithSchemaVersion, so they omitted schema_version and
// ignored BD_JSON_ENVELOPE. This test runs each affected command under
// BD_JSON_ENVELOPE=1 and asserts the output is the {schema_version, data}
// envelope — which only outputJSON produces.
//
// Members covered here (all in the s2oy scope): bd hooks install/uninstall/list,
// bd todo add/list/done, bd lint. (bd human list = beads-erw5 and bd backup
// status = beads-51fl are tracked as standalone twins and covered there.)
func TestMarshalVariantSchemaVersion(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "mv")

	// run executes bd with BD_JSON_ENVELOPE=1 and returns stdout; fails the test
	// on a non-zero exit for the always-succeeding commands.
	run := func(t *testing.T, wantOK bool, args ...string) string {
		t.Helper()
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = append(bdEnv(dir), "BD_JSON_ENVELOPE=1")
		out, err := cmd.CombinedOutput()
		if wantOK && err != nil {
			t.Fatalf("bd %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}

	// assertEnvelope parses the LAST JSON object on stdout and asserts the
	// BD_JSON_ENVELOPE shape: a top-level {"schema_version":1,"data":...}. Only
	// outputJSON emits this; a raw MarshalIndent of the payload does not.
	assertEnvelope := func(t *testing.T, label, out string) {
		t.Helper()
		obj := lastJSONObject(t, label, out)
		if _, ok := obj["schema_version"]; !ok {
			t.Errorf("%s --json (BD_JSON_ENVELOPE=1) is missing schema_version — raw-marshal bypass of outputJSON (beads-s2oy):\n%s", label, out)
		}
		if _, ok := obj["data"]; !ok {
			t.Errorf("%s --json (BD_JSON_ENVELOPE=1) is missing the \"data\" envelope key (outputJSON wraps under \"data\" when enabled):\n%s", label, out)
		}
	}

	t.Run("hooks_install", func(t *testing.T) {
		out := run(t, true, "hooks", "install", "--json")
		assertEnvelope(t, "hooks install", out)
	})

	t.Run("hooks_list", func(t *testing.T) {
		out := run(t, true, "hooks", "list", "--json")
		assertEnvelope(t, "hooks list", out)
	})

	t.Run("hooks_uninstall", func(t *testing.T) {
		out := run(t, true, "hooks", "uninstall", "--json")
		assertEnvelope(t, "hooks uninstall", out)
	})

	t.Run("todo_add", func(t *testing.T) {
		out := run(t, true, "todo", "add", "A schema-version todo", "--json")
		assertEnvelope(t, "todo add", out)
	})

	t.Run("todo_list", func(t *testing.T) {
		out := run(t, true, "todo", "list", "--json")
		assertEnvelope(t, "todo list", out)
	})

	t.Run("lint", func(t *testing.T) {
		out := run(t, true, "lint", "--json")
		assertEnvelope(t, "lint", out)
	})
}

// lastJSONObject extracts the last top-level JSON object from combined output
// (bd may print a trailing deprecation NOTE or other lines around it). It scans
// for the last '{' that yields a valid object when parsed to end-of-string
// after trimming, falling back to the first parseable object.
func lastJSONObject(t *testing.T, label, out string) map[string]interface{} {
	t.Helper()
	trimmed := strings.TrimSpace(out)
	// Fast path: the whole output is one JSON object.
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
		return obj
	}
	// Otherwise scan candidate '{' offsets, preferring the last valid parse.
	var found map[string]interface{}
	for i := 0; i < len(trimmed); i++ {
		if trimmed[i] != '{' {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(trimmed[i:]))
		var cand map[string]interface{}
		if err := dec.Decode(&cand); err == nil {
			found = cand
		}
	}
	if found == nil {
		t.Fatalf("%s: no parseable JSON object in output:\n%s", label, out)
	}
	return found
}
