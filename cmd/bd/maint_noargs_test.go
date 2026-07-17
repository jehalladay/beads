//go:build cgo

package main

import (
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
