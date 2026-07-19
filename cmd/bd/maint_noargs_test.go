//go:build cgo

package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestMaintNoArgsRejectsPositional pins the guard that keeps the flag-only
// maintenance commands (flatten/gc/prune) from silently ignoring a stray
// positional argument. flatten is irreversible ("bd flatten mybead --force"
// squashes ALL history), so a swallowed positional is a real footgun
// (beads-ib1u); mirror bd list/bd count and reject positionals loudly.
func TestMaintNoArgsRejectsPositional(t *testing.T) {
	dummy := &cobra.Command{Use: "flatten"}

	t.Run("no_args_ok", func(t *testing.T) {
		if err := maintNoArgs(dummy, nil); err != nil {
			t.Fatalf("maintNoArgs with no args: unexpected error %v", err)
		}
		if err := maintNoArgs(dummy, []string{}); err != nil {
			t.Fatalf("maintNoArgs with empty args: unexpected error %v", err)
		}
	})

	t.Run("positional_rejected", func(t *testing.T) {
		err := maintNoArgs(dummy, []string{"mybead"})
		if err == nil {
			t.Fatal("maintNoArgs with a positional: expected an error, got nil")
		}
		msg := err.Error()
		if !strings.Contains(msg, "does not accept positional arguments") {
			t.Errorf("error message should explain positionals are rejected, got %q", msg)
		}
		if !strings.Contains(msg, "flatten") {
			t.Errorf("error message should name the command (%q) so the user knows which invocation was wrong, got %q", "flatten", msg)
		}
		if !strings.Contains(msg, "--help") {
			t.Errorf("error message should point at --help, got %q", msg)
		}
	})

	t.Run("multiple_positionals_rejected", func(t *testing.T) {
		if err := maintNoArgs(dummy, []string{"a", "b"}); err == nil {
			t.Fatal("maintNoArgs with multiple positionals: expected an error, got nil")
		}
	})
}

// TestMaintNoArgsJSONErrorContract pins the --json contract for the stray-
// positional guard (beads-t1lx): under --json a consumer must get a parseable
// JSON error object on stdout (empty stderr), not the bare plaintext cobra
// prints. cobra runs Args-validators after flag-parse but before the global
// jsonOutput is set, so the guard reads the flag directly off the command.
func TestMaintNoArgsJSONErrorContract(t *testing.T) {
	cmd := &cobra.Command{Use: "flatten"}
	cmd.Flags().Bool("json", true, "")

	// Capture stdout during the guard call.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := maintNoArgs(cmd, []string{"mybead"})

	_ = w.Close()
	os.Stdout = oldStdout
	out, _ := io.ReadAll(r)

	if err == nil {
		t.Fatal("maintNoArgs with a positional under --json: expected an error, got nil")
	}
	// The error must carry a non-zero exit code but NOT be printed as plaintext
	// by main's SilenceErrors path (an *exitError is consumed by
	// exitCodeFromError before the stderr print).
	if _, ok := exitCodeFromError(err); !ok {
		t.Errorf("under --json the guard must return an exitError (consumed before the plaintext stderr print), got %v", err)
	}
	var obj map[string]interface{}
	if jerr := json.Unmarshal(out, &obj); jerr != nil {
		t.Fatalf("under --json stdout must be a parseable JSON error object, got %q (unmarshal: %v)", string(out), jerr)
	}
	if _, ok := obj["error"]; !ok && obj["data"] == nil {
		t.Errorf("JSON error object should carry an 'error' field, got %v", obj)
	}
}

// TestMaintCommandsUseNoArgsGuard ensures the guard is actually wired onto the
// flag-only maintenance commands, not just defined. Without this the fix
// regresses silently if someone drops the Args field.
func TestMaintCommandsUseNoArgsGuard(t *testing.T) {
	for _, c := range []*cobra.Command{flattenCmd, gcCmd, pruneCmd} {
		if c.Args == nil {
			t.Errorf("%s must set an Args validator to reject stray positionals", c.Name())
			continue
		}
		if err := c.Args(c, []string{"stray"}); err == nil {
			t.Errorf("%s must reject a stray positional argument", c.Name())
		}
	}
}
