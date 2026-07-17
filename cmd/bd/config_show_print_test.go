package main

import (
	"strings"
	"testing"
)

// beads-zq5p: hermetic test for printConfigEntries (config_show.go), a pure
// stdout formatter (verified 0% + no test refs). Exercised via captureStdout.

func TestPrintConfigEntries(t *testing.T) {
	t.Run("aligns keys and shows source", func(t *testing.T) {
		out := captureStdout(t, func() error {
			printConfigEntries([]configEntry{
				{Key: "a", Value: "1", Source: "default"},
				{Key: "long.key.name", Value: "val", Source: "config.yaml"},
			})
			return nil
		})
		if !strings.Contains(out, "a") || !strings.Contains(out, "long.key.name") {
			t.Errorf("expected both keys in output:\n%s", out)
		}
		if !strings.Contains(out, "(default)") || !strings.Contains(out, "(config.yaml)") {
			t.Errorf("expected both sources in output:\n%s", out)
		}
		// Short key line is padded to the longest key width so the '=' aligns.
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if len(lines) != 2 {
			t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
		}
		eqA := strings.Index(lines[0], "=")
		eqB := strings.Index(lines[1], "=")
		if eqA != eqB {
			t.Errorf("'=' not aligned across rows: %d vs %d\n%s", eqA, eqB, out)
		}
	})

	t.Run("long value is truncated with ellipsis", func(t *testing.T) {
		longVal := strings.Repeat("x", 100)
		out := captureStdout(t, func() error {
			printConfigEntries([]configEntry{{Key: "k", Value: longVal, Source: "env"}})
			return nil
		})
		if strings.Contains(out, longVal) {
			t.Error("the full 100-char value should not appear (should be truncated)")
		}
		if !strings.Contains(out, "...") {
			t.Errorf("expected an ellipsis for the truncated value:\n%s", out)
		}
	})

	t.Run("empty slice prints nothing", func(t *testing.T) {
		out := captureStdout(t, func() error {
			printConfigEntries(nil)
			return nil
		})
		if strings.TrimSpace(out) != "" {
			t.Errorf("expected no output for empty entries, got:\n%s", out)
		}
	})
}
