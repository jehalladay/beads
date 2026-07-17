package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// beads-kb5v: hermetic tests for two pure helpers verified 0% + no test refs:
// prime.go formatPrimeMemoryTimeout and metrics.go marshalIndentNoEscape.

func TestFormatPrimeMemoryTimeout(t *testing.T) {
	t.Run("full (non-compact) heading", func(t *testing.T) {
		out := formatPrimeMemoryTimeout(false, 3*time.Second)
		if !strings.Contains(out, "## Persistent Memories") {
			t.Errorf("expected full heading, got %q", out)
		}
		if !strings.Contains(out, "3s") || !strings.Contains(out, "timed out") {
			t.Errorf("expected the timeout value + message, got %q", out)
		}
	})

	t.Run("compact heading", func(t *testing.T) {
		out := formatPrimeMemoryTimeout(true, 5*time.Second)
		if !strings.Contains(out, "## Memories") || strings.Contains(out, "## Persistent Memories") {
			t.Errorf("expected compact heading, got %q", out)
		}
	})

	t.Run("non-positive timeout falls back to the default", func(t *testing.T) {
		out := formatPrimeMemoryTimeout(false, 0)
		if !strings.Contains(out, primeStoreTimeoutDefault.Round(time.Millisecond).String()) {
			t.Errorf("expected default timeout %s in message, got %q", primeStoreTimeoutDefault, out)
		}
	})
}

func TestMarshalIndentNoEscape(t *testing.T) {
	t.Run("does not HTML-escape and has no trailing newline", func(t *testing.T) {
		data, err := marshalIndentNoEscape(map[string]string{"url": "a<b>&c"})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		s := string(data)
		// SetEscapeHTML(false): < > & stay literal, not < etc.
		if !strings.Contains(s, "a<b>&c") {
			t.Errorf("expected unescaped value, got %q", s)
		}
		if strings.HasSuffix(s, "\n") {
			t.Errorf("expected trailing newline trimmed, got %q", s)
		}
		// Still valid JSON.
		var back map[string]string
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatalf("output not valid JSON: %v", err)
		}
		if back["url"] != "a<b>&c" {
			t.Errorf("round-trip mismatch: %q", back["url"])
		}
	})

	t.Run("indented", func(t *testing.T) {
		data, err := marshalIndentNoEscape(map[string]int{"a": 1})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(data), "\n") {
			t.Errorf("expected indented multi-line output, got %q", data)
		}
	})
}
