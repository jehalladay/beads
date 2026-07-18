//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// beads-mrns: bd create input-validation errors must honor the --json error
// contract — a parseable JSON error object on stdout (HandleErrorRespectJSON),
// matching list(8lqh)/relate(jy6w)/defer(xwjg) — not a raw HandleError that
// prints plain text to stderr (which breaks `bd create ... --json` consumers
// doing json.load on stdout).
//
// gatherCreateInput (cmd/bd/create_input.go) runs inside createCmd.RunE, AFTER
// PersistentPreRunE sets the global jsonOutput, and is reached on both the
// direct and proxied-server create paths — so every flag/arg validation error
// it emits is reachable under --json.

func TestCreateJSONErrorContract_mrns(t *testing.T) {
	prevJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = prevJSON })

	assertJSONError := func(t *testing.T, label, stdout string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: expected a non-nil error, got nil (stdout=%q)", label, stdout)
		}
		out := strings.TrimSpace(stdout)
		if out == "" {
			t.Fatalf("%s: stdout empty on a --json error — must emit a JSON error object (beads-mrns), err=%v", label, err)
		}
		var obj map[string]any
		if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
			t.Fatalf("%s: stdout is not a JSON object on --json error: %v\nstdout:\n%s", label, jerr, out)
		}
		if _, ok := obj["error"]; !ok {
			t.Errorf("%s: expected an \"error\" field in the --json stdout object, got: %s", label, out)
		}
	}

	// Each case drives a distinct pre-store validation error path in
	// gatherCreateInput by setting flags on the real createCmd (its flag set is
	// registered in init()). Flags are reset to defaults after each case so
	// state does not bleed. None of these paths need a live store.
	setFlag := func(t *testing.T, name, val string) {
		t.Helper()
		if err := createCmd.Flags().Set(name, val); err != nil {
			t.Fatalf("set --%s=%s: %v", name, val, err)
		}
		t.Cleanup(func() { _ = createCmd.Flags().Set(name, defaultCreateFlag(name)) })
	}

	t.Run("file_and_graph_conflict", func(t *testing.T) {
		setFlag(t, "file", "a.md")
		setFlag(t, "graph", "b.json")
		out, err := captureStdoutExpectErr(t, func() error {
			_, e := gatherCreateInput(createCmd, nil)
			return e
		})
		assertJSONError(t, "file+graph", out, err)
	})

	t.Run("ephemeral_and_no_history_conflict", func(t *testing.T) {
		setFlag(t, "ephemeral", "true")
		setFlag(t, "no-history", "true")
		out, err := captureStdoutExpectErr(t, func() error {
			_, e := gatherCreateInput(createCmd, []string{"a title"})
			return e
		})
		assertJSONError(t, "ephemeral+no-history", out, err)
	})

	t.Run("invalid_mol_type", func(t *testing.T) {
		setFlag(t, "mol-type", "bogus")
		out, err := captureStdoutExpectErr(t, func() error {
			_, e := gatherCreateInput(createCmd, []string{"a title"})
			return e
		})
		assertJSONError(t, "invalid mol-type", out, err)
	})

	t.Run("invalid_due_format", func(t *testing.T) {
		setFlag(t, "due", "not-a-date")
		out, err := captureStdoutExpectErr(t, func() error {
			_, e := gatherCreateInput(createCmd, []string{"a title"})
			return e
		})
		assertJSONError(t, "invalid due", out, err)
	})
}

// defaultCreateFlag returns the zero/default value a create flag resets to
// after a test case mutates it, keeping the shared createCmd flag set clean.
func defaultCreateFlag(name string) string {
	switch name {
	case "ephemeral", "no-history":
		return "false"
	default:
		return ""
	}
}
