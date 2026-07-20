package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestFormatAmbiguousProtoError_sanitize_7n9y asserts the ambiguous-proto
// disambiguation error routes each proto Title through displayTitle
// (ui.SanitizeForTerminal), so a malicious imported proto title carrying an
// OSC/CSI escape cannot inject terminal-control sequences into the error line.
// 7n9y sink-class slice. Mutation-verify: replace displayTitle(m.Title) with
// m.Title and this test goes RED.
func TestFormatAmbiguousProtoError_sanitize_7n9y(t *testing.T) {
	// OSC 52 clipboard-write escape embedded in a proto title.
	evil := "steal\x1b]52;c;ZXZpbA==\x07me"
	matches := []*types.Issue{
		{ID: "beads-aaa", Title: evil},
		{ID: "beads-bbb", Title: "clean title"},
	}
	err := formatAmbiguousProtoError("foo", matches)
	if err == nil {
		t.Fatal("expected an ambiguous-match error, got nil")
	}
	msg := err.Error()
	if strings.Contains(msg, "\x1b]52") {
		t.Errorf("error message leaked raw OSC escape (unsanitized Title): %q", msg)
	}
	// The visible text around the escape must survive sanitization.
	if !strings.Contains(msg, "steal") || !strings.Contains(msg, "me") {
		t.Errorf("sanitized title dropped visible text: %q", msg)
	}
	if !strings.Contains(msg, "clean title") {
		t.Errorf("clean title missing from error: %q", msg)
	}
	if !strings.Contains(msg, "beads-aaa") || !strings.Contains(msg, "beads-bbb") {
		t.Errorf("proto IDs missing from disambiguation error: %q", msg)
	}
}
